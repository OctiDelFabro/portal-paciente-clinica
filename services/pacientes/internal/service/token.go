package service

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MinSecretBytes is the minimum HS256 key length (RFC 7518 3.2: key >= hash size).
const MinSecretBytes = 32

var ErrWeakSecret = errors.New("JWT_SECRET must be at least 32 bytes")

// Claims shared with the gateway (ADR-014): sub, role, iss, iat, exp.
type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

// Issuer signs access tokens with HS256 and the secret shared with the gateway.
type Issuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewIssuer(secret string, ttl time.Duration, now func() time.Time) (*Issuer, error) {
	if len(secret) < MinSecretBytes {
		return nil, ErrWeakSecret
	}
	if ttl <= 0 {
		return nil, errors.New("token TTL must be positive")
	}
	return &Issuer{secret: []byte(secret), ttl: ttl, now: now}, nil
}

// Issue returns a signed token for the patient or doctor and its lifetime in seconds.
func (i *Issuer) Issue(patientID, role string) (string, int, error) {
	if role == "" {
		role = "patient"
	}
	now := i.now().UTC().Truncate(time.Second)
	claims := Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{
		Subject: patientID, Issuer: "pacientes",
		IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
	}}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	return signed, int(i.ttl / time.Second), err
}
