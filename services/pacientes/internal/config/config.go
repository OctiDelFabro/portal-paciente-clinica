// Package config reads the Pacientes settings from the environment and
// refuses invalid values so the service fails at startup, not at login.
package config

import (
	"errors"
	"fmt"
	"time"

	"portal-paciente-clinica/services/pacientes/internal/service"
)

type Config struct {
	DSN          string
	Secret       string
	TokenTTL     time.Duration
	Addr         string
	DemoPassword string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		DSN: getenv("PACIENTES_DB_DSN"), Secret: getenv("JWT_SECRET"), Addr: getenv("HTTP_ADDR"),
		DemoPassword: getenv("DEMO_PATIENT_PASSWORD"), TokenTTL: time.Hour,
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8082"
	}
	if cfg.DSN == "" {
		return cfg, errors.New("PACIENTES_DB_DSN is required")
	}
	if len(cfg.Secret) < service.MinSecretBytes {
		return cfg, service.ErrWeakSecret
	}
	if raw := getenv("JWT_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil || ttl <= 0 {
			return cfg, fmt.Errorf("JWT_TTL must be a positive duration such as 1h or 30m")
		}
		cfg.TokenTTL = ttl
	}
	if cfg.DemoPassword != "" && len(cfg.DemoPassword) < service.MinPasswordLen {
		return cfg, fmt.Errorf("DEMO_PATIENT_PASSWORD must have at least %d characters", service.MinPasswordLen)
	}
	return cfg, nil
}
