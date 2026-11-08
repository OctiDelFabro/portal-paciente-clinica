package main

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var pinned = filepath.Join("..", "..", "..", "docs", "contracts", "providers", "renewable-energy-v1", "examples")

func example(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(pinned, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func do(s *Stub, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/estimaciones-consumo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "k")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Codigo   string `json:"codigo"`
		TraceID  string `json:"traceId"`
		Detalles []struct {
			Campo string `json:"campo"`
		} `json:"detalles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.TraceID == "" || e.Detalles == nil {
		t.Fatalf("error body outside the contract: %s", rec.Body)
	}
	if len(e.Detalles) > 0 {
		return e.Codigo + ":" + e.Detalles[0].Campo
	}
	return e.Codigo
}

// The provider's own example: 6 + 32.4 + 9 = 47.4 kWh, 1.58 kWh/day.
func TestStubAppliesTheProviderFormula(t *testing.T) {
	rec := do(&Stub{APIKey: "k"}, example(t, "solicitud-valida.json"), map[string]string{"X-Request-Id": "corr_1"})
	var got struct {
		VersionAlgoritmo string `json:"versionAlgoritmo"`
		DiasPeriodo      int    `json:"diasPeriodo"`
		Desglose         []struct {
			ID      string      `json:"id"`
			Energia json.Number `json:"energiaPeriodoKWh"`
		} `json:"desglose"`
		Total     json.Number `json:"totalPeriodoKWh"`
		Promedio  json.Number `json:"promedioDiarioKWh"`
		Supuestos []string    `json:"supuestos"`
		TraceID   string      `json:"traceId"`
	}
	dec := json.NewDecoder(rec.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v", rec.Code, err)
	}
	if got.Total != "47.4" || got.Promedio != "1.58" || got.VersionAlgoritmo != "consumo-v1" || got.DiasPeriodo != 30 ||
		len(got.Desglose) != 3 || got.Desglose[0].Energia != "6" || got.Desglose[1].Energia != "32.4" || got.Desglose[2].Energia != "9" ||
		len(got.Supuestos) == 0 || got.TraceID == "" {
		t.Fatalf("got %+v", got)
	}
	if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("X-Request-Id") != "corr_1" {
		t.Fatalf("headers %v", rec.Header())
	}
}

func TestStubRejectsLikeTheContract(t *testing.T) {
	s := &Stub{APIKey: "k"}
	valid := example(t, "solicitud-valida.json")
	cases := []struct {
		name    string
		body    string
		headers map[string]string
		status  int
		code    string
	}{
		{"no key", valid, map[string]string{"X-API-Key": ""}, 401, "NO_AUTENTICADO"},
		{"wrong key", valid, map[string]string{"X-API-Key": "x"}, 401, "NO_AUTENTICADO"},
		{"content type", valid, map[string]string{"Content-Type": "text/plain"}, 415, "TIPO_CONTENIDO_NO_SOPORTADO"},
		{"bad json", "{", nil, 400, "FORMATO_INVALIDO"},
		{"too large", `{"x":"` + strings.Repeat("a", 300<<10) + `"}`, nil, 413, "CUERPO_DEMASIADO_GRANDE"},
		{"diasUso above period", example(t, "solicitud-invalida-dias.json"), nil, 422, "VALIDACION:equipos[0].diasUso"},
		{"unknown field", strings.Replace(valid, `"diasPeriodo"`, `"extra": 1, "diasPeriodo"`, 1), nil, 422, "VALIDACION:extra"},
		{"mixed variants", strings.Replace(valid, `"factorFuncionamiento": 1`, `"factorFuncionamiento": 1, "ciclosPeriodo": 2`, 1), nil, 422, "VALIDACION:equipos[0].ciclosPeriodo"},
		{"seven decimals", strings.Replace(valid, `"factorFuncionamiento": 0.3`, `"factorFuncionamiento": 0.3000001`, 1), nil, 422, "VALIDACION:equipos[1].factorFuncionamiento"},
		{"quoted number", strings.Replace(valid, `"potenciaW": 10`, `"potenciaW": "10"`, 1), nil, 422, "VALIDACION:equipos[0].potenciaW"},
		{"repeated id", strings.Replace(valid, `"heladera"`, `"iluminacion"`, 1), nil, 422, "VALIDACION:equipos[1].id"},
	}
	for _, c := range cases {
		rec := do(s, c.body, c.headers)
		if rec.Code != c.status || code(t, rec) != c.code {
			t.Errorf("%s: got %d %s, want %d %s", c.name, rec.Code, rec.Body, c.status, c.code)
		}
	}
}

func TestStubFailureModes(t *testing.T) {
	valid := example(t, "solicitud-valida.json")
	rec := do(&Stub{APIKey: "k", Mode: "503"}, valid, nil)
	if rec.Code != 503 || code(t, rec) != "SERVICIO_NO_DISPONIBLE" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("503 mode: %d %s", rec.Code, rec.Body)
	}
	down := &Stub{APIKey: "k", Mode: "down-after-n", DownAfter: 2}
	for i, want := range []int{200, 200, 503, 503} {
		if rec := do(down, valid, nil); rec.Code != want {
			t.Fatalf("down-after-n call %d: got %d", i, rec.Code)
		}
	}
	slow := &Stub{APIKey: "k", Mode: "slow", Delay: 150 * time.Millisecond}
	begin := time.Now()
	if rec := do(slow, valid, nil); rec.Code != 200 || time.Since(begin) < 150*time.Millisecond {
		t.Fatalf("slow mode: %d after %v", rec.Code, time.Since(begin))
	}
}

func TestStubOnlyServesTheContractRoute(t *testing.T) {
	h := (&Stub{APIKey: "k"}).Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/estimaciones-consumo", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
}

func TestRound6WritesSmallValuesAndRoundsHalfUp(t *testing.T) {
	cases := map[string]decimal{"381/1000": "0.381", "0": "0", "1/2000000": "0.000001", "1/3": "0.333333", "47": "47"}
	for in, want := range cases {
		r, _ := new(big.Rat).SetString(in)
		if got := round6(r); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}
