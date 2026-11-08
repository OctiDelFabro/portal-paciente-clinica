// provider-stub: local test double of the energy provider's v1 contract
// (ArquitecturasoftwareRenewableEnergy, POST /api/v1/estimaciones-consumo).
// It is not our capability; see services/provider-stub/README.md.
//
// Environment:
//
//	STUB_ADDR       listen address (default :8090)
//	STUB_API_KEY    required X-API-Key value (required)
//	STUB_MODE       ok | slow | 503 | down-after-n (default ok)
//	STUB_SLOW_MS    delay of slow mode (default 6000, above Turnos' budget)
//	STUB_DOWN_AFTER successful answers before down-after-n returns 503 (default 3)
package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "provider-stub"))
	key := os.Getenv("STUB_API_KEY")
	slowMS, err1 := strconv.Atoi(env("STUB_SLOW_MS", "6000"))
	downAfter, err2 := strconv.Atoi(env("STUB_DOWN_AFTER", "3"))
	mode := env("STUB_MODE", "ok")
	validMode := map[string]bool{"ok": true, "slow": true, "503": true, "down-after-n": true}[mode]
	if key == "" || err1 != nil || err2 != nil || slowMS < 0 || downAfter < 0 || !validMode {
		slog.Error("invalid configuration: STUB_API_KEY is required; STUB_MODE ok|slow|503|down-after-n; STUB_SLOW_MS and STUB_DOWN_AFTER non-negative integers")
		os.Exit(1)
	}
	stub := &Stub{APIKey: key, Mode: mode, Delay: time.Duration(slowMS) * time.Millisecond, DownAfter: downAfter}
	addr := env("STUB_ADDR", ":8090")
	slog.Info("provider stub listening", "event", "service.started", "addr", addr, "mode", mode)
	server := &http.Server{Addr: addr, Handler: stub.Handler(), ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 30 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("http server failed", "error", err.Error())
		os.Exit(1)
	}
}
