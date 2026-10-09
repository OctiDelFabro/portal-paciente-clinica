// Gateway inicial, no autenticacion del portal implementada. No expone API keys al frontend.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

func main() {
	target := os.Getenv("NOTIFICATIONS_URL")
	if target == "" {
		target = "http://127.0.0.1:8085"
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		slog.Error("NOTIFICATIONS_URL invalida")
		os.Exit(1)
	}
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.Transport = &http.Transport{ResponseHeaderTimeout: 2 * time.Second, IdleConnTimeout: 30 * time.Second}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "UPSTREAM_UNAVAILABLE", "message": "Mock no disponible."}})
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", proxy)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"service": "gateway", "status": "ok", "portalAuthImplemented": false})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(501)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "NOT_IMPLEMENTED"})
	})
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP", "error", err)
		os.Exit(1)
	}
}
