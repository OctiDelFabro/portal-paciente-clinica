package main

import (
	"net/http"
	"strings"
)

// defaultCORSOrigins is the Vite dev server of the frontend.
var defaultCORSOrigins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}

func parseOrigins(raw string) []string {
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		return defaultCORSOrigins
	}
	return out
}

// withCORS lets the listed browser origins call the internal listener. It
// answers preflights itself, so they never need a token. Tokens travel in
// Authorization, not cookies, so credentials are not allowed.
func withCORS(origins []string, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range origins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !allowed[origin] {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Expose-Headers", "X-Correlation-ID, Retry-After, X-Instance, Location, Idempotency-Replayed")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Correlation-ID")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
