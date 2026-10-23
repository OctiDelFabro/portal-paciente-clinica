package apikey

import (
	"testing"
	"time"
)

func TestParseSkipsMalformedAndReservedEntries(t *testing.T) {
	keys := Parse(" team-a:k1 ,bad, :k2,team-b:, portal:k3,team-c:k:4", "portal")
	if len(keys) != 2 || keys["team-a"] != "k1" || keys["team-c"] != "k:4" {
		t.Fatalf("keys = %v", keys)
	}
}

func TestOwnerMatchesOnlyConfiguredKeys(t *testing.T) {
	keys := map[string]string{"team-a": "k1", "team-b": "k2"}
	if got := Owner(keys, "k2"); got != "team-b" {
		t.Fatalf("Owner(k2) = %q", got)
	}
	for _, bad := range []string{"", "k3", "k1 "} {
		if got := Owner(keys, bad); got != "" {
			t.Fatalf("Owner(%q) = %q, want empty", bad, got)
		}
	}
}

func TestLimiterIsPerOwnerAndResetsEachMinute(t *testing.T) {
	l := &Limiter{Rate: 2}
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a", now); !ok {
			t.Fatalf("request %d rejected", i)
		}
	}
	ok, wait := l.Allow("a", now.Add(10*time.Second))
	if ok || wait != 51 {
		t.Fatalf("third request ok=%v wait=%d, want rejected with 51 s", ok, wait)
	}
	if ok, _ := l.Allow("b", now); !ok {
		t.Fatal("other owner must have its own window")
	}
	if ok, _ := l.Allow("a", now.Add(time.Minute)); !ok {
		t.Fatal("window must reset after one minute")
	}
}

func TestLimiterWithZeroRateAllowsEverything(t *testing.T) {
	l := &Limiter{}
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("a", time.Now()); !ok {
			t.Fatal("zero rate must not limit")
		}
	}
}
