package domain

import (
	"sort"
	"time"
)

// Consultation energy (ADR-009, decision L6): each consultation uses the
// equipment of the office during the slot. The external provider turns the
// equipment list into kWh; Turnos prices it with its own tariff.

// EnergyStatus tells whether the consultation energy cost is known.
type EnergyStatus string

const (
	// EnergyEstimated: the provider answered; kWh and cost are final.
	EnergyEstimated EnergyStatus = "estimated"
	// EnergyPending: the provider failed or was not called yet; the background
	// re-estimator retries it. The appointment total is the base price meanwhile.
	EnergyPending EnergyStatus = "pending"
	// EnergyNotApplicable: partner bookings (ADR-008) bill a flat price.
	EnergyNotApplicable EnergyStatus = "not_applicable"
)

// Energy is the estimated consumption of one consultation and its cost.
// Amounts are integers: MicroKWh in millionths of a kWh, CostCents in cents.
type Energy struct {
	Status           EnergyStatus
	MicroKWh         int64
	CostCents        int64
	AlgorithmVersion string
}

// EquipmentMode is how an equipment's energy is described.
type EquipmentMode string

const (
	// ModePower: PowerW drawn for the hours of use, times DutyFactor.
	ModePower EquipmentMode = "power"
	// ModeCycles: a fixed energy per cycle (for example, one study).
	ModeCycles EquipmentMode = "cycles"
)

// Equipment is one line of a consultation profile.
type Equipment struct {
	ID       string
	Quantity int
	Mode     EquipmentMode
	// ModePower
	PowerW     float64
	DutyFactor float64 // fraction of the time the device draws PowerW (0, 1]
	// ModeCycles
	EnergyPerCycleKWh float64
	Cycles            int // cycles per consultation
}

// Consumption is what one consultation uses: Hours of use on a single day.
type Consumption struct {
	Hours     float64 // at most six decimals, as the provider requires
	Equipment []Equipment
}

// ILLUSTRATIVE VALUES. The profiles below are reasonable orders of magnitude
// for a small office, not measurements of the clinic's real equipment. They
// are data: changing a profile changes only the estimate, never booking rules.
//
// Base office, present in every consultation:
//   - 4 LED panels of 18 W, on the whole slot.
//   - 1 split air conditioner of 1100 W electrical input, compressor running
//     half of the time (duty factor 0.5).
//   - 1 desktop PC with monitor, 120 W, on the whole slot.
var baseOffice = []Equipment{
	{ID: "iluminacion-led", Quantity: 4, Mode: ModePower, PowerW: 18, DutyFactor: 1},
	{ID: "aire-acondicionado", Quantity: 1, Mode: ModePower, PowerW: 1100, DutyFactor: 0.5},
	{ID: "pc-consultorio", Quantity: 1, Mode: ModePower, PowerW: 120, DutyFactor: 1},
}

// specialtyEquipment adds the devices of each specialty (Medicos slugs):
//   - cardiologia: one 12-lead electrocardiogram per consultation, 0.01 kWh.
//   - pediatria: electronic baby scale (5 W, whole slot) and one cycle of a
//     small instrument sterilizer, 0.05 kWh.
//
// Specialties without an entry (for example clinica-medica) use the base office.
var specialtyEquipment = map[string][]Equipment{
	"cardiologia": {
		{ID: "electrocardiografo", Quantity: 1, Mode: ModeCycles, EnergyPerCycleKWh: 0.01, Cycles: 1},
	},
	"pediatria": {
		{ID: "balanza-pediatrica", Quantity: 1, Mode: ModePower, PowerW: 5, DutyFactor: 1},
		{ID: "esterilizador", Quantity: 1, Mode: ModeCycles, EnergyPerCycleKWh: 0.05, Cycles: 1},
	},
}

// Specialties lists the specialties with their own equipment, sorted.
func Specialties() []string {
	out := make([]string, 0, len(specialtyEquipment))
	for s := range specialtyEquipment {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ProfileFor returns the base office plus the specialty's equipment.
func ProfileFor(specialty string) []Equipment {
	out := append([]Equipment(nil), baseOffice...)
	return append(out, specialtyEquipment[specialty]...)
}

// ConsultationConsumption uses the doctor's slot duration as hours of use.
// Hours are rounded half-up to six decimals (20 min = 0.333333 h).
func ConsultationConsumption(d Doctor) Consumption {
	const msPerHour = int64(time.Hour / time.Millisecond)
	micro := (d.Duration.Milliseconds()*1_000_000 + msPerHour/2) / msPerHour
	return Consumption{Hours: float64(micro) / 1e6, Equipment: ProfileFor(d.Specialty)}
}
