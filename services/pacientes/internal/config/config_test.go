package config

import (
	"strings"
	"testing"
	"time"
)

func lookup(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func base() map[string]string {
	return map[string]string{"PACIENTES_DB_DSN": "user:pw@tcp(db:3306)/pacientes", "JWT_SECRET": strings.Repeat("s", 32)}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(lookup(base()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TokenTTL != time.Hour || cfg.Addr != "127.0.0.1:8082" || cfg.DemoPassword != "" {
		t.Fatalf("unexpected defaults %+v", cfg)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	cases := map[string]func(map[string]string){
		"missing dsn":         func(m map[string]string) { delete(m, "PACIENTES_DB_DSN") },
		"missing secret":      func(m map[string]string) { delete(m, "JWT_SECRET") },
		"short secret":        func(m map[string]string) { m["JWT_SECRET"] = strings.Repeat("s", 31) },
		"bad ttl":             func(m map[string]string) { m["JWT_TTL"] = "soon" },
		"negative ttl":        func(m map[string]string) { m["JWT_TTL"] = "-1h" },
		"short demo password": func(m map[string]string) { m["DEMO_PATIENT_PASSWORD"] = "short" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := base()
			mutate(m)
			if _, err := Load(lookup(m)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	m := base()
	m["JWT_TTL"] = "30m"
	m["HTTP_ADDR"] = "0.0.0.0:8082"
	m["DEMO_PATIENT_PASSWORD"] = "demo-pass-1"
	cfg, err := Load(lookup(m))
	if err != nil || cfg.TokenTTL != 30*time.Minute || cfg.Addr != "0.0.0.0:8082" || cfg.DemoPassword != "demo-pass-1" {
		t.Fatalf("got %+v %v", cfg, err)
	}
}
