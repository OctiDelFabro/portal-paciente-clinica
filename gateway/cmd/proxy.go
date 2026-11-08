package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"portal-paciente-clinica/internal/platform/obs"
)

const (
	requestBudget = 5 * time.Second
	upstreamLimit = 2 * time.Second
	// Active health check of ADR-012.
	healthEvery     = 2 * time.Second
	healthTimeout   = 500 * time.Millisecond
	failuresForDown = 2
)

var (
	upstreamErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_upstream_errors_total",
		Help: "Fallas al llamar a un servicio interno, por servicio y tipo (timeout, unavailable).",
	}, []string{"upstream", "kind"})
	retries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_retries_total",
		Help: "Reintentos de GET ante 502/503/504 o error de red, por servicio.",
	}, []string{"upstream"})
	upstreamRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_upstream_requests_total",
		Help: "Intentos enviados a cada instancia de un servicio interno; muestra la distribucion del balanceo (D12).",
	}, []string{"upstream", "instance"})
	upstreamHealthy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gateway_upstream_healthy",
		Help: "1 si el gateway considera sana la instancia, 0 si la saco de la rotacion.",
	}, []string{"instance"})
)

// instance is one replica of an internal service.
type instance struct {
	url     *url.URL
	label   string
	healthy atomic.Bool
	mu      sync.Mutex
	fails   int
}

// pool balances round-robin among the healthy instances of a service. A
// single-URL service is a pool of one, so every route shares one proxy path.
type pool struct {
	name      string
	instances []*instance
	next      atomic.Uint64
	checked   atomic.Bool
}

func newPool(name string, targets []string) (*pool, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s: no upstream URL", name)
	}
	p := &pool{name: name}
	for _, t := range targets {
		u, err := url.Parse(t)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("%s: invalid upstream URL", name)
		}
		inst := &instance{url: u, label: u.Host}
		inst.healthy.Store(true)
		upstreamHealthy.WithLabelValues(inst.label).Set(1)
		p.instances = append(p.instances, inst)
	}
	return p, nil
}

// pick chooses the next healthy instance, skipping avoid (the instance a retry
// just failed on). With no healthy candidate a retry stays on avoid, and a
// first attempt tries any instance: a mark can be up to one check stale.
func (p *pool) pick(avoid *instance) *instance {
	cands := make([]*instance, 0, len(p.instances))
	for _, in := range p.instances {
		if in != avoid && in.healthy.Load() {
			cands = append(cands, in)
		}
	}
	if len(cands) == 0 && avoid != nil {
		return avoid
	}
	if len(cands) == 0 {
		cands = p.instances
	}
	return cands[(p.next.Add(1)-1)%uint64(len(cands))]
}

func (p *pool) setHealthy(in *instance, ok bool) {
	if in.healthy.Swap(ok) == ok {
		return
	}
	if ok {
		upstreamHealthy.WithLabelValues(in.label).Set(1)
		slog.Info("upstream instance up", "event", "gateway.instance_up", "upstream", p.name, "upstream_instance", in.label)
		return
	}
	upstreamHealthy.WithLabelValues(in.label).Set(0)
	slog.Warn("upstream instance down", "event", "gateway.instance_down", "upstream", p.name, "upstream_instance", in.label)
}

func (p *pool) recordCheck(in *instance, ok bool) {
	in.mu.Lock()
	if ok {
		in.fails = 0
	} else {
		in.fails++
	}
	down := in.fails >= failuresForDown
	in.mu.Unlock()
	if ok || down {
		p.setHealthy(in, ok)
	}
}

// connectionFailed takes the instance out of rotation at once; one OK check
// brings it back.
func (p *pool) connectionFailed(in *instance) {
	in.mu.Lock()
	in.fails = failuresForDown
	in.mu.Unlock()
	p.setHealthy(in, false)
}

// answered is the passive recovery for pools without an active check.
func (p *pool) answered(in *instance) {
	if !p.checked.Load() {
		p.recordCheck(in, true)
	}
}

// watch runs the active GET /healthz check until ctx ends.
func (p *pool) watch(ctx context.Context, every, timeout time.Duration) {
	p.checked.Store(true)
	client := &http.Client{Timeout: timeout}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p.checkOnce(ctx, client)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *pool) checkOnce(ctx context.Context, client *http.Client) {
	var wg sync.WaitGroup
	for _, in := range p.instances {
		wg.Add(1)
		go func(in *instance) {
			defer wg.Done()
			p.recordCheck(in, probe(ctx, client, in))
		}(in)
	}
	wg.Wait()
}

func probe(ctx context.Context, client *http.Client, in *instance) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.url.JoinPath("/healthz").String(), nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// retryTransport sends each attempt to an instance of the pool and repeats a
// GET (idempotent) once, on another instance when there is one, after a
// network error or 502/503/504, with a 100-300 ms wait and only within the
// budget. Writes are never retried: a missing response does not prove failure.
type retryTransport struct {
	pool *pool
	next http.RoundTripper
}

func (t retryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	first := t.pool.pick(nil)
	resp, err := t.send(first, r)
	if r.Method != http.MethodGet || !retryable(resp, err) {
		return resp, err
	}
	wait := time.Duration(100+rand.IntN(200)) * time.Millisecond
	if deadline, ok := r.Context().Deadline(); ok && time.Until(deadline) < wait+upstreamLimit/2 {
		return resp, err
	}
	if resp != nil {
		resp.Body.Close()
	}
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-time.After(wait):
	}
	retries.WithLabelValues(t.pool.name).Inc()
	return t.send(t.pool.pick(first), r)
}

func (t retryTransport) send(in *instance, r *http.Request) (*http.Response, error) {
	out := r.WithContext(r.Context())
	u := *r.URL
	u.Scheme, u.Host = in.url.Scheme, in.url.Host
	out.URL = &u
	upstreamRequests.WithLabelValues(t.pool.name, in.label).Inc()
	resp, err := t.next.RoundTrip(out)
	switch {
	case err != nil && connectionFailure(err):
		t.pool.connectionFailed(in)
	case err == nil:
		t.pool.answered(in)
	}
	return resp, err
}

func retryable(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}
	return resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504
}

// connectionFailure is a dial error or a broken connection; a slow answer
// (timeout) or a cancelled request says nothing about the instance's health.
func connectionFailure(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var ne net.Error
	return !(errors.As(err, &ne) && ne.Timeout())
}

// proxy is the single-URL form of poolProxy.
func proxy(name, target, stripPrefix string) http.Handler {
	p, err := newPool(name, []string{target})
	if err != nil {
		slog.Error("invalid upstream URL", "upstream", name)
		os.Exit(1)
	}
	return poolProxy(p, stripPrefix)
}

// poolProxy is the only proxy implementation of the gateway: timeouts of
// ADR-005, GET retry, correlation and tracing, balancing and identity headers.
func poolProxy(p *pool, stripPrefix string) http.Handler {
	base := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext,
		ResponseHeaderTimeout: upstreamLimit,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   50,
	}
	name := p.name
	rp := httputil.NewSingleHostReverseProxy(p.instances[0].url)
	rp.Transport = retryTransport{pool: p, next: obs.Transport(base)}
	// El middleware del gateway ya devuelve X-Correlation-ID; sin esto el proxy
	// copiaria tambien el del servicio y el header saldria duplicado. CORS lo
	// resuelve solo el gateway, por la misma razon.
	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del(obs.CorrelationHeader)
		for k := range resp.Header {
			if strings.HasPrefix(k, "Access-Control-") {
				resp.Header.Del(k)
			}
		}
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		var ne net.Error
		if errors.Is(e, context.DeadlineExceeded) || (errors.As(e, &ne) && ne.Timeout()) {
			upstreamErrors.WithLabelValues(name, "timeout").Inc()
			slog.WarnContext(r.Context(), "upstream timeout", "event", "gateway.upstream_timeout", "upstream", name)
			sendError(w, 504, "UPSTREAM_TIMEOUT", "El servicio no respondio a tiempo; el resultado puede ser indeterminado.")
			return
		}
		upstreamErrors.WithLabelValues(name, "unavailable").Inc()
		slog.WarnContext(r.Context(), "upstream unavailable", "event", "gateway.upstream_unavailable", "upstream", name, "error", e.Error())
		sendError(w, 502, "UPSTREAM_UNAVAILABLE", "Servicio no disponible.")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestBudget)
		defer cancel()
		r = r.WithContext(ctx)
		// La identidad solo la escribe el gateway, desde un token verificado.
		if r.Header = r.Header.Clone(); r.Header == nil {
			r.Header = http.Header{}
		}
		// ReverseProxy deletes every header the client names in Connection
		// after this handler runs; drop those names here so a client cannot
		// use them to erase the identity set below.
		for _, v := range r.Header.Values("Connection") {
			for _, f := range strings.Split(v, ",") {
				if f = textproto.TrimString(f); f != "" {
					r.Header.Del(f)
				}
			}
		}
		r.Header.Del("Connection")
		r.Header.Del(patientHeader)
		r.Header.Del(roleHeader)
		if id, ok := identityFrom(ctx); ok {
			r.Header.Set(patientHeader, id.subject)
			r.Header.Set(roleHeader, id.role)
		}
		if stripPrefix != "" {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, stripPrefix)
			r.URL.RawPath = ""
		}
		rp.ServeHTTP(w, r)
	})
}
