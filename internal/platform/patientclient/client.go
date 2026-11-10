// Package patientclient is the authenticated, bounded internal contract owned
// by Pacientes. Consumers never infer verification from a JWT or a seed list.
package patientclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"portal-paciente-clinica/internal/platform/obs"
)

var ErrNotFound = errors.New("patient not found")
var ErrUnavailable = errors.New("patient directory unavailable")

const KeyHeader = "X-Service-Key"

type Profile struct {
	PatientID     string  `json:"patientId"`
	FullName      string  `json:"fullName"`
	Email         *string `json:"email"`
	Enabled       bool    `json:"enabled"`
	ResponsibleID string  `json:"responsiblePatientId"`
	BirthDate     string  `json:"birthDate"`
	Role          string  `json:"role"`
}

type Client struct {
	URL, Key string
	HTTP     *http.Client
}

func New(base, key string) *Client {
	client := obs.Client(400 * time.Millisecond)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{URL: strings.TrimRight(base, "/"), Key: key, HTTP: client}
}
func (c *Client) Profile(ctx context.Context, id string) (Profile, error) {
	var p Profile
	if c == nil || len(c.Key) < 32 {
		return p, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+"/internal/patients/"+url.PathEscape(id), nil)
	if err != nil {
		return p, ErrUnavailable
	}
	req.Header.Set(KeyHeader, c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return p, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return p, ErrNotFound
	}
	if resp.StatusCode != 200 {
		return p, ErrUnavailable
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&p); err != nil || p.PatientID != id || p.Role == "" {
		return Profile{}, ErrUnavailable
	}
	return p, nil
}
