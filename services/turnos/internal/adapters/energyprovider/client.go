// Package energyprovider is the outbound adapter for the consumption
// estimate of group ArquitecturasoftwareRenewableEnergy (ADR-009). It
// implements app.EnergyEstimator and is an anticorruption layer: provider
// fields, codes and errors never leave it.
package energyprovider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/internal/platform/obs"
	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// Operation is the only provider operation we use (metric label).
const Operation = "estimate_consumption"

const path = "/api/v1/estimaciones-consumo"

// ADR-011: outcome takes the fixed Kind values plus "ok"; one series per outcome.
var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "provider_requests_total",
		Help: "Calls to the energy provider by operation and outcome (circuit_open: rejected locally).",
	}, []string{"operation", "outcome"})
	duration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "provider_request_duration_seconds",
		Help:    "Duration of each HTTP attempt to the energy provider.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2, 3, 5},
	}, []string{"operation"})
	circuit = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "provider_circuit_state",
		Help: "Energy provider circuit breaker: 0 closed, 1 half-open, 2 open.",
	})
)

// Client calls POST /api/v1/estimaciones-consumo.
type Client struct {
	URL    string // base URL, without the path
	APIKey string // X-API-Key; never logged
	HTTP   *http.Client
	// Budget is the total time for one Estimate, retry included (provider
	// guidance: 5 s). A shorter caller deadline wins.
	Budget  time.Duration
	Breaker *Breaker // optional
	Jitter  func() time.Duration
}

// New builds a client with the provider's guidance: 5 s budget, one retry
// with 50-250 ms jitter, tracing transport, and a 5-failure/30 s breaker.
func New(url, apiKey string) *Client {
	return &Client{
		URL: strings.TrimRight(url, "/"), APIKey: apiKey,
		HTTP:    &http.Client{Transport: obs.Transport(http.DefaultTransport), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Budget:  5 * time.Second,
		Breaker: NewBreaker(),
		Jitter:  func() time.Duration { return 50*time.Millisecond + rand.N(200*time.Millisecond) },
	}
}

// minRetryWindow: a retry is sent only if this much budget remains after the wait.
const minRetryWindow = 200 * time.Millisecond

// requestID derives X-Request-Id from the correlation id, limited to the
// provider's charset [A-Za-z0-9_-] and 64 characters.
func requestID(ctx context.Context) string {
	id := obs.CorrelationID(ctx)
	if id == "" {
		id = obs.NewID()
	}
	b := []byte(id)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			b[i] = '_'
		}
	}
	if len(b) > 64 {
		b = b[:64]
	}
	return string(b)
}

// Estimate implements app.EnergyEstimator.
func (c *Client) Estimate(ctx context.Context, cons domain.Consumption) (app.EnergyEstimate, error) {
	if c.Breaker != nil && !c.Breaker.Allow() {
		requests.WithLabelValues(Operation, string(KindCircuitOpen)).Inc()
		err := &Error{Kind: KindCircuitOpen}
		c.logFailure(ctx, err, 0, 0)
		return app.EnergyEstimate{}, err
	}
	budget := c.Budget
	if budget <= 0 {
		budget = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	body, err := requestBody(cons)
	if err != nil {
		return app.EnergyEstimate{}, err
	}
	begin, id := time.Now(), requestID(ctx)
	var est app.EnergyEstimate
	attempts := 0
	for {
		attempts++
		est, err = c.attempt(ctx, body, id)
		var pe *Error
		retryable := errors.As(err, &pe) && (pe.Kind == KindTransport || pe.Status == http.StatusServiceUnavailable)
		if !retryable || attempts == 2 {
			break
		}
		wait := c.Jitter()
		if deadline, _ := ctx.Deadline(); time.Until(deadline) < wait+minRetryWindow {
			break
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
	if c.Breaker != nil {
		c.Breaker.Record(reachable(err))
		circuit.Set(float64(c.Breaker.State()))
	}
	if err != nil {
		c.logFailure(ctx, err, attempts, time.Since(begin))
	}
	return est, err
}

// reachable: a 4xx means the provider answered; only transport, timeouts,
// 5xx and responses outside the contract count towards opening the circuit.
func reachable(err error) bool {
	var pe *Error
	if err == nil || !errors.As(err, &pe) {
		return err == nil
	}
	switch pe.Kind {
	case KindInvalidRequest, KindUnauthorized, KindForbidden, KindRateLimited:
		return true
	}
	return false
}

func (c *Client) attempt(ctx context.Context, body []byte, id string) (app.EnergyEstimate, error) {
	start := time.Now()
	est, err := c.send(ctx, body, id)
	duration.WithLabelValues(Operation).Observe(time.Since(start).Seconds())
	outcome := "ok"
	var pe *Error
	if errors.As(err, &pe) {
		outcome = string(pe.Kind)
	}
	requests.WithLabelValues(Operation, outcome).Inc()
	return est, err
}

func (c *Client) send(ctx context.Context, body []byte, id string) (app.EnergyEstimate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return app.EnergyEstimate{}, &Error{Kind: KindTransport}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("X-Request-Id", id)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return app.EnergyEstimate{}, &Error{Kind: KindTimeout}
		}
		return app.EnergyEstimate{}, &Error{Kind: KindTransport}
	}
	defer resp.Body.Close()
	// 64 KiB is far above a 100-item answer; a larger body is not the contract.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		if ctx.Err() != nil {
			return app.EnergyEstimate{}, &Error{Kind: KindTimeout, Status: resp.StatusCode}
		}
		return app.EnergyEstimate{}, &Error{Kind: KindTransport, Status: resp.StatusCode}
	}
	return parseResponse(resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header, raw)
}

// logFailure: WARN because the booking degrades to "pending" by itself (ADR-011).
// Never logs the key, the request or the response body.
func (c *Client) logFailure(ctx context.Context, err error, attempts int, elapsed time.Duration) {
	var pe *Error
	if !errors.As(err, &pe) {
		return
	}
	attrs := []any{"event", "provider.unavailable", "operation", Operation, "outcome", string(pe.Kind),
		"status", pe.Status, "attempt", attempts, "duration_ms", elapsed.Milliseconds()}
	if pe.Code != "" {
		attrs = append(attrs, "reason", pe.Code)
	}
	if pe.RetryAfter > 0 {
		attrs = append(attrs, "retry_after_s", int(pe.RetryAfter.Seconds()))
	}
	slog.WarnContext(ctx, "energy provider unavailable", attrs...)
}
