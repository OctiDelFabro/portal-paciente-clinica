// Mock de contrato: memoria y resultados simulados; no envia correos reales.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Request struct {
	TemplateID string            `json:"templateId"`
	Recipient  Recipient         `json:"recipient"`
	Parameters map[string]string `json:"parameters"`
}
type Recipient struct {
	Email string `json:"email"`
}
type Notification struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	TemplateID string `json:"templateId"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Simulated  bool   `json:"simulated"`
}
type Template struct {
	ID       string   `json:"id"`
	Required []string `json:"requiredParameters"`
	Optional []string `json:"optionalParameters"`
	Subject  string   `json:"subject"`
	Body     string   `json:"body"`
}

var catalog = []Template{
	{"operation-confirmed.v1", []string{"operationName", "reference"}, []string{"recipientName", "scheduledAt"}, "Confirmacion: {{operationName}}", "{{operationName}} confirmada. Referencia: {{reference}}.{{scheduledAt}}"},
	{"operation-cancelled.v1", []string{"operationName", "reference"}, []string{"recipientName", "reason"}, "Cancelacion: {{operationName}}", "{{operationName}} cancelada. Referencia: {{reference}}.{{reason}}"},
	{"operation-reminder.v1", []string{"operationName", "reference", "scheduledAt"}, []string{"recipientName"}, "Recordatorio: {{operationName}}", "Recordatorio de {{operationName}}. Referencia: {{reference}}. Fecha: {{scheduledAt}}."},
}
var safeKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
var paramLimits = map[string]int{"operationName": 80, "reference": 80, "recipientName": 100, "reason": 200, "scheduledAt": 35}

type entry struct {
	owner       string
	fingerprint [32]byte
	accepted    Notification
	current     Notification
	expires     time.Time
	due         time.Time
}
type window struct {
	start time.Time
	count int
}
type Config struct {
	Keys   map[string]string // consumer -> API key, solo configuracion local
	Delay  time.Duration
	Result string
	Rate   int
	TTL    time.Duration
	Now    func() time.Time
}
type App struct {
	mu            sync.Mutex
	cfg           Config
	notifications map[string]*entry
	dedupe        map[string]*entry
	windows       map[string]window
}

func NewApp(cfg Config) *App {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	return &App{cfg: cfg, notifications: map[string]*entry{}, dedupe: map[string]*entry{}, windows: map[string]window{}}
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
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
func validString(value string, max int) bool {
	return len(value) > 0 && len([]rune(value)) <= max && strings.TrimSpace(value) != "" &&
		!strings.ContainsAny(value, "\r\n\x00")
}
func validateRequest(r Request) string {
	var template *Template
	for i := range catalog {
		if catalog[i].ID == r.TemplateID {
			template = &catalog[i]
			break
		}
	}
	if template == nil {
		return "La plantilla no existe."
	}
	addr, err := mail.ParseAddress(r.Recipient.Email)
	if err != nil || addr.Address != r.Recipient.Email || len(r.Recipient.Email) > 254 ||
		strings.ContainsAny(r.Recipient.Email, "\r\n") {
		return "El destinatario requiere un correo valido sin nombre de presentacion."
	}
	if r.Parameters == nil {
		return "parameters es obligatorio."
	}
	allowed := map[string]bool{}
	for _, k := range template.Required {
		allowed[k] = true
		if !validString(r.Parameters[k], paramLimits[k]) {
			return "Falta un parametro obligatorio o su valor es invalido: " + k
		}
	}
	for _, k := range template.Optional {
		allowed[k] = true
	}
	for k, v := range r.Parameters {
		if !allowed[k] {
			return "Parametro no permitido: " + k
		}
		if !validString(v, paramLimits[k]) {
			return "Parametro invalido: " + k
		}
		if k == "scheduledAt" {
			if _, e := time.Parse(time.RFC3339, v); e != nil {
				return "scheduledAt requiere RFC3339 con zona horaria."
			}
		}
	}
	return ""
}
func (a *App) authenticate(key string) string {
	sum := sha256.Sum256([]byte(key))
	for owner, expected := range a.cfg.Keys {
		target := sha256.Sum256([]byte(expected))
		if subtle.ConstantTimeCompare(sum[:], target[:]) == 1 {
			return owner
		}
	}
	return ""
}
func (a *App) allow(owner string, now time.Time) (bool, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	win := a.windows[owner]
	if win.start.IsZero() || !now.Before(win.start.Add(time.Minute)) {
		win = window{start: now}
	}
	if a.cfg.Rate > 0 && win.count >= a.cfg.Rate {
		return false, int(win.start.Add(time.Minute).Sub(now).Seconds()) + 1
	}
	win.count++
	a.windows[owner] = win
	return true, 0
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-ID")
	if !safeKey.MatchString(requestID) {
		requestID = newID()
	}
	w.Header().Set("X-Request-ID", requestID)
	if r.URL.Path == "/healthz" {
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			sendError(w, 405, "METHOD_NOT_ALLOWED", "Metodo no permitido.")
			return
		}
		sendJSON(w, 200, map[string]any{"status": "ok", "mode": "mock", "simulated": true})
		return
	}
	owner := a.authenticate(r.Header.Get("X-API-Key"))
	if owner == "" {
		sendError(w, 401, "UNAUTHORIZED", "Clave de API ausente o invalida.")
		return
	}
	now := a.cfg.Now().UTC()
	if ok, wait := a.allow(owner, now); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(wait))
		sendError(w, 429, "RATE_LIMITED", "Limite de solicitudes excedido.")
		return
	}
	switch {
	case r.URL.Path == "/v1/templates":
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			sendError(w, 405, "METHOD_NOT_ALLOWED", "Metodo no permitido.")
			return
		}
		sendJSON(w, 200, map[string]any{"templates": catalog})
	case r.URL.Path == "/v1/notifications":
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			sendError(w, 405, "METHOD_NOT_ALLOWED", "Metodo no permitido.")
			return
		}
		a.create(w, r, owner, now)
	case strings.HasPrefix(r.URL.Path, "/v1/notifications/"):
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			sendError(w, 405, "METHOD_NOT_ALLOWED", "Metodo no permitido.")
			return
		}
		a.get(w, strings.TrimPrefix(r.URL.Path, "/v1/notifications/"), owner, now)
	default:
		sendError(w, 404, "NOT_FOUND", "Recurso no encontrado.")
	}
}
func (a *App) create(w http.ResponseWriter, r *http.Request, owner string, now time.Time) {
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
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			sendError(w, 413, "PAYLOAD_TOO_LARGE", "El cuerpo supera 16 KiB.")
		} else {
			sendError(w, 400, "INVALID_JSON", "No se pudo leer el cuerpo.")
		}
		return
	}
	var request Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&request); err != nil {
		sendError(w, 400, "INVALID_JSON", "JSON invalido, campos desconocidos o tipos incorrectos.")
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		sendError(w, 400, "INVALID_JSON", "Debe enviarse un unico objeto JSON.")
		return
	}
	if reason := validateRequest(request); reason != "" {
		sendError(w, 422, "VALIDATION_ERROR", reason)
		return
	}
	canonical, _ := json.Marshal(request)
	fingerprint := sha256.Sum256(canonical)
	scopedKey := owner + "\x00" + key
	a.mu.Lock()
	// No se retienen direcciones ni parametros; solo su hash para detectar cambios.
	if prev := a.dedupe[scopedKey]; prev != nil && now.Before(prev.expires) {
		if prev.fingerprint != fingerprint {
			a.mu.Unlock()
			sendError(w, 409, "IDEMPOTENCY_CONFLICT", "La clave ya identifica una solicitud con otros datos.")
			return
		}
		value := prev.accepted
		a.mu.Unlock()
		w.Header().Set("Idempotency-Replayed", "true")
		w.Header().Set("Location", "/v1/notifications/"+value.ID)
		sendJSON(w, 202, value)
		return
	}
	value := Notification{ID: newID(), Status: "accepted", TemplateID: request.TemplateID, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Simulated: true}
	e := &entry{owner: owner, fingerprint: fingerprint, accepted: value, current: value, expires: now.Add(a.cfg.TTL), due: now.Add(a.cfg.Delay)}
	a.notifications[value.ID] = e
	a.dedupe[scopedKey] = e
	a.mu.Unlock()
	w.Header().Set("Idempotency-Replayed", "false")
	w.Header().Set("Location", "/v1/notifications/"+value.ID)
	sendJSON(w, 202, value)
}
func (a *App) get(w http.ResponseWriter, id, owner string, now time.Time) {
	a.mu.Lock()
	e := a.notifications[id]
	if e == nil || e.owner != owner {
		a.mu.Unlock()
		sendError(w, 404, "NOT_FOUND", "Recurso no encontrado.")
		return
	}
	// El reloj simula el resultado al consultar. No hay un trabajador real.
	if e.current.Status == "accepted" && !now.Before(e.due) {
		e.current.Status = a.cfg.Result
		e.current.UpdatedAt = e.due.Format(time.RFC3339Nano)
	}
	value := e.current
	a.mu.Unlock()
	sendJSON(w, 200, value)
}
func main() {
	keys := map[string]string{}
	for _, item := range strings.Split(os.Getenv("MOCK_API_KEYS"), ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			keys[parts[0]] = parts[1]
		}
	}
	if len(keys) == 0 {
		slog.Error("Defina MOCK_API_KEYS=consumidor:clave")
		os.Exit(1)
	}
	delay := time.Second
	if x := os.Getenv("MOCK_DELAY"); x != "" {
		d, e := time.ParseDuration(x)
		if e != nil || d < 0 {
			slog.Error("MOCK_DELAY invalido")
			os.Exit(1)
		}
		delay = d
	}
	result := os.Getenv("MOCK_RESULT")
	if result == "" {
		result = "sent"
	}
	if result != "sent" && result != "failed" {
		slog.Error("MOCK_RESULT debe ser sent o failed")
		os.Exit(1)
	}
	addr := os.Getenv("MOCK_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8085"
	}
	app := NewApp(Config{Keys: keys, Delay: delay, Result: result, Rate: 60})
	server := &http.Server{Addr: addr, Handler: app, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	slog.Info("Mock de Notificaciones", "addr", addr, "simulated", true, "version", "1.0.0")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP", "error", err)
		os.Exit(1)
	}
}
