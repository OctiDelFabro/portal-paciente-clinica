// Esqueleto HTTP de servicios: solo health y respuesta explicita de no implementado.
package scaffold

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func Run(name, defaultAddr string) {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{"service": name, "status": "ok", "domainImplemented": false})
			return
		}
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "NOT_IMPLEMENTED", "service": name})
	})
	slog.Info("Esqueleto, sin reglas de dominio", "service", name, "addr", addr)
	server := http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP", "error", err)
		os.Exit(1)
	}
}
