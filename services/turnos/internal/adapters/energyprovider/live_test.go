package energyprovider

// Live consumer check against the real provider deployment (ADR-009). It is
// opt-in: it runs only when PROVIDER_ENERGY_LIVE_URL is set, so the normal
// suite never needs network or credentials. Warm the provider first with
// GET <url>/openapi.json: a free-tier cold start exceeds the 5 s budget.
//
//	PROVIDER_ENERGY_LIVE_URL         base URL, without the path
//	PROVIDER_ENERGY_LIVE_API_KEY     key with the estimar-consumo permission
//	PROVIDER_ENERGY_LIVE_DENIED_KEY  optional valid key without that permission

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestLiveProviderEstimatesTheCardiologyExample(t *testing.T) {
	url := os.Getenv("PROVIDER_ENERGY_LIVE_URL")
	if url == "" {
		t.Skip("PROVIDER_ENERGY_LIVE_URL not set")
	}
	key := os.Getenv("PROVIDER_ENERGY_LIVE_API_KEY")
	if key == "" {
		t.Fatal("PROVIDER_ENERGY_LIVE_API_KEY is required with PROVIDER_ENERGY_LIVE_URL")
	}
	est, err := New(url, key).Estimate(context.Background(), consumption)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if est.MicroKWh != 381_000 || est.AlgorithmVersion != "consumo-v1" {
		t.Fatalf("got %+v, want 0.381 kWh with consumo-v1", est)
	}
}

func TestLiveProviderRejectsAKeyWithoutPermission(t *testing.T) {
	url, denied := os.Getenv("PROVIDER_ENERGY_LIVE_URL"), os.Getenv("PROVIDER_ENERGY_LIVE_DENIED_KEY")
	if url == "" || denied == "" {
		t.Skip("PROVIDER_ENERGY_LIVE_URL or PROVIDER_ENERGY_LIVE_DENIED_KEY not set")
	}
	_, err := New(url, denied).Estimate(context.Background(), consumption)
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindForbidden {
		t.Fatalf("got %v, want %s", err, KindForbidden)
	}
}
