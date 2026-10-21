package service

import (
	"net/mail"
	"strings"
	"time"
)

// Template sigue el esquema Template de notifications-v1.openapi.json.
type Template struct {
	ID       string   `json:"id"`
	Required []string `json:"requiredParameters"`
	Optional []string `json:"optionalParameters"`
	Subject  string   `json:"subject"`
	Body     string   `json:"body"`
}

// Catalog es el mismo catalogo publicado por el mock (contrato v1).
var Catalog = []Template{
	{"operation-confirmed.v1", []string{"operationName", "reference"}, []string{"recipientName", "scheduledAt"}, "Confirmacion: {{operationName}}", "{{operationName}} confirmada. Referencia: {{reference}}.{{scheduledAt}}"},
	{"operation-cancelled.v1", []string{"operationName", "reference"}, []string{"recipientName", "reason"}, "Cancelacion: {{operationName}}", "{{operationName}} cancelada. Referencia: {{reference}}.{{reason}}"},
	{"operation-reminder.v1", []string{"operationName", "reference", "scheduledAt"}, []string{"recipientName"}, "Recordatorio: {{operationName}}", "Recordatorio de {{operationName}}. Referencia: {{reference}}. Fecha: {{scheduledAt}}."},
}

var paramLimits = map[string]int{"operationName": 80, "reference": 80, "recipientName": 100, "reason": 200, "scheduledAt": 35}

func findTemplate(id string) *Template {
	for i := range Catalog {
		if Catalog[i].ID == id {
			return &Catalog[i]
		}
	}
	return nil
}

func validString(value string, max int) bool {
	return len(value) > 0 && len([]rune(value)) <= max && strings.TrimSpace(value) != "" &&
		!strings.ContainsAny(value, "\r\n\x00")
}

// Validate aplica las reglas del contrato. Devuelve un motivo vacio si es valida.
func Validate(r Request) string {
	template := findTemplate(r.TemplateID)
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

// Render sustituye parametros como texto plano. Los opcionales ausentes se
// omiten; los presentes se agregan con su etiqueta para que el texto sea legible.
func Render(templateID string, params map[string]string) (subject, body string) {
	t := findTemplate(templateID)
	if t == nil {
		return "", ""
	}
	labels := map[string]string{"scheduledAt": " Fecha: ", "reason": " Motivo: "}
	subject, body = t.Subject, t.Body
	for k := range paramLimits {
		value := params[k]
		if label, optional := labels[k]; optional && value != "" && strings.HasSuffix(t.Body, ".{{"+k+"}}") {
			value = label + value + "."
		}
		subject = strings.ReplaceAll(subject, "{{"+k+"}}", params[k])
		body = strings.ReplaceAll(body, "{{"+k+"}}", value)
	}
	if name := params["recipientName"]; name != "" {
		body = "Hola " + name + ". " + body
	}
	return subject, body
}
