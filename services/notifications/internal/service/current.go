package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"portal-paciente-clinica/internal/platform/obs"
	"strings"
	"time"
)

type AppointmentClient struct {
	URLs []string
	Key  string
	HTTP *http.Client
	Now  func() time.Time
}

func NewAppointmentClient(raw, key string) *AppointmentClient {
	client := obs.Client(400 * time.Millisecond)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &AppointmentClient{URLs: strings.Split(raw, ","), Key: key, HTTP: client, Now: time.Now}
}
func (c *AppointmentClient) Current(ctx context.Context, id, start string) (bool, error) {
	expected, e := time.Parse(time.RFC3339, start)
	if e != nil || len(c.Key) < 32 {
		return false, errors.New("invalid reminder authorization")
	}
	for _, base := range c.URLs {
		req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(strings.TrimSpace(base), "/")+"/internal/appointments/"+url.PathEscape(id), nil)
		if e != nil {
			continue
		}
		req.Header.Set("X-Service-Key", c.Key)
		r, e := c.HTTP.Do(req)
		if e != nil {
			continue
		}
		var a struct {
			Status  string    `json:"status"`
			StartAt time.Time `json:"startAt"`
		}
		e = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&a)
		r.Body.Close()
		if r.StatusCode == 404 {
			return false, nil
		}
		if r.StatusCode != 200 || e != nil {
			continue
		}
		return a.Status == "booked" && a.StartAt.Equal(expected) && a.StartAt.After(c.Now()), nil
	}
	return false, errors.New("appointment authorization unavailable")
}
