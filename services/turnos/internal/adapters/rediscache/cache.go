// Package rediscache is the Redis cache-aside adapter for availability reads
// in Turnos (ADR-007). Redis is derived data: it never confirms a booking,
// and any Redis failure degrades to the MySQL read path.
package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

const (
	// TTL is the safety net for a lost invalidation (ADR-007).
	TTL = 30 * time.Second
	// Timeout bounds every Redis call so a slow Redis costs latency, not function.
	Timeout = 100 * time.Millisecond
)

// requests: result is hit, miss or error (fixed cardinality, ADR-007/ADR-011).
var requests = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cache_requests_total",
	Help: "Lecturas de disponibilidad en Redis, por resultado (hit, miss, error).",
}, []string{"result"})

// Key is availability:v1:{doctorId}:{yyyy-mm-dd}; v1 versions the value format.
func Key(doctorID, day string) string { return "availability:v1:" + doctorID + ":" + day }

// Cache decorates the MySQL availability query (Next) and implements
// app.AvailabilityCache so the service can invalidate after commit.
type Cache struct {
	client *redis.Client
	Next   app.AvailabilityQuery
}

// New connects lazily to addr; a Redis that is down at start-up only
// produces cache errors and MySQL reads.
func New(addr string, next app.AvailabilityQuery) *Cache {
	return &Cache{Next: next, client: redis.NewClient(&redis.Options{
		Addr: addr, DialTimeout: Timeout, ReadTimeout: Timeout, WriteTimeout: Timeout, PoolTimeout: Timeout,
		ContextTimeoutEnabled: true, MaxRetries: -1, // a retry would exceed the 100 ms budget
	})}
}

func (c *Cache) Close() error { return c.client.Close() }

// cached is the stored value; it holds no patient data.
type cached struct {
	DoctorID        string        `json:"doctorId"`
	Date            string        `json:"date"`
	DurationMinutes int           `json:"durationMinutes"`
	Slots           []domain.Slot `json:"slots"`
}

func (c *Cache) Availability(ctx context.Context, doctorID string, day time.Time) (app.Availability, error) {
	key := Key(doctorID, domain.DayOf(day))
	rctx, cancel := context.WithTimeout(ctx, Timeout)
	raw, err := c.client.Get(rctx, key).Bytes()
	cancel()
	switch {
	case err == nil:
		var v cached
		if json.Unmarshal(raw, &v) == nil {
			requests.WithLabelValues("hit").Inc()
			return app.Availability{DoctorID: v.DoctorID, Date: v.Date, Duration: time.Duration(v.DurationMinutes) * time.Minute, Slots: v.Slots}, nil
		}
		requests.WithLabelValues("miss").Inc() // unreadable value: recompute and overwrite
	case errors.Is(err, redis.Nil):
		requests.WithLabelValues("miss").Inc()
	default:
		requests.WithLabelValues("error").Inc()
		unavailable(ctx, "get", err)
		return c.Next.Availability(ctx, doctorID, day)
	}
	av, err := c.Next.Availability(ctx, doctorID, day)
	if err != nil {
		return av, err
	}
	value, _ := json.Marshal(cached{av.DoctorID, av.Date, int(av.Duration / time.Minute), av.Slots})
	rctx, cancel = context.WithTimeout(ctx, Timeout)
	defer cancel()
	if err := c.client.Set(rctx, key, value, TTL).Err(); err != nil {
		unavailable(ctx, "set", err)
	}
	return av, nil
}

// Invalidate deletes the day's key after a commit. A failure is only logged:
// the TTL bounds the staleness and the booking itself was validated in MySQL.
func (c *Cache) Invalidate(ctx context.Context, doctorID, day string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), Timeout)
	defer cancel()
	if err := c.client.Del(rctx, Key(doctorID, day)).Err(); err != nil {
		unavailable(ctx, "invalidate", err)
	}
}

func unavailable(ctx context.Context, op string, err error) {
	slog.WarnContext(ctx, "cache unavailable, using database", "event", "cache.unavailable", "op", op, "error", err.Error())
}
