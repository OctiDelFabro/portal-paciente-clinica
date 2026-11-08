package energyprovider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

const okBody = `{"versionAlgoritmo":"consumo-v1","diasPeriodo":1,"desglose":[{"id":"pc-consultorio","energiaPeriodoKWh":0.381}],` +
	`"totalPeriodoKWh":0.381,"promedioDiarioKWh":0.381,"supuestos":["x"],"traceId":"t-1"}`

var consumption = domain.ConsultationConsumption(domain.Doctor{Duration: 30 * time.Minute, Specialty: "cardiologia"})

// provider replies with the scripted statuses in order (the last one repeats).
type provider struct {
	statuses []int
	calls    atomic.Int32
	delay    time.Duration
	contentT string
	body     string
	headers  []http.Header
}

func (p *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := int(p.calls.Add(1)) - 1
	p.headers = append(p.headers, r.Header.Clone())
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-r.Context().Done():
			return
		}
	}
	status := p.statuses[min(n, len(p.statuses)-1)]
	ct, body := p.contentT, p.body
	if ct == "" {
		ct = "application/json"
	}
	if body == "" {
		body = okBody
		if status != 200 {
			body = `{"codigo":"SERVICIO_NO_DISPONIBLE","mensaje":"x","traceId":"t","detalles":[]}`
		}
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func newTestClient(url string) *Client {
	c := New(url, "secret-key-123")
	c.Jitter = func() time.Duration { return time.Millisecond }
	return c
}

func start(t *testing.T, p *provider) *Client {
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return newTestClient(srv.URL)
}

func TestEstimateSendsTheContractRequest(t *testing.T) {
	p := &provider{statuses: []int{200}}
	c := start(t, p)
	ctx := obs.WithCorrelationID(context.Background(), "corr.7:abc")
	est, err := c.Estimate(ctx, consumption)
	if err != nil || est.MicroKWh != 381_000 || est.AlgorithmVersion != "consumo-v1" {
		t.Fatalf("got %+v err=%v", est, err)
	}
	h := p.headers[0]
	if h.Get("X-API-Key") != "secret-key-123" || h.Get("Content-Type") != "application/json" || h.Get("X-Request-Id") != "corr_7_abc" {
		t.Fatalf("headers: %v", h)
	}
}

func TestRequestIDIsSanitizedToTheProviderCharset(t *testing.T) {
	long := strings.Repeat("a", 100)
	cases := map[string]string{"corr-1_x": "corr-1_x", "a.b:c": "a_b_c", long: long[:64]}
	for in, want := range cases {
		if got := requestID(obs.WithCorrelationID(context.Background(), in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	if got := requestID(context.Background()); len(got) == 0 || len(got) > 64 || strings.Trim(got, "abcdef0123456789-") != "" {
		t.Errorf("generated id %q", got)
	}
}

func TestEstimateRetriesOnceOn503(t *testing.T) {
	p := &provider{statuses: []int{503, 200}}
	if _, err := start(t, p).Estimate(context.Background(), consumption); err != nil || p.calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d", err, p.calls.Load())
	}
	p = &provider{statuses: []int{503}}
	_, err := start(t, p).Estimate(context.Background(), consumption)
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindUnavailable || p.calls.Load() != 2 {
		t.Fatalf("want one retry then unavailable, got %v calls=%d", err, p.calls.Load())
	}
}

func TestEstimateNeverRetries4xxOr500(t *testing.T) {
	for _, status := range []int{400, 401, 403, 413, 415, 422, 429, 500} {
		p := &provider{statuses: []int{status, 200}}
		_, err := start(t, p).Estimate(context.Background(), consumption)
		if !errors.Is(err, app.ErrEnergyUnavailable) || p.calls.Load() != 1 {
			t.Errorf("%d: err=%v calls=%d", status, err, p.calls.Load())
		}
	}
}

func TestEstimateRetriesOnceOnConnectionFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close() // connection reset before any response
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	_, err = newTestClient("http://"+l.Addr().String()).Estimate(context.Background(), consumption)
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindTransport || accepted.Load() != 2 {
		t.Fatalf("got %v accepted=%d", err, accepted.Load())
	}
}

// Infrastructure answers outside the schema are provider failures, never a business 200.
func TestEstimateRejectsResponsesOutsideTheContract(t *testing.T) {
	cases := map[string]*provider{
		"html 200":        {statuses: []int{200}, contentT: "text/html", body: "<html>waking up</html>"},
		"json not schema": {statuses: []int{200}, body: `{"total":1}`},
		"unknown field":   {statuses: []int{200}, body: strings.Replace(okBody, `"traceId"`, `"extra":1,"traceId"`, 1)},
		"string number":   {statuses: []int{200}, body: strings.Replace(okBody, `"totalPeriodoKWh":0.381`, `"totalPeriodoKWh":"0.381"`, 1)},
		"negative total":  {statuses: []int{200}, body: strings.Replace(okBody, `"totalPeriodoKWh":0.381`, `"totalPeriodoKWh":-1`, 1)},
		"other algorithm": {statuses: []int{200}, body: strings.Replace(okBody, "consumo-v1", "consumo-v9", 1)},
		"html 404":        {statuses: []int{404}, contentT: "text/html", body: "<html>no</html>"},
	}
	for name, p := range cases {
		_, err := start(t, p).Estimate(context.Background(), consumption)
		var pe *Error
		if !errors.As(err, &pe) || pe.Kind != KindBadResponse {
			t.Errorf("%s: got %v", name, err)
		}
	}
	// A proxy's 503 page without the error schema is still "unavailable" and retried.
	p := &provider{statuses: []int{503, 200}, contentT: "text/html", body: "<html>busy</html>"}
	if _, err := start(t, p).Estimate(context.Background(), consumption); p.calls.Load() != 2 || err == nil {
		t.Errorf("proxy 503: err=%v calls=%d", err, p.calls.Load())
	}
}

func TestEstimateRespectsTheBudget(t *testing.T) {
	p := &provider{statuses: []int{200}, delay: 2 * time.Second}
	c := start(t, p)
	c.Budget = 150 * time.Millisecond
	begin := time.Now()
	_, err := c.Estimate(context.Background(), consumption)
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindTimeout || time.Since(begin) > time.Second {
		t.Fatalf("got %v after %v", err, time.Since(begin))
	}
	// A shorter caller deadline wins over the client budget.
	c.Budget = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	begin = time.Now()
	if _, err := c.Estimate(ctx, consumption); err == nil || time.Since(begin) > time.Second {
		t.Fatalf("caller deadline ignored: %v after %v", err, time.Since(begin))
	}
}

func TestOpenCircuitSkipsTheProvider(t *testing.T) {
	p := &provider{statuses: []int{500}}
	c := start(t, p)
	now := time.Unix(0, 0)
	c.Breaker = &Breaker{Threshold: 2, OpenFor: 30 * time.Second, Now: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		_, _ = c.Estimate(context.Background(), consumption)
	}
	_, err := c.Estimate(context.Background(), consumption)
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindCircuitOpen || p.calls.Load() != 2 {
		t.Fatalf("got %v calls=%d", err, p.calls.Load())
	}
	now = now.Add(31 * time.Second)
	p.statuses = []int{200}
	if _, err := c.Estimate(context.Background(), consumption); err != nil || c.Breaker.State() != StateClosed {
		t.Fatalf("half-open probe should close the circuit: %v state=%v", err, c.Breaker.State())
	}
}

func TestFailuresLogProviderUnavailableWithoutTheKey(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	_, _ = start(t, &provider{statuses: []int{422}}).Estimate(context.Background(), consumption)
	out := buf.String()
	if !strings.Contains(out, `"event":"provider.unavailable"`) || !strings.Contains(out, `"outcome":"invalid_request"`) || !strings.Contains(out, `"level":"WARN"`) {
		t.Fatalf("log: %s", out)
	}
	if strings.Contains(out, "secret-key-123") {
		t.Fatalf("API key leaked to logs: %s", out)
	}
}
