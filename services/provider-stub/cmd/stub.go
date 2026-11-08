package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Stub is a test double of the provider's v1 contract (contract 1.0.1 of
// ArquitecturasoftwareRenewableEnergy, pinned under
// docs/contracts/providers/renewable-energy-v1). It is NOT our capability and
// NOT the provider's implementation: it exists for local runs and the failure
// drill (ADR-009, ADR-010).
type Stub struct {
	APIKey    string
	Mode      string        // ok | slow | 503 | down-after-n
	Delay     time.Duration // slow: wait before answering
	DownAfter int           // down-after-n: successful answers before 503

	mu     sync.Mutex
	served int
}

const maxBody = 256 << 10

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type detail struct {
	Campo  string `json:"campo"`
	Motivo string `json:"motivo"`
}

func traceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string, details ...detail) {
	if details == nil {
		details = []detail{}
	}
	reply(w, status, map[string]any{"codigo": code, "mensaje": msg, "traceId": traceID(), "detalles": details})
}

func (s *Stub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]string{"service": "provider-stub", "mode": s.mode()})
	})
	mux.HandleFunc("POST /api/v1/estimaciones-consumo", s.estimate)
	return mux
}

func (s *Stub) mode() string {
	if s.Mode == "" {
		return "ok"
	}
	return s.Mode
}

// unavailable applies the failure mode before any work.
func (s *Stub) unavailable(r *http.Request) bool {
	switch s.mode() {
	case "503":
		return true
	case "slow":
		select {
		case <-time.After(s.Delay):
		case <-r.Context().Done():
		}
	case "down-after-n":
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.served >= s.DownAfter {
			return true
		}
		s.served++
	}
	return false
}

func (s *Stub) estimate(w http.ResponseWriter, r *http.Request) {
	if id := r.Header.Get("X-Request-Id"); safeID.MatchString(id) {
		w.Header().Set("X-Request-Id", id)
	}
	key := r.Header.Get("X-API-Key")
	if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(s.APIKey)) != 1 {
		fail(w, 401, "NO_AUTENTICADO", "Se requiere una credencial M2M válida.")
		return
	}
	if s.unavailable(r) {
		w.Header().Set("Retry-After", "30")
		fail(w, 503, "SERVICIO_NO_DISPONIBLE", "La capacidad no está disponible temporalmente.")
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		fail(w, 415, "TIPO_CONTENIDO_NO_SOPORTADO", "Utilice Content-Type application/json.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, 413, "CUERPO_DEMASIADO_GRANDE", "La solicitud supera 256 KiB.")
		return
	}
	var req map[string]json.RawMessage
	if !json.Valid(body) || json.Unmarshal(body, &req) != nil {
		fail(w, 400, "FORMATO_INVALIDO", "El cuerpo no contiene JSON válido.")
		return
	}
	result, bad := compute(req)
	if bad != nil {
		fail(w, 422, "VALIDACION", "Los datos de consumo no cumplen las reglas de la operación.", *bad)
		return
	}
	reply(w, 200, result)
}

// number parses a JSON number (not a string or null) with at most six decimals.
func number(raw json.RawMessage) (*big.Rat, bool) {
	s := string(bytes.TrimSpace(raw))
	if s == "" || !(s[0] == '-' || s[0] >= '0' && s[0] <= '9') {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, false
	}
	return r, new(big.Rat).Mul(r, big.NewRat(1_000_000, 1)).IsInt()
}

func between(r *big.Rat, lo, hi int64, exclusiveLo bool) bool {
	c := r.Cmp(big.NewRat(lo, 1))
	return (c > 0 || (c == 0 && !exclusiveLo)) && r.Cmp(big.NewRat(hi, 1)) <= 0
}

// decimal writes r rounded half-up to six decimals, without trailing zeros.
type decimal string

func (d decimal) MarshalJSON() ([]byte, error) { return []byte(d), nil }

func round6(r *big.Rat) decimal {
	scaled := new(big.Rat).Mul(r, big.NewRat(1_000_000, 1))
	q, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	s := q.String()
	if len(s) < 7 {
		s = strings.Repeat("0", 7-len(s)) + s
	}
	out := strings.TrimRight(s[:len(s)-6]+"."+s[len(s)-6:], "0")
	return decimal(strings.TrimSuffix(out, "."))
}

type line struct {
	ID      string  `json:"id"`
	Energia decimal `json:"energiaPeriodoKWh"`
}

type result struct {
	VersionAlgoritmo  string   `json:"versionAlgoritmo"`
	DiasPeriodo       int64    `json:"diasPeriodo"`
	Desglose          []line   `json:"desglose"`
	TotalPeriodoKWh   decimal  `json:"totalPeriodoKWh"`
	PromedioDiarioKWh decimal  `json:"promedioDiarioKWh"`
	Supuestos         []string `json:"supuestos"`
	TraceID           string   `json:"traceId"`
}

// field is one input rule: integer or decimal within [lo, hi].
type field struct {
	name        string
	integer     bool
	lo, hi      int64
	exclusiveLo bool
}

var (
	powerFields = []field{{"cantidad", true, 1, 1000, false}, {"potenciaW", false, 0, 1_000_000, true},
		{"horasPorDia", false, 0, 24, false}, {"diasUso", true, 0, 366, false}, {"factorFuncionamiento", false, 0, 1, true}}
	cycleFields = []field{{"cantidad", true, 1, 1000, false}, {"energiaPorCicloKWh", false, 0, 1000, true},
		{"ciclosPeriodo", true, 0, 10_000, false}}
)

func unknownKeys(obj map[string]json.RawMessage, allowed ...string) []string {
	var out []string
	for k := range obj {
		known := false
		for _, a := range allowed {
			known = known || k == a
		}
		if !known {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// compute validates like the provider (first violation reported) and applies
// its README formulas with exact rationals:
// POTENCIA kWh = potenciaW x cantidad x horasPorDia x diasUso x factorFuncionamiento / 1000;
// CICLOS kWh = energiaPorCicloKWh x cantidad x ciclosPeriodo.
func compute(req map[string]json.RawMessage) (*result, *detail) {
	if extra := unknownKeys(req, "diasPeriodo", "equipos"); len(extra) > 0 {
		return nil, &detail{extra[0], "Campo desconocido."}
	}
	days, ok := number(req["diasPeriodo"])
	if !ok || !days.IsInt() || !between(days, 1, 366, false) {
		return nil, &detail{"diasPeriodo", "Debe ser un entero entre 1 y 366."}
	}
	var equipos []map[string]json.RawMessage
	if json.Unmarshal(req["equipos"], &equipos) != nil || len(equipos) < 1 || len(equipos) > 100 {
		return nil, &detail{"equipos", "Debe contener entre 1 y 100 equipos."}
	}
	total := new(big.Rat)
	out := &result{VersionAlgoritmo: "consumo-v1", DiasPeriodo: days.Num().Int64(), TraceID: traceID(),
		Supuestos: []string{"Estimación basada en equipos y hábitos declarados; no es una medición real.", "Respuesta del stub local de pruebas."}}
	seen := map[string]bool{}
	for i, e := range equipos {
		at := func(f string) string { return fmt.Sprintf("equipos[%d].%s", i, f) }
		var modo string
		_ = json.Unmarshal(e["modo"], &modo)
		fields := map[string][]field{"POTENCIA": powerFields, "CICLOS": cycleFields}[modo]
		if fields == nil {
			return nil, &detail{at("modo"), "Debe ser POTENCIA o CICLOS."}
		}
		allowed := []string{"id", "modo"}
		for _, f := range fields {
			allowed = append(allowed, f.name)
		}
		if extra := unknownKeys(e, allowed...); len(extra) > 0 {
			return nil, &detail{at(extra[0]), "Campo no permitido para el modo " + modo + "."}
		}
		var id string
		if json.Unmarshal(e["id"], &id) != nil || !safeID.MatchString(id) {
			return nil, &detail{at("id"), "Debe tener de 1 a 64 letras, números, guion o guion bajo."}
		}
		if seen[id] {
			return nil, &detail{at("id"), "Identificador repetido."}
		}
		seen[id] = true
		values := map[string]*big.Rat{}
		for _, f := range fields {
			v, ok := number(e[f.name])
			if !ok || (f.integer && !v.IsInt()) || !between(v, f.lo, f.hi, f.exclusiveLo) {
				return nil, &detail{at(f.name), fmt.Sprintf("Valor fuera de rango o con más de seis decimales (%d a %d).", f.lo, f.hi)}
			}
			values[f.name] = v
		}
		kwh := new(big.Rat)
		if modo == "POTENCIA" {
			if values["diasUso"].Cmp(days) > 0 {
				return nil, &detail{at("diasUso"), fmt.Sprintf("No puede superar diasPeriodo (%s).", days.RatString())}
			}
			kwh.Mul(values["potenciaW"], values["cantidad"])
			kwh.Mul(kwh, values["horasPorDia"]).Mul(kwh, values["diasUso"]).Mul(kwh, values["factorFuncionamiento"])
			kwh.Quo(kwh, big.NewRat(1000, 1))
		} else {
			kwh.Mul(values["energiaPorCicloKWh"], values["cantidad"]).Mul(kwh, values["ciclosPeriodo"])
		}
		total.Add(total, kwh)
		out.Desglose = append(out.Desglose, line{id, round6(kwh)})
	}
	out.TotalPeriodoKWh = round6(total)
	out.PromedioDiarioKWh = round6(new(big.Rat).Quo(total, days))
	return out, nil
}
