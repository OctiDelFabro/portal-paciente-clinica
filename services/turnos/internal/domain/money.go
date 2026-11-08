package domain

import (
	"errors"
	"math/big"
	"strings"
)

// Prices in ARS are integer cents and energy is integer micro-kWh, so the
// shown total never drifts like a float would (25000 + 68.58 = 25068.58).

// Tariff is the portal price of a consultation: a base price plus the
// consultation energy at the clinic's own tariff (not the provider's).
type Tariff struct {
	BaseCents   int64
	CentsPerKWh int64
}

// Price is the breakdown shown to the patient.
type Price struct {
	BaseCents  int64
	Energy     Energy
	TotalCents int64
}

// Estimated prices microKWh: cost = kWh x tariff, rounded half-up to cents.
func (t Tariff) Estimated(microKWh int64, algorithmVersion string) Energy {
	cost := new(big.Int).Mul(big.NewInt(microKWh), big.NewInt(t.CentsPerKWh))
	cost.Add(cost, big.NewInt(500_000))
	cost.Quo(cost, big.NewInt(1_000_000))
	return Energy{Status: EnergyEstimated, MicroKWh: microKWh, CostCents: cost.Int64(), AlgorithmVersion: algorithmVersion}
}

// Price adds the energy cost only when it is estimated: a pending cost is
// not charged, so the total is the base price.
func (t Tariff) Price(e Energy) Price {
	total := t.BaseCents
	if e.Status == EnergyEstimated {
		total += e.CostCents
	}
	return Price{BaseCents: t.BaseCents, Energy: e, TotalCents: total}
}

var errDecimal = errors.New("want a non-negative decimal number")

// ParseScaled parses a non-negative decimal (JSON number syntax, exponent
// allowed) into an integer with scale decimals, rounding half-up. exact is
// false when digits beyond scale were dropped.
func ParseScaled(s string, scale int) (v int64, exact bool, err error) {
	r, ok := new(big.Rat).SetString(s)
	if s == "" || !ok || r.Sign() < 0 {
		return 0, false, errDecimal
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	exact = rem.Sign() == 0
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, false, errDecimal
	}
	return q.Int64(), exact, nil
}

// FormatScaled writes v with exactly scale decimals (2506858, 2 -> "25068.58").
func FormatScaled(v int64, scale int) string {
	neg := v < 0
	if neg {
		v = -v
	}
	digits := big.NewInt(v).String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	out := digits[:len(digits)-scale]
	if scale > 0 {
		out += "." + digits[len(digits)-scale:]
	}
	if neg {
		out = "-" + out
	}
	return out
}

// TrimDecimal drops trailing zeros of a decimal ("0.381000" -> "0.381").
func TrimDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	return strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
}
