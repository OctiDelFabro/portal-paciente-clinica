// Package api es la capa de presentacion de Notificaciones: HTTP publico
// (contrato notifications-v1) y consumidor de eventos internos.
package api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/notifications/internal/repository"
	"portal-paciente-clinica/services/notifications/internal/service"
)

// consumer toma solo los nombres configurados; outcome tiene valores fijos.
var requests = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "notifications_requests_total",
	Help: "Solicitudes a la API publica, por consumidor, plantilla y resultado.",
}, []string{"consumer", "template", "outcome"})

var safeKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type notificationDTO struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	TemplateID string `json:"templateId"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Simulated  bool   `json:"simulated"`
}

func toDTO(n repository.Notification) notificationDTO {
	return notificationDTO{n.ID, n.Status, n.TemplateID, n.CreatedAt.Format(time.RFC3339Nano), n.UpdatedAt.Format(time.RFC3339Nano), false}
}

type window struct {
	start time.Time
	count int
}

type HTTP struct {
	Service *service.Service
	Keys    map[string]string // consumidor -> clave, desde configuracion
	Rate    int
	Health  func() error

	mu      sync.Mutex
	windows map[string]window
}

func (h *HTTP) Register(mux *http.ServeMux) {
	h.windows = map[string]window{}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := h.Health(); err != nil {
			sendError(w, 503, "SERVICE_UNAVAILABLE", "Almacenamiento no disponible.")
			return
		}
		sendJSON(w, 200, map[string]any{"status": "ok", "mode": "service", "simulated": false})
	})
	mux.HandleFunc("GET /v1/templates", h.auth(func(w http.ResponseWriter, r *http.Request, consumer string) {
		sendJSON(w, 200, map[string]any{"templates": service.Catalog})
	}))
	mux.HandleFunc("POST /v1/notifications", h.auth(h.create))
	mux.HandleFunc("GET /v1/notifications/{id}", h.auth(func(w http.ResponseWriter, r *http.Request, consumer string) {
		n, err := h.Service.Get(r.Context(), r.PathValue("id"), consumer)
		if errors.Is(err, repository.ErrNotFound) {
			sendError(w, 404, "NOT_FOUND", "Recurso no encontrado.")
			return
		}
		if err != nil {
			sendError(w, 503, "SERVICE_UNAVAILABLE", "No se pudo consultar el estado; reintente.")
			return
		}
		sendJSON(w, 200, toDTO(n))
	}))
	for _, path := range []string{"/v1/templates", "/v1/notifications", "/v1/notifications/{id}"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			sendError(w, 405, "METHOD_NOT_ALLOWED", "Metodo no permitido.")
		})
	}
}

func (h *HTTP) authenticate(key string) string {
	sum := sha256.Sum256([]byte(key))
	for owner, expected := range h.Keys {
		target := sha256.Sum256([]byte(expected))
		if subtle.ConstantTimeCompare(sum[:], target[:]) == 1 {
			return owner
		}
	}
	return ""
}

// allow aplica 60 solicitudes por minuto por consumidor. El contador vive en
// memoria de cada instancia: con varias replicas el limite es por replica.
func (h *HTTP) allow(owner string, now time.Time) (bool, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	win := h.windows[owner]
	if win.start.IsZero() || !now.Before(win.start.Add(time.Minute)) {
		win = window{start: now}
	}
	if h.Rate > 0 && win.count >= h.Rate {
		return false, int(win.start.Add(time.Minute).Sub(now).Seconds()) + 1
	}
	win.count++
	h.windows[owner] = win
	return true, 0
}

func (h *HTTP) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if !safeKey.MatchString(requestID) {
			requestID = obs.NewID()
		}
		w.Header().Set("X-Request-ID", requestID)
		consumer := h.authenticate(r.Header.Get("X-API-Key"))
		if consumer == "" {
			requests.WithLabelValues("unknown", "", "unauthorized").Inc()
			sendError(w, 401, "UNAUTHORIZED", "Clave de API ausente o invalida.")
			return
		}
		if ok, wait := h.allow(consumer, time.Now()); !ok {
			requests.WithLabelValues(consumer, "", "rate_limited").Inc()
			w.Header().Set("Retry-After", fmt.Sprint(wait))
			sendError(w, 429, "RATE_LIMITED", "Limite de solicitudes excedido.")
			return
		}
		next(w, r, consumer)
	}
}

func (h *HTTP) create(w http.ResponseWriter, r *http.Request, consumer string) {
	key := r.Header.Get("Idempotency-Key")
	if !safeKey.MatchString(key) {
		sendError(w, 400, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key requiere de 1 a 128 caracteres alfanumericos o ._:-.")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		sendError(w, 415, "UNSUPPORTED_MEDIA_TYPE", "Content-Type debe ser application/json.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			sendError(w, 413, "PAYLOAD_TOO_LARGE", "El cuerpo supera 16 KiB.")
		} else {
			sendError(w, 400, "INVALID_JSON", "No se pudo leer el cuerpo.")
		}
		return
	}
	var req service.Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&req); err != nil {
		sendError(w, 400, "INVALID_JSON", "JSON invalido, campos desconocidos o tipos incorrectos.")
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		sendError(w, 400, "INVALID_JSON", "Debe enviarse un unico objeto JSON.")
		return
	}
	if reason := service.Validate(req); reason != "" {
		requests.WithLabelValues(consumer, "", "invalid").Inc()
		sendError(w, 422, "VALIDATION_ERROR", reason)
		return
	}
	n, replayed, err := h.Service.Accept(r.Context(), consumer, key, req)
	switch {
	case errors.Is(err, repository.ErrIdempotencyConflict):
		requests.WithLabelValues(consumer, req.TemplateID, "conflict").Inc()
		sendError(w, 409, "IDEMPOTENCY_CONFLICT", "La clave ya identifica una solicitud con otros datos.")
		return
	case err != nil:
		// Sin registro durable no se acepta (contrato: 503, reintentar con la misma clave).
		requests.WithLabelValues(consumer, req.TemplateID, "unavailable").Inc()
		slog.ErrorContext(r.Context(), "cannot persist notification", "event", "notification.persist_failed", "error", err.Error())
		sendError(w, 503, "SERVICE_UNAVAILABLE", "No se pudo registrar la solicitud; reintente con la misma clave.")
		return
	}
	outcome := "accepted"
	if replayed {
		outcome = "replayed"
	}
	requests.WithLabelValues(consumer, req.TemplateID, outcome).Inc()
	w.Header().Set("Idempotency-Replayed", fmt.Sprint(replayed))
	w.Header().Set("Location", "/v1/notifications/"+n.ID)
	sendJSON(w, 202, toDTO(n))
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sendError(w http.ResponseWriter, status int, code, message string) {
	sendJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
