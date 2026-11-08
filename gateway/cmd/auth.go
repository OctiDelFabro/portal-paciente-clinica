package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Portal identity (ADR-014): Pacientes issues the JWT, the gateway verifies
// it and forwards the identity to the services as headers.
const (
	patientHeader  = "X-Patient-Id"
	roleHeader     = "X-User-Role"
	tokenIssuer    = "pacientes"
	minSecretBytes = 32
)

var rateLimited = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gateway_rate_limited_total",
	Help: "Solicitudes del portal rechazadas con 429 por el limite por paciente o IP, por ruta.",
}, []string{"route"})

// safeClaim keeps sub and role safe to forward as header values and as
// limiter keys: no control characters, bounded length.
var safeClaim = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)

var errInvalidClaims = errors.New("invalid sub or role claim")

type identity struct{ subject, role string }

type identityKey struct{}

func withIdentity(ctx context.Context, id identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

func identityFrom(ctx context.Context) (identity, bool) {
	id, ok := ctx.Value(identityKey{}).(identity)
	return id, ok
}

type authenticator struct{ secret []byte }

func newAuthenticator(secret string) (*authenticator, error) {
	if len(secret) < minSecretBytes {
		return nil, fmt.Errorf("JWT_SECRET must be at least %d bytes", minSecretBytes)
	}
	return &authenticator{secret: []byte(secret)}, nil
}

type portalClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// verify accepts only HS256 tokens signed with the shared secret, issued by
// Pacientes, with an expiry, a subject and a role.
func (a *authenticator) verify(raw string) (identity, error) {
	var c portalClaims
	_, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return a.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(tokenIssuer), jwt.WithExpirationRequired())
	if err != nil {
		return identity{}, err
	}
	if !safeClaim.MatchString(c.Subject) || !safeClaim.MatchString(c.Role) {
		return identity{}, errInvalidClaims
	}
	return identity{subject: c.Subject, role: c.Role}, nil
}

// fromRequest returns the verified identity of a "Bearer <JWT>" header.
func (a *authenticator) fromRequest(r *http.Request) (identity, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return identity{}, false
	}
	id, err := a.verify(token)
	return id, err == nil
}

type access int

const (
	open      access = iota // no token needed; a valid one still identifies the caller
	protected               // a valid token is required
)

// portal guards an /api route: it identifies the caller, applies the rate
// limit (per patient, or per IP when anonymous, so that floods of invalid
// tokens are limited too) and then the route's access policy.
func (c config) portal(route string, need access, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, authed := c.auth.fromRequest(r)
		key := "ip:" + clientIP(r)
		if authed {
			key = "sub:" + id.subject
		}
		if ok, retry := c.limiter.Allow(key, time.Now()); !ok {
			rateLimited.WithLabelValues(route).Inc()
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			sendError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Demasiadas solicitudes; reintente mas tarde.")
			return
		}
		if !authed && need == protected {
			w.Header().Set("WWW-Authenticate", `Bearer realm="portal"`)
			sendError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token ausente, vencido o invalido.")
			return
		}
		if authed {
			r = r.WithContext(withIdentity(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the TCP peer. X-Forwarded-For is not trusted: the internal
// listener is reached directly, never through a proxy that sets it.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
