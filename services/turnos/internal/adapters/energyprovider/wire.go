package energyprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"time"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Wire format of the provider contract (ArquitecturasoftwareRenewableEnergy,
// POST /api/v1/estimaciones-consumo, contract 1.0.1, pinned under
// docs/contracts/providers/renewable-energy-v1). Nothing here leaves the package.

// Kind classifies a failed call; it is the outcome label of provider_requests_total.
type Kind string

const (
	KindInvalidRequest Kind = "invalid_request" // 400, 413, 415, 422: our request is wrong
	KindUnauthorized   Kind = "unauthorized"    // 401: missing or wrong PROVIDER_ENERGY_API_KEY
	KindForbidden      Kind = "forbidden"       // 403: key without permission estimar-consumo
	KindRateLimited    Kind = "rate_limited"    // 429: over the agreed 60/min
	KindInternal       Kind = "internal"        // 500
	KindUnavailable    Kind = "unavailable"     // 502, 503, 504 (also proxy pages)
	KindBadResponse    Kind = "bad_response"    // outside the contract: content type, schema, status
	KindTransport      Kind = "transport"       // connection refused, reset, DNS
	KindTimeout        Kind = "timeout"         // budget exhausted
	KindCircuitOpen    Kind = "circuit_open"    // rejected locally, no request sent
)

// Error is a typed provider failure. It unwraps to app.ErrEnergyUnavailable,
// the only error the application layer knows.
type Error struct {
	Kind       Kind
	Status     int    // HTTP status, 0 without response
	Code       string // provider "codigo" when the body follows the Error schema
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("energy provider: %s (status %d, code %q)", e.Kind, e.Status, e.Code)
}

func (e *Error) Unwrap() error { return app.ErrEnergyUnavailable }

const (
	diasPeriodo = 1 // one consultation happens on a single day
	diasUso     = 1
)

type powerWire struct {
	ID                   string  `json:"id"`
	Modo                 string  `json:"modo"`
	Cantidad             int     `json:"cantidad"`
	PotenciaW            float64 `json:"potenciaW"`
	HorasPorDia          float64 `json:"horasPorDia"`
	DiasUso              int     `json:"diasUso"`
	FactorFuncionamiento float64 `json:"factorFuncionamiento"`
}

type cyclesWire struct {
	ID                 string  `json:"id"`
	Modo               string  `json:"modo"`
	Cantidad           int     `json:"cantidad"`
	EnergiaPorCicloKWh float64 `json:"energiaPorCicloKWh"`
	CiclosPeriodo      int     `json:"ciclosPeriodo"`
}

// requestBody translates a consultation into SolicitudEstimacion: the slot
// duration is horasPorDia on one day of use in a one-day period.
func requestBody(c domain.Consumption) ([]byte, error) {
	equipos := make([]any, 0, len(c.Equipment))
	for _, e := range c.Equipment {
		switch e.Mode {
		case domain.ModePower:
			equipos = append(equipos, powerWire{e.ID, "POTENCIA", e.Quantity, e.PowerW, c.Hours, diasUso, e.DutyFactor})
		case domain.ModeCycles:
			equipos = append(equipos, cyclesWire{e.ID, "CICLOS", e.Quantity, e.EnergyPerCycleKWh, e.Cycles})
		default:
			return nil, fmt.Errorf("equipment %s: unknown mode %q", e.ID, e.Mode)
		}
	}
	return json.Marshal(struct {
		DiasPeriodo int   `json:"diasPeriodo"`
		Equipos     []any `json:"equipos"`
	}{diasPeriodo, equipos})
}

// resultWire is ResultadoEstimacion. Numbers stay raw so that a quoted
// number ("0.381") is rejected as the schema requires.
type resultWire struct {
	VersionAlgoritmo  *string         `json:"versionAlgoritmo"`
	DiasPeriodo       *int            `json:"diasPeriodo"`
	Desglose          []consumoWire   `json:"desglose"`
	TotalPeriodoKWh   json.RawMessage `json:"totalPeriodoKWh"`
	PromedioDiarioKWh json.RawMessage `json:"promedioDiarioKWh"`
	Supuestos         []string        `json:"supuestos"`
	TraceID           *string         `json:"traceId"`
}

type consumoWire struct {
	ID                *string         `json:"id"`
	EnergiaPeriodoKWh json.RawMessage `json:"energiaPeriodoKWh"`
}

type errorWire struct {
	Codigo   *string `json:"codigo"`
	Mensaje  *string `json:"mensaje"`
	TraceID  *string `json:"traceId"`
	Detalles []struct {
		Campo  *string `json:"campo"`
		Motivo *string `json:"motivo"`
	} `json:"detalles"`
}

var errorCodes = map[string]bool{"FORMATO_INVALIDO": true, "NO_AUTENTICADO": true, "SIN_PERMISO": true,
	"CUERPO_DEMASIADO_GRANDE": true, "TIPO_CONTENIDO_NO_SOPORTADO": true, "VALIDACION": true,
	"LIMITE_DE_TRAFICO": true, "ERROR_INTERNO": true, "SERVICIO_NO_DISPONIBLE": true}

func strict(body []byte, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return false
	}
	return !dec.More()
}

// microKWh converts a raw JSON number >= 0 to micro-kWh (half-up rounding).
func microKWh(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || raw[0] == '"' || raw[0] == 'n' {
		return 0, false
	}
	v, _, err := domain.ParseScaled(string(raw), 6)
	return v, err == nil
}

func isJSON(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	return err == nil && media == "application/json"
}

func kindForStatus(status int) Kind {
	switch status {
	case 400, 413, 415, 422:
		return KindInvalidRequest
	case 401:
		return KindUnauthorized
	case 403:
		return KindForbidden
	case 429:
		return KindRateLimited
	case 500:
		return KindInternal
	case 502, 503, 504:
		return KindUnavailable
	}
	return KindBadResponse
}

// parseResponse never trusts a response that does not follow the contract:
// a 200 must be application/json and valid ResultadoEstimacion; anything else
// is a typed Error.
func parseResponse(status int, contentType string, header http.Header, body []byte) (app.EnergyEstimate, error) {
	if status == http.StatusOK {
		var r resultWire
		bad := &Error{Kind: KindBadResponse, Status: status}
		if !isJSON(contentType) || !strict(body, &r) {
			return app.EnergyEstimate{}, bad
		}
		total, ok := microKWh(r.TotalPeriodoKWh)
		_, avgOK := microKWh(r.PromedioDiarioKWh)
		valid := ok && avgOK && r.VersionAlgoritmo != nil && *r.VersionAlgoritmo == "consumo-v1" &&
			r.DiasPeriodo != nil && *r.DiasPeriodo >= 1 && *r.DiasPeriodo <= 366 &&
			len(r.Desglose) >= 1 && len(r.Desglose) <= 100 && len(r.Supuestos) >= 1 &&
			r.TraceID != nil && len(*r.TraceID) >= 1 && len(*r.TraceID) <= 128
		for _, d := range r.Desglose {
			_, eOK := microKWh(d.EnergiaPeriodoKWh)
			valid = valid && d.ID != nil && *d.ID != "" && eOK
		}
		if !valid {
			return app.EnergyEstimate{}, bad
		}
		return app.EnergyEstimate{MicroKWh: total, AlgorithmVersion: *r.VersionAlgoritmo}, nil
	}
	e := &Error{Kind: kindForStatus(status), Status: status}
	var w errorWire
	if isJSON(contentType) && strict(body, &w) && w.Codigo != nil && errorCodes[*w.Codigo] && w.Mensaje != nil && w.TraceID != nil && w.Detalles != nil {
		e.Code = *w.Codigo
	}
	if header != nil {
		if s, err := strconv.Atoi(header.Get("Retry-After")); err == nil && s > 0 {
			e.RetryAfter = time.Duration(s) * time.Second
		}
	}
	return app.EnergyEstimate{}, e
}
