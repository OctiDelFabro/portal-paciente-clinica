// Package opensearch implements app.Index over the OpenSearch REST API with
// plain net/http (ADR-006). Reads and writes go through the alias "doctors";
// a rebuild creates doctors-vN+1 and moves the alias in one atomic call.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/medicos/internal/app"
	"portal-paciente-clinica/services/medicos/internal/domain"
)

const (
	Alias       = "doctors"
	indexPrefix = Alias + "-v"
	timeout     = 3 * time.Second
)

// mapping follows the ADR-006 field table; strict so a typo never creates fields.
const mapping = `{"settings":{"number_of_shards":1,"number_of_replicas":0},"mappings":{"dynamic":"strict","properties":{` +
	`"doctorId":{"type":"keyword"},` +
	`"fullName":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
	`"specialties":{"type":"keyword"},` +
	`"acceptsMinors":{"type":"boolean"},` +
	`"active":{"type":"boolean"},` +
	`"nextAvailableAt":{"type":"date"},` +
	`"profileVersion":{"type":"long"}}}}`

type Index struct {
	URL  string
	HTTP *http.Client
}

func New(baseURL string) *Index {
	return &Index{URL: strings.TrimRight(baseURL, "/"), HTTP: obs.Client(timeout)}
}

// do sends one request. Transport failures and 5xx answers are reported as
// app.ErrSearchUnavailable; other statuses are returned to the caller.
func (ix *Index) do(ctx context.Context, method, path string, body []byte, contentType string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, ix.URL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := ix.HTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", app.ErrSearchUnavailable, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", app.ErrSearchUnavailable, err)
	}
	if resp.StatusCode >= 500 {
		return resp.StatusCode, out, fmt.Errorf("%w: %s %s returned %d", app.ErrSearchUnavailable, method, path, resp.StatusCode)
	}
	return resp.StatusCode, out, nil
}

func (ix *Index) doJSON(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	return ix.do(ctx, method, path, body, "application/json")
}

// Ping reports whether the cluster answers; used by /healthz.
func (ix *Index) Ping(ctx context.Context) error {
	code, _, err := ix.doJSON(ctx, "GET", "/_cluster/health", nil)
	if err == nil && code != 200 {
		err = fmt.Errorf("%w: cluster health %d", app.ErrSearchUnavailable, code)
	}
	return err
}

// AliasExists tells whether the alias was already created by a rebuild.
func (ix *Index) AliasExists(ctx context.Context) (bool, error) {
	code, _, err := ix.doJSON(ctx, "GET", "/_alias/"+Alias, nil)
	if err != nil {
		return false, err
	}
	return code == 200, nil
}

// Upsert writes doc with external_gte versioning on profileVersion: an event
// carrying an older profile never overwrites a newer one, while availability
// updates of the same profile version are accepted. require_alias avoids
// auto-creating a concrete "doctors" index before the first rebuild.
func (ix *Index) Upsert(ctx context.Context, doc domain.SearchDoc) error {
	body, _ := json.Marshal(doc)
	params := url.Values{"version": {strconv.FormatInt(doc.ProfileVersion, 10)}, "version_type": {"external_gte"}, "require_alias": {"true"}}
	code, out, err := ix.doJSON(ctx, "PUT", "/"+Alias+"/_doc/"+url.PathEscape(doc.DoctorID)+"?"+params.Encode(), body)
	switch {
	case err != nil:
		return err
	case code == 200 || code == 201:
		return nil
	case code == 409:
		return nil // a newer profileVersion is already indexed
	default:
		return fmt.Errorf("index doc %s: status %d: %s", doc.DoctorID, code, truncate(out))
	}
}

func (ix *Index) Search(ctx context.Context, q domain.Query) (domain.Page, error) {
	body, _ := json.Marshal(searchBody(q))
	code, out, err := ix.doJSON(ctx, "POST", "/"+Alias+"/_search", body)
	if err != nil {
		return domain.Page{}, err
	}
	if code == 404 {
		return domain.Page{}, fmt.Errorf("%w: alias %s not created yet", app.ErrSearchUnavailable, Alias)
	}
	if code != 200 {
		return domain.Page{}, fmt.Errorf("search: status %d: %s", code, truncate(out))
	}
	var res struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source domain.SearchDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return domain.Page{}, fmt.Errorf("decode search response: %w", err)
	}
	page := domain.Page{Total: res.Hits.Total.Value, Items: make([]domain.SearchDoc, 0, len(res.Hits.Hits))}
	for _, h := range res.Hits.Hits {
		page.Items = append(page.Items, h.Source)
	}
	return page, nil
}

func searchBody(q domain.Query) map[string]any {
	filter := []any{term("active", true)}
	if q.Specialty != "" {
		filter = append(filter, term("specialties", q.Specialty))
	}
	if q.AcceptsMinors != nil {
		filter = append(filter, term("acceptsMinors", *q.AcceptsMinors))
	}
	boolQ := map[string]any{"filter": filter}
	if q.Q != "" {
		boolQ["must"] = []any{map[string]any{"match": map[string]any{"fullName": map[string]any{"query": q.Q, "operator": "and", "fuzziness": "AUTO"}}}}
	}
	byName := []any{map[string]any{"fullName.keyword": "asc"}, map[string]any{"doctorId": "asc"}}
	var sortBy []any
	switch {
	case q.Sort == domain.SortNextAvailable:
		sortBy = append([]any{map[string]any{"nextAvailableAt": map[string]any{"order": "asc", "missing": "_last"}}}, byName...)
	case q.Sort == domain.SortName || q.Q == "":
		sortBy = byName
	default:
		sortBy = []any{map[string]any{"_score": "desc"}, map[string]any{"doctorId": "asc"}}
	}
	return map[string]any{"from": q.From(), "size": q.Size, "track_total_hits": true,
		"query": map[string]any{"bool": boolQ}, "sort": sortBy}
}

func term(field string, v any) map[string]any {
	return map[string]any{"term": map[string]any{field: v}}
}

// Rebuild creates doctors-vN+1, bulk-loads docs, moves the alias atomically
// and finally deletes the previous versions. If any step before the alias
// swap fails, searches keep using the previous index.
func (ix *Index) Rebuild(ctx context.Context, docs []domain.SearchDoc) (string, error) {
	code, out, err := ix.doJSON(ctx, "GET", "/"+indexPrefix+"*/_alias", nil)
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("list indices: status %d: %s", code, truncate(out))
	}
	var existing map[string]struct {
		Aliases map[string]any `json:"aliases"`
	}
	if err := json.Unmarshal(out, &existing); err != nil {
		return "", fmt.Errorf("decode indices: %w", err)
	}
	latest, names, current := 0, []string{}, []string{}
	for name, info := range existing {
		if n, err := strconv.Atoi(strings.TrimPrefix(name, indexPrefix)); err == nil && n > latest {
			latest = n
		}
		names = append(names, name)
		if _, ok := info.Aliases[Alias]; ok {
			current = append(current, name)
		}
	}
	sort.Strings(current)
	next := indexPrefix + strconv.Itoa(latest+1)

	if code, out, err = ix.doJSON(ctx, "PUT", "/"+next, []byte(mapping)); err != nil || code != 200 {
		return "", statusErr("create "+next, code, out, err)
	}
	var bulk bytes.Buffer
	for _, d := range docs {
		meta, _ := json.Marshal(map[string]any{"index": map[string]any{"_id": d.DoctorID, "version": d.ProfileVersion, "version_type": "external_gte"}})
		src, _ := json.Marshal(d)
		bulk.Write(meta)
		bulk.WriteByte('\n')
		bulk.Write(src)
		bulk.WriteByte('\n')
	}
	if len(docs) > 0 {
		code, out, err = ix.do(ctx, "POST", "/"+next+"/_bulk?refresh=true", bulk.Bytes(), "application/x-ndjson")
		if err != nil || code != 200 {
			return "", statusErr("bulk "+next, code, out, err)
		}
		var res struct {
			Errors bool `json:"errors"`
		}
		if err := json.Unmarshal(out, &res); err != nil || res.Errors {
			return "", fmt.Errorf("bulk %s reported item errors: %s", next, truncate(out))
		}
	}
	actions := []any{}
	for _, name := range current {
		actions = append(actions, map[string]any{"remove": map[string]string{"alias": Alias, "index": name}})
	}
	actions = append(actions, map[string]any{"add": map[string]string{"alias": Alias, "index": next}})
	body, _ := json.Marshal(map[string]any{"actions": actions})
	if code, out, err = ix.doJSON(ctx, "POST", "/_aliases", body); err != nil || code != 200 {
		return "", statusErr("swap alias", code, out, err)
	}
	for _, name := range names {
		// Old versions are no longer reachable; a failed delete only wastes disk.
		_, _, _ = ix.doJSON(ctx, "DELETE", "/"+name, nil)
	}
	return next, nil
}

func statusErr(op string, code int, out []byte, err error) error {
	if err != nil {
		return err
	}
	return errors.New(op + ": status " + strconv.Itoa(code) + ": " + truncate(out))
}

func truncate(b []byte) string {
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}
