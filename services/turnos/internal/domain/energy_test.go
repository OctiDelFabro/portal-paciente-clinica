package domain

import (
	"testing"
	"time"
)

func TestConsultationConsumptionUsesSlotDurationAndSpecialtyProfile(t *testing.T) {
	cardio := ConsultationConsumption(Doctor{ID: "d", Duration: 30 * time.Minute, Specialty: "cardiologia"})
	if cardio.Hours != 0.5 {
		t.Fatalf("hours: want 0.5, got %v", cardio.Hours)
	}
	ids := map[string]bool{}
	for _, e := range cardio.Equipment {
		ids[e.ID] = true
	}
	for _, want := range []string{"iluminacion-led", "aire-acondicionado", "pc-consultorio", "electrocardiografo"} {
		if !ids[want] {
			t.Errorf("cardiology profile lacks %s: %+v", want, cardio.Equipment)
		}
	}
	general := ConsultationConsumption(Doctor{ID: "d", Duration: 30 * time.Minute, Specialty: "unknown"})
	if len(general.Equipment) != len(baseOffice) {
		t.Fatalf("unknown specialty must use the base office only, got %+v", general.Equipment)
	}
}

// The provider accepts at most six decimals: a 20-minute slot is 0.333333 h.
func TestConsultationHoursAreRoundedToSixDecimals(t *testing.T) {
	c := ConsultationConsumption(Doctor{Duration: 20 * time.Minute, Specialty: "pediatria"})
	if c.Hours != 0.333333 {
		t.Fatalf("want 0.333333, got %v", c.Hours)
	}
}

func TestEveryProfileIsValidEquipment(t *testing.T) {
	for _, s := range append(Specialties(), "") {
		for _, e := range ProfileFor(s) {
			ok := e.Quantity >= 1 && e.ID != ""
			switch e.Mode {
			case ModePower:
				ok = ok && e.PowerW > 0 && e.DutyFactor > 0 && e.DutyFactor <= 1
			case ModeCycles:
				ok = ok && e.EnergyPerCycleKWh > 0 && e.Cycles >= 0
			default:
				ok = false
			}
			if !ok {
				t.Errorf("specialty %q: invalid equipment %+v", s, e)
			}
		}
	}
}

func TestParseScaledIsExactAndRoundsHalfUp(t *testing.T) {
	cases := []struct {
		in    string
		scale int
		want  int64
		exact bool
	}{
		{"47.4", 6, 47_400_000, true},
		{"0.381", 6, 381_000, true},
		{"1e-6", 6, 1, true},
		{"0.0000005", 6, 1, false},
		{"0.0000004", 6, 0, false},
		{"180", 2, 18000, true},
		{"25000.5", 2, 2500050, true},
	}
	for _, c := range cases {
		got, exact, err := ParseScaled(c.in, c.scale)
		if err != nil || got != c.want || exact != c.exact {
			t.Errorf("%s: got %d exact=%v err=%v, want %d exact=%v", c.in, got, exact, err, c.want, c.exact)
		}
	}
	for _, bad := range []string{"", "-1", "abc", "1e40"} {
		if _, _, err := ParseScaled(bad, 6); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestFormatScaled(t *testing.T) {
	if got := FormatScaled(2506858, 2); got != "25068.58" {
		t.Errorf("cents: %s", got)
	}
	if got := FormatScaled(2500000, 2); got != "25000.00" {
		t.Errorf("cents: %s", got)
	}
	if got := TrimDecimal(FormatScaled(381000, 6)); got != "0.381" {
		t.Errorf("kWh: %s", got)
	}
	if got := TrimDecimal(FormatScaled(47_000_000, 6)); got != "47" {
		t.Errorf("kWh: %s", got)
	}
}

// kWh x tariff is computed on integers: 0.381 kWh x ARS 180 = ARS 68.58 exactly.
func TestTariffPricesEstimatedEnergyWithoutFloatDrift(t *testing.T) {
	tariff := Tariff{BaseCents: 2_500_000, CentsPerKWh: 18_000}
	e := tariff.Estimated(381_000, "consumo-v1")
	if e.Status != EnergyEstimated || e.CostCents != 6858 || e.AlgorithmVersion != "consumo-v1" {
		t.Fatalf("got %+v", e)
	}
	p := tariff.Price(e)
	if p.BaseCents != 2_500_000 || p.TotalCents != 2_506_858 {
		t.Fatalf("got %+v", p)
	}
	// Half-up rounding of the cost: 0.000003 kWh x 18000 c = 0.054 c -> 0; 0.000028 -> 0.504 c -> 1.
	if got := tariff.Estimated(3, "v").CostCents; got != 0 {
		t.Errorf("tiny cost: %d", got)
	}
	if got := tariff.Estimated(28, "v").CostCents; got != 1 {
		t.Errorf("half-up cost: %d", got)
	}
}

func TestPendingEnergyLeavesTotalAtBasePrice(t *testing.T) {
	p := Tariff{BaseCents: 2_500_000, CentsPerKWh: 18_000}.Price(Energy{Status: EnergyPending})
	if p.TotalCents != 2_500_000 || p.Energy.Status != EnergyPending {
		t.Fatalf("got %+v", p)
	}
}
