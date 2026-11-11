// Package api is the presentation layer of Pacientes: authentication,
// profile and the internal contact lookup.
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
	"math"
	"mime"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/services/pacientes/internal/repository"
	"portal-paciente-clinica/services/pacientes/internal/service"
)

// Both labels take fixed values (no emails or IDs) to keep cardinality bounded.
// The outcome split shows brute force (invalid/locked) apart from outages.
var authAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "pacientes_auth_total",
	Help: "Register and login attempts by operation and outcome.",
}, []string{"operation", "outcome"})

type HTTP struct {
	Service     *service.Service
	Health      func() error
	InternalKey string
}

func (h *HTTP) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := h.Health(); err != nil {
			sendError(w, 503, "SERVICE_UNAVAILABLE", "Storage unavailable.")
			return
		}
		sendJSON(w, 200, map[string]string{"service": "pacientes", "status": "ok"})
	})
	mux.HandleFunc("POST /auth/register", h.register)
	mux.HandleFunc("POST /auth/login", h.login)
	mux.HandleFunc("GET /me", h.me)
	mux.HandleFunc("GET /patients/{id}/contact", h.contact)
	mux.HandleFunc("GET /internal/patients/{id}", h.internalProfile)
	h.registerManagement(mux)
}

func (h *HTTP) register(w http.ResponseWriter, r *http.Request) {
	var in service.RegisterInput
	if !decode(w, r, &in) {
		authAttempts.WithLabelValues("register", "bad_request").Inc()
		return
	}
	id, err := h.Service.Register(r.Context(), in)
	var verr *service.ValidationError
	switch {
	case errors.As(err, &verr):
		authAttempts.WithLabelValues("register", "invalid").Inc()
		sendError(w, 422, "VALIDATION_ERROR", verr.Reason)
	case errors.Is(err, repository.ErrEmailTaken):
		authAttempts.WithLabelValues("register", "email_taken").Inc()
		sendError(w, 409, "EMAIL_TAKEN", "The email is already registered.")
	case errors.Is(err, repository.ErrDocumentTaken):
		sendError(w, 409, "DOCUMENT_TAKEN", "Ya existe una ficha con ese documento. Consultá a recepción para recuperar o corregir tus datos.")
	case err != nil:
		authAttempts.WithLabelValues("register", "unavailable").Inc()
		slog.ErrorContext(r.Context(), "cannot register patient", "event", "patient.register_failed", "error", err.Error())
		sendError(w, 503, "SERVICE_UNAVAILABLE", "Could not register the patient; retry later.")
	default:
		authAttempts.WithLabelValues("register", "created").Inc()
		slog.InfoContext(r.Context(), "patient registered", "event", "patient.registered", "patient_id", id)
		w.Header().Set("Location", "/me")
		sendJSON(w, 201, map[string]string{"patientId": id})
	}
}

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type contactDTO struct {
	PatientID   string  `json:"patientId"`
	FullName    string  `json:"fullName"`
	Email       *string `json:"email"`
	Responsible string  `json:"responsiblePatientId,omitempty"`
}

func emailPtr(email string) *string {
	if email == "" {
		return nil
	}
	return &email
}

func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	var in loginInput
	if !decode(w, r, &in) {
		authAttempts.WithLabelValues("login", "bad_request").Inc()
		return
	}
	session, err := h.Service.Login(r.Context(), in.Email, in.Password)
	var limited *service.RateLimitError
	switch {
	case errors.As(err, &limited):
		authAttempts.WithLabelValues("login", "locked").Inc()
		slog.WarnContext(r.Context(), "login locked after repeated failures", "event", "auth.login_locked")
		w.Header().Set("Retry-After", fmt.Sprint(int(math.Ceil(limited.RetryAfter.Seconds()))))
		sendError(w, 429, "RATE_LIMITED", "Too many failed attempts; retry later.")
	case errors.Is(err, service.ErrInvalidCredentials):
		authAttempts.WithLabelValues("login", "invalid").Inc()
		slog.InfoContext(r.Context(), "login rejected", "event", "auth.login_failed")
		sendError(w, 401, "INVALID_CREDENTIALS", "Invalid email or password.")
	case err != nil:
		authAttempts.WithLabelValues("login", "unavailable").Inc()
		slog.ErrorContext(r.Context(), "login failed", "event", "auth.login_error", "error", err.Error())
		sendError(w, 503, "SERVICE_UNAVAILABLE", "Could not sign in; retry later.")
	default:
		authAttempts.WithLabelValues("login", "success").Inc()
		slog.InfoContext(r.Context(), "login succeeded", "event", "auth.login_succeeded", "patient_id", session.Patient.ID)
		sendJSON(w, 200, map[string]any{
			"accessToken": session.AccessToken, "tokenType": "Bearer", "expiresIn": session.ExpiresIn,
			"patient": map[string]string{
				"patientId": session.Patient.ID, "fullName": session.Patient.FullName, "email": session.Patient.Email,
				"role": session.Patient.Role,
			},
		})
	}
}

// me trusts X-Patient-Id because the gateway validates the JWT, strips any
// client copy and sets it from the token subject.
func (h *HTTP) me(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Patient-Id")
	if id == "" {
		sendError(w, 401, "UNAUTHORIZED", "Authentication required.")
		return
	}
	p, ok := h.find(w, r, id)
	if !ok {
		return
	}
	var birth string
	if !p.BirthDate.IsZero() {
		birth = p.BirthDate.Format(time.DateOnly)
	}
	role := p.Role
	if role == "" {
		role = "patient"
	}
	sendJSON(w, 200, map[string]any{
		"patientId": p.ID, "fullName": p.FullName, "email": emailPtr(p.Email), "documentNumber": p.DocumentNumber,
		"birthDate": birth, "enabled": p.Enabled, "responsiblePatientId": p.ResponsibleID,
		"role": role,
	})
}

// contact serves other services on the internal network. A request that came
// through the gateway always carries X-Patient-Id and may only read itself,
// unless caller has role "doctor" (RF-14: doctor needs patient contact for agenda).
func (h *HTTP) contact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := r.Header.Get("X-User-Role")
	if caller := r.Header.Get("X-Patient-Id"); caller != "" && !h.Service.CanReadContact(r.Context(), caller, id, role) {
		sendError(w, 403, "FORBIDDEN", "Not allowed to read this patient.")
		return
	}
	p, ok := h.find(w, r, id)
	if !ok {
		return
	}
	sendJSON(w, 200, contactDTO{PatientID: p.ID, FullName: p.FullName, Email: emailPtr(p.Email), Responsible: p.ResponsibleID})
}

func (h *HTTP) find(w http.ResponseWriter, r *http.Request, id string) (repository.Patient, bool) {
	p, err := h.Service.Profile(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		sendError(w, 404, "NOT_FOUND", "Patient not found.")
		return p, false
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "cannot read patient", "event", "patient.read_failed", "error", err.Error())
		sendError(w, 503, "SERVICE_UNAVAILABLE", "Could not read the patient; retry later.")
		return p, false
	}
	return p, true
}

// decode accepts a single JSON object of at most 16 KiB without unknown fields.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		sendError(w, 415, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json.")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			sendError(w, 413, "PAYLOAD_TOO_LARGE", "The body exceeds 16 KiB.")
		} else {
			sendError(w, 400, "INVALID_JSON", "Could not read the body.")
		}
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(dst) != nil || dec.Decode(&extra) != io.EOF {
		sendError(w, 400, "INVALID_JSON", "Invalid JSON, unknown fields or wrong types.")
		return false
	}
	return true
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

func (h *HTTP) internalProfile(w http.ResponseWriter, r *http.Request) {
	expected, got := sha256.Sum256([]byte(h.InternalKey)), sha256.Sum256([]byte(r.Header.Get("X-Service-Key")))
	if len(h.InternalKey) < 32 || subtle.ConstantTimeCompare(expected[:], got[:]) != 1 {
		sendError(w, 401, "UNAUTHORIZED", "Service authentication required.")
		return
	}
	p, ok := h.find(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	birth := ""
	if !p.BirthDate.IsZero() {
		birth = p.BirthDate.Format(time.DateOnly)
	}
	sendJSON(w, 200, map[string]any{"patientId": p.ID, "fullName": p.FullName, "email": emailPtr(p.Email), "enabled": p.Enabled, "role": p.Role, "birthDate": birth, "responsiblePatientId": p.ResponsibleID})
}
