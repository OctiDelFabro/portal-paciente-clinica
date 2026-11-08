package httpapi

import (
	"net/http"
	"time"

	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Portal price breakdown (ADR-009). Amounts are exact decimals written as JSON
// numbers from integer cents and micro-kWh, so no float drift reaches the UI.
// The partner API (turnos-partner-v1) never includes it.

// pendingMessage is what the patient reads when the provider did not answer.
const pendingMessage = "Costo energético pendiente: no pudimos obtener la estimación del proveedor; se calculará automáticamente y no se cobra mientras tanto."

// decimal is a JSON number written verbatim ("25068.58").
type decimal string

func (d decimal) MarshalJSON() ([]byte, error) { return []byte(d), nil }

type energyDTO struct {
	Status           string   `json:"status"`
	KWh              *decimal `json:"kWh"`
	CostArs          *decimal `json:"costArs"`
	AlgorithmVersion *string  `json:"algorithmVersion"`
	Message          string   `json:"message,omitempty"`
}

type priceDTO struct {
	BasePriceArs decimal   `json:"basePriceArs"`
	Energy       energyDTO `json:"energy"`
	TotalArs     decimal   `json:"totalArs"`
}

func ars(cents int64) decimal { return decimal(domain.FormatScaled(cents, 2)) }

func toPriceDTO(p domain.Price) priceDTO {
	e := energyDTO{Status: string(p.Energy.Status)}
	if p.Energy.Status == domain.EnergyEstimated {
		kwh := decimal(domain.TrimDecimal(domain.FormatScaled(p.Energy.MicroKWh, 6)))
		cost := ars(p.Energy.CostCents)
		version := p.Energy.AlgorithmVersion
		e.KWh, e.CostArs, e.AlgorithmVersion = &kwh, &cost, &version
	} else {
		e.Status, e.Message = string(domain.EnergyPending), pendingMessage
	}
	return priceDTO{BasePriceArs: ars(p.BaseCents), Energy: e, TotalArs: ars(p.TotalCents)}
}

// portalAppointmentDTO adds the price to the existing fields. Appointments
// that are not priced by energy (partner bookings) carry no price.
type portalAppointmentDTO struct {
	appointmentDTO
	Price *priceDTO `json:"price,omitempty"`
}

func toPortalDTO(uc UseCases, a domain.Appointment) portalAppointmentDTO {
	out := portalAppointmentDTO{appointmentDTO: toDTO(a)}
	if a.Energy.Status != domain.EnergyNotApplicable {
		p := toPriceDTO(uc.PriceOf(a.Energy))
		out.Price = &p
	}
	return out
}

// registerQuote exposes GET /doctors/{id}/quote?startAt=<RFC3339>: the price
// shown before confirming. A provider failure still answers 200, pending.
func registerQuote(mux *http.ServeMux, uc UseCases) {
	mux.HandleFunc("GET /doctors/{id}/quote", func(w http.ResponseWriter, r *http.Request) {
		start, err := time.Parse(time.RFC3339, r.URL.Query().Get("startAt"))
		if err != nil {
			sendError(w, 400, "INVALID_REQUEST", "startAt (RFC3339) es obligatorio.")
			return
		}
		q, err := uc.Quote(r.Context(), r.PathValue("id"), start)
		if err != nil {
			mapError(w, err)
			return
		}
		p := toPriceDTO(q.Price)
		sendJSON(w, 200, struct {
			DoctorID string `json:"doctorId"`
			StartAt  string `json:"startAt"`
			priceDTO
		}{q.DoctorID, q.StartAt.UTC().Format(time.RFC3339), p})
	})
}
