// Package apikey resuelve la autenticacion por X-API-Key y el limite de
// solicitudes por consumidor de las capacidades publicadas a otros grupos.
package apikey

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
	"sync"
	"time"
)

// Parse lee "consumidor:clave,consumidor:clave". Descarta entradas mal formadas
// y los nombres reservados para uso interno.
func Parse(raw string, reserved ...string) map[string]string {
	keys := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || contains(reserved, parts[0]) {
			continue
		}
		keys[parts[0]] = parts[1]
	}
	return keys
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Owner devuelve el consumidor de la clave, o "" si no coincide con ninguna.
// Compara resumenes en tiempo constante para no filtrar la clave por tiempos.
func Owner(keys map[string]string, key string) string {
	sum := sha256.Sum256([]byte(key))
	for owner, expected := range keys {
		target := sha256.Sum256([]byte(expected))
		if subtle.ConstantTimeCompare(sum[:], target[:]) == 1 {
			return owner
		}
	}
	return ""
}

type window struct {
	start time.Time
	count int
}

// Limiter aplica Rate solicitudes por minuto por consumidor (0 = sin limite).
// El contador vive en memoria de cada instancia: con varias replicas el limite
// es por replica.
type Limiter struct {
	Rate int

	mu      sync.Mutex
	windows map[string]window
}

// Allow registra una solicitud; si se excede el limite devuelve los segundos
// a esperar para Retry-After.
func (l *Limiter) Allow(owner string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = map[string]window{}
	}
	win := l.windows[owner]
	if win.start.IsZero() || !now.Before(win.start.Add(time.Minute)) {
		win = window{start: now}
	}
	if l.Rate > 0 && win.count >= l.Rate {
		return false, int(win.start.Add(time.Minute).Sub(now).Seconds()) + 1
	}
	win.count++
	l.windows[owner] = win
	return true, 0
}
