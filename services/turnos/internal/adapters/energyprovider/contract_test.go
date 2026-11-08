package energyprovider

// Consumer contract test (ADR-009): our requests and the provider's published
// examples are checked against the pinned copy of their OpenAPI, without
// network. Refreshing docs/contracts/providers/renewable-energy-v1 with an
// incompatible provider change (new required field, changed type or enum,
// new error status) makes this test fail.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

var pinned = filepath.Join("..", "..", "..", "..", "..", "docs", "contracts", "providers", "renewable-energy-v1")

func decodeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func readPinned(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(pinned, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// openAPI is the subset of JSON Schema (OpenAPI 3.0.3 dialect) the provider
// contract uses: $ref, oneOf, type, required, properties,
// additionalProperties:false, enum, minimum/maximum with boolean
// exclusiveMinimum/exclusiveMaximum, minLength/maxLength, pattern,
// minItems/maxItems and items. An unsupported keyword fails the test so the
// validator never silently accepts a schema it does not understand.
type openAPI struct {
	t    *testing.T
	root map[string]any
}

func loadOpenAPI(t *testing.T) openAPI {
	return openAPI{t: t, root: decodeJSON(t, readPinned(t, "openapi.json")).(map[string]any)}
}

func (o openAPI) schema(name string) map[string]any {
	s, ok := o.root["components"].(map[string]any)["schemas"].(map[string]any)[name].(map[string]any)
	if !ok {
		o.t.Fatalf("schema %s missing from the pinned contract", name)
	}
	return s
}

var supported = map[string]bool{"$ref": true, "oneOf": true, "discriminator": true, "type": true, "required": true,
	"properties": true, "additionalProperties": true, "enum": true, "minimum": true, "maximum": true,
	"exclusiveMinimum": true, "exclusiveMaximum": true, "minLength": true, "maxLength": true, "pattern": true,
	"minItems": true, "maxItems": true, "items": true, "format": true, "description": true}

func rat(v any) *big.Rat {
	r, ok := new(big.Rat).SetString(fmt.Sprint(v))
	if !ok {
		return nil
	}
	return r
}

func intOf(v any) int { n, _ := strconv.Atoi(fmt.Sprint(v)); return n }

// validate returns one message per violation of v against s, prefixed by path.
func (o openAPI) validate(s map[string]any, v any, path string) []string {
	for k := range s {
		if !supported[k] {
			o.t.Fatalf("%s: unsupported schema keyword %q; extend the validator", path, k)
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		return o.validate(o.schema(strings.TrimPrefix(ref, "#/components/schemas/")), v, path)
	}
	if alts, ok := s["oneOf"].([]any); ok {
		matches := 0
		for _, alt := range alts {
			if len(o.validate(alt.(map[string]any), v, path)) == 0 {
				matches++
			}
		}
		if matches != 1 {
			return []string{fmt.Sprintf("%s: matches %d oneOf alternatives, want 1", path, matches)}
		}
		return nil
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, path+": "+fmt.Sprintf(format, args...)) }
	switch s["type"] {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return []string{path + ": want object"}
		}
		props, _ := s["properties"].(map[string]any)
		for _, r := range asSlice(s["required"]) {
			if _, ok := obj[r.(string)]; !ok {
				fail("missing required %s", r)
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ps, known := props[k].(map[string]any)
			if !known {
				if s["additionalProperties"] == false {
					fail("unknown property %s", k)
				}
				continue
			}
			errs = append(errs, o.validate(ps, obj[k], path+"."+k)...)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return []string{path + ": want array"}
		}
		if m, ok := s["minItems"]; ok && len(arr) < intOf(m) {
			fail("fewer than %v items", m)
		}
		if m, ok := s["maxItems"]; ok && len(arr) > intOf(m) {
			fail("more than %v items", m)
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, it := range arr {
				errs = append(errs, o.validate(items, it, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case "string":
		str, ok := v.(string)
		if !ok {
			return []string{path + ": want string"}
		}
		n := utf8.RuneCountInString(str)
		if m, ok := s["minLength"]; ok && n < intOf(m) {
			fail("shorter than %v", m)
		}
		if m, ok := s["maxLength"]; ok && n > intOf(m) {
			fail("longer than %v", m)
		}
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(str) {
			fail("does not match %s", p)
		}
		if enum, ok := s["enum"]; ok && !contains(asSlice(enum), str) {
			fail("%q not in enum %v", str, enum)
		}
	case "integer", "number":
		num, ok := v.(json.Number)
		if !ok {
			return []string{path + ": want number"}
		}
		r := rat(num)
		if s["type"] == "integer" && (r == nil || !r.IsInt() || strings.ContainsAny(string(num), ".eE")) {
			return []string{path + ": want integer"}
		}
		if m, ok := s["minimum"]; ok {
			if c := r.Cmp(rat(m)); c < 0 || (c == 0 && s["exclusiveMinimum"] == true) {
				fail("%s below minimum %v (exclusive=%v)", num, m, s["exclusiveMinimum"])
			}
		}
		if m, ok := s["maximum"]; ok {
			if c := r.Cmp(rat(m)); c > 0 || (c == 0 && s["exclusiveMaximum"] == true) {
				fail("%s above maximum %v", num, m)
			}
		}
	default:
		o.t.Fatalf("%s: unsupported type %v", path, s["type"])
	}
	return errs
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

func contains(list []any, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// crossRules are the README's normative rules beyond the schema: unique ids,
// diasUso <= diasPeriodo and at most six decimals in every input number.
func crossRules(v any) []string {
	var errs []string
	req := v.(map[string]any)
	days := rat(req["diasPeriodo"])
	seen := map[string]bool{}
	for i, e := range asSlice(req["equipos"]) {
		eq := e.(map[string]any)
		id := fmt.Sprint(eq["id"])
		if seen[id] {
			errs = append(errs, fmt.Sprintf("equipos[%d].id %s repeated", i, id))
		}
		seen[id] = true
		if d, ok := eq["diasUso"]; ok && rat(d).Cmp(days) > 0 {
			errs = append(errs, fmt.Sprintf("equipos[%d].diasUso above diasPeriodo", i))
		}
	}
	var walk func(any, string)
	walk = func(x any, path string) {
		switch t := x.(type) {
		case map[string]any:
			for k, c := range t {
				walk(c, path+"."+k)
			}
		case []any:
			for i, c := range t {
				walk(c, fmt.Sprintf("%s[%d]", path, i))
			}
		case json.Number:
			if _, exact, err := domain.ParseScaled(string(t), 6); err != nil || !exact {
				errs = append(errs, fmt.Sprintf("%s: %s has more than six decimals", path, t))
			}
		}
	}
	walk(v, "$")
	return errs
}

// (a) Every request body the adapter builds, for every equipment profile and
// usual slot durations, is valid against SolicitudEstimacion and the README rules.
func TestEveryRequestWeBuildMatchesThePinnedSchema(t *testing.T) {
	o := loadOpenAPI(t)
	schema := o.schema("SolicitudEstimacion")
	for _, specialty := range append(domain.Specialties(), "clinica-medica") {
		for _, minutes := range []int{10, 15, 20, 30, 45, 60, 90} {
			c := domain.ConsultationConsumption(domain.Doctor{Duration: time.Duration(minutes) * time.Minute, Specialty: specialty})
			body, err := requestBody(c)
			if err != nil {
				t.Fatal(err)
			}
			v := decodeJSON(t, body)
			if errs := append(o.validate(schema, v, "$"), crossRules(v)...); len(errs) > 0 {
				t.Errorf("%s %d min: %s\n%s", specialty, minutes, strings.Join(errs, "; "), body)
			}
		}
	}
}

// The validator is not vacuous: the provider's valid example passes and
// mutations that break the contract are caught.
func TestTheValidatorRejectsIncompatibleRequests(t *testing.T) {
	o := loadOpenAPI(t)
	schema := o.schema("SolicitudEstimacion")
	valid := readPinned(t, "examples/solicitud-valida.json")
	if errs := append(o.validate(schema, decodeJSON(t, valid), "$"), crossRules(decodeJSON(t, valid))...); len(errs) > 0 {
		t.Fatalf("valid example rejected: %v", errs)
	}
	if errs := crossRules(decodeJSON(t, readPinned(t, "examples/solicitud-invalida-dias.json"))); len(errs) == 0 {
		t.Fatal("diasUso above diasPeriodo not detected")
	}
	mutations := map[string]func(map[string]any){
		"unknown field":   func(r map[string]any) { r["extra"] = true },
		"missing equipos": func(r map[string]any) { delete(r, "equipos") },
		"unknown mode":    func(r map[string]any) { first(r)["modo"] = "SOLAR" },
		"mixed variants":  func(r map[string]any) { first(r)["ciclosPeriodo"] = json.Number("1") },
		"zero power":      func(r map[string]any) { first(r)["potenciaW"] = json.Number("0") },
		"float quantity":  func(r map[string]any) { first(r)["cantidad"] = json.Number("1.5") },
		"string number":   func(r map[string]any) { first(r)["horasPorDia"] = "5" },
		"too many hours":  func(r map[string]any) { first(r)["horasPorDia"] = json.Number("25") },
		"bad id":          func(r map[string]any) { first(r)["id"] = "luz sala" },
	}
	for name, mutate := range mutations {
		v := decodeJSON(t, valid).(map[string]any)
		mutate(v)
		if errs := o.validate(schema, v, "$"); len(errs) == 0 {
			t.Errorf("%s: not detected", name)
		}
	}
}

func first(r map[string]any) map[string]any { return asSlice(r["equipos"])[0].(map[string]any) }

// expectedKind is how the adapter must classify each status the contract declares.
var expectedKind = map[int]Kind{
	400: KindInvalidRequest, 401: KindUnauthorized, 403: KindForbidden, 413: KindInvalidRequest,
	415: KindInvalidRequest, 422: KindInvalidRequest, 429: KindRateLimited, 500: KindInternal, 503: KindUnavailable,
}

// (b) Every pinned example response is valid against its schema and is
// decoded by the adapter's parser into an estimate or the right typed error.
func TestEveryPinnedExampleResponseIsParsed(t *testing.T) {
	o := loadOpenAPI(t)
	ok := readPinned(t, "examples/respuesta-200.json")
	if errs := o.validate(o.schema("ResultadoEstimacion"), decodeJSON(t, ok), "$"); len(errs) > 0 {
		t.Fatalf("200 example invalid: %v", errs)
	}
	est, err := parseResponse(200, "application/json", nil, ok)
	if err != nil || est != (app.EnergyEstimate{MicroKWh: 47_400_000, AlgorithmVersion: "consumo-v1"}) {
		t.Fatalf("200: got %+v err=%v", est, err)
	}
	for status, kind := range expectedKind {
		raw := readPinned(t, fmt.Sprintf("examples/respuesta-%d.json", status))
		body := decodeJSON(t, raw)
		if errs := o.validate(o.schema("Error"), body, "$"); len(errs) > 0 {
			t.Errorf("%d example invalid: %v", status, errs)
		}
		_, err := parseResponse(status, "application/json; charset=utf-8", nil, raw)
		var pe *Error
		if !errors.As(err, &pe) || pe.Kind != kind || pe.Status != status || pe.Code != body.(map[string]any)["codigo"] || !errors.Is(err, app.ErrEnergyUnavailable) {
			t.Errorf("%d: got %#v", status, err)
		}
	}
}

// A new response status in the provider's contract must be classified here first.
func TestThePinnedContractDeclaresOnlyKnownStatuses(t *testing.T) {
	o := loadOpenAPI(t)
	op := o.root["paths"].(map[string]any)["/api/v1/estimaciones-consumo"].(map[string]any)["post"].(map[string]any)
	for code := range op["responses"].(map[string]any) {
		status, _ := strconv.Atoi(code)
		if _, known := expectedKind[status]; !known && status != 200 {
			t.Errorf("status %s declared by the provider is not classified by the adapter", code)
		}
	}
	if _, ok := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"]; !ok {
		t.Error("request content type is no longer application/json")
	}
	for _, p := range asSlice(op["parameters"]) {
		if p.(map[string]any)["name"] == "X-Request-Id" && p.(map[string]any)["schema"].(map[string]any)["pattern"] != "^[A-Za-z0-9_-]+$" {
			t.Error("X-Request-Id pattern changed; update requestID")
		}
	}
}
