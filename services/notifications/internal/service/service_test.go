package service

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFingerprintIgnoresParameterOrder(t *testing.T) {
	a := Request{TemplateID: "operation-confirmed.v1", Recipient: Recipient{"a@example.test"}, Parameters: map[string]string{"operationName": "x", "reference": "1"}}
	b := Request{TemplateID: "operation-confirmed.v1", Recipient: Recipient{"a@example.test"}, Parameters: map[string]string{"reference": "1", "operationName": "x"}}
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("equivalent requests must share fingerprint")
	}
	b.Parameters["reference"] = "2"
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("different data must change fingerprint")
	}
}

func TestValidateRejectsFreeTextAndBadTemplates(t *testing.T) {
	base := func() Request {
		return Request{TemplateID: "operation-reminder.v1", Recipient: Recipient{"a@example.test"},
			Parameters: map[string]string{"operationName": "Turno", "reference": "T-1", "scheduledAt": "2026-10-25T10:00:00-03:00"}}
	}
	if reason := Validate(base()); reason != "" {
		t.Fatalf("valid request rejected: %s", reason)
	}
	cases := map[string]func(*Request){
		"unknown template": func(r *Request) { r.TemplateID = "free-text.v1" },
		"free text param":  func(r *Request) { r.Parameters["body"] = "hola" },
		"display name":     func(r *Request) { r.Recipient.Email = "Ana <a@example.test>" },
		"missing required": func(r *Request) { delete(r.Parameters, "scheduledAt") },
		"header injection": func(r *Request) { r.Parameters["reference"] = "x\r\nBcc: z@example.test" },
		"bad date":         func(r *Request) { r.Parameters["scheduledAt"] = "mañana" },
	}
	for name, mutate := range cases {
		r := base()
		mutate(&r)
		if Validate(r) == "" {
			t.Errorf("%s: should be rejected", name)
		}
	}
}

func TestRenderOmitsMissingOptionalParameters(t *testing.T) {
	subject, body := Render("operation-confirmed.v1", map[string]string{"operationName": "Turno medico", "reference": "T-1"})
	if subject != "Confirmacion: Turno medico" || strings.Contains(body, "{{") {
		t.Fatalf("bad render: %q %q", subject, body)
	}
	_, body = Render("operation-confirmed.v1", map[string]string{"operationName": "Turno", "reference": "T-1", "scheduledAt": "2026-10-25T10:00:00Z"})
	if !strings.Contains(body, "Fecha: 2026-10-25T10:00:00Z.") {
		t.Fatalf("scheduledAt not rendered: %q", body)
	}
}

func TestNextAttemptFollowsBackoffThenGivesUp(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	want := []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}
	for i, d := range want {
		next, ok := NextAttempt(i, now)
		if !ok || next.Sub(now) != d {
			t.Fatalf("attempt %d: got %v %v", i, next.Sub(now), ok)
		}
	}
	if _, ok := NextAttempt(3, now); ok {
		t.Fatal("fourth failure must be terminal")
	}
}

func TestBreakerOpensAndHalfOpens(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	b := &Breaker{Threshold: 3, Cooldown: 30 * time.Second}
	for i := 0; i < 3; i++ {
		b.Record(false, now)
	}
	if b.Allow(now.Add(10 * time.Second)) {
		t.Fatal("breaker must be open during cooldown")
	}
	if !b.Allow(now.Add(30 * time.Second)) {
		t.Fatal("breaker must allow a probe after cooldown")
	}
	b.Record(true, now.Add(31*time.Second))
	if !b.Allow(now.Add(32 * time.Second)) {
		t.Fatal("success must close the breaker")
	}
}

func TestParseEventClassifiesPoisonMessages(t *testing.T) {
	valid := `{"eventId":"0b8f7c3a-1111-4222-8333-444455556666","eventType":"turnos.reservado.v1","version":1,"occurredAt":"2026-10-20T12:00:00Z","correlationId":"c-1","data":{"turnId":"t-1","patientId":"pat-001","doctorId":"doc-001","startAt":"2026-10-25T10:00:00Z","endAt":"2026-10-25T10:30:00Z","appointmentVersion":1}}`
	if _, err := ParseEvent([]byte(valid)); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	poison := []string{
		`not json`,
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, "turnos.reservado.v1", "turnos.desconocido.v1", 1),
		strings.Replace(valid, "turnos.reservado.v1", "turnos.cancelado.v1", 1), // sin reasonCode
	}
	for _, body := range poison {
		if _, err := ParseEvent([]byte(body)); !errors.Is(err, ErrUnprocessable) {
			t.Errorf("want ErrUnprocessable for %.40s, got %v", body, err)
		}
	}
}
