package rediscache

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"portal-paciente-clinica/services/turnos/internal/app"
	"portal-paciente-clinica/services/turnos/internal/domain"
)

// countingQuery stands for the MySQL read path and counts how often it runs.
type countingQuery struct{ calls atomic.Int32 }

func (q *countingQuery) Availability(_ context.Context, doctorID string, day time.Time) (app.Availability, error) {
	q.calls.Add(1)
	start := day.Add(9 * time.Hour)
	return app.Availability{DoctorID: doctorID, Date: domain.DayOf(day), Duration: 30 * time.Minute,
		Slots: []domain.Slot{{StartAt: start, EndAt: start.Add(30 * time.Minute)}}}, nil
}

var day = time.Date(2026, 11, 12, 0, 0, 0, 0, domain.ClinicZone)

func TestKeyFollowsADR007(t *testing.T) {
	if got := Key("doc-001", "2026-11-12"); got != "availability:v1:doc-001:2026-11-12" {
		t.Fatalf("got %s", got)
	}
}

// silentRedis accepts connections and never answers, like a hung Redis.
func silentRedis(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	return ln.Addr().String()
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestUnavailableRedisFallsBackToTheDatabaseWithinTheTimeout(t *testing.T) {
	logs := captureLogs(t)
	next := &countingQuery{}
	c := New(silentRedis(t), next)
	defer c.Close()
	before := testutil.ToFloat64(requests.WithLabelValues("error"))

	start := time.Now()
	av, err := c.Availability(context.Background(), "doc-001", day)
	elapsed := time.Since(start)

	if err != nil || len(av.Slots) != 1 || next.calls.Load() != 1 {
		t.Fatalf("want the database answer, got %+v err=%v calls=%d", av, err, next.calls.Load())
	}
	// Get and the skipped Set are bounded by 100 ms each; allow scheduling slack.
	if elapsed > 600*time.Millisecond {
		t.Fatalf("fallback took %v; Redis timeout is not enforced", elapsed)
	}
	if got := testutil.ToFloat64(requests.WithLabelValues("error")) - before; got != 1 {
		t.Fatalf("want cache_requests_total{result=error} +1, got %v", got)
	}
	var line map[string]any
	if err := json.Unmarshal(bytes.SplitN(logs.Bytes(), []byte("\n"), 2)[0], &line); err != nil {
		t.Fatalf("no log line: %q", logs.String())
	}
	if line["level"] != "WARN" || line["event"] != "cache.unavailable" {
		t.Fatalf("want WARN cache.unavailable, got %v", line)
	}
}

func TestInvalidateWithUnavailableRedisDoesNotFailTheCaller(t *testing.T) {
	logs := captureLogs(t)
	c := New(silentRedis(t), &countingQuery{})
	defer c.Close()
	start := time.Now()
	c.Invalidate(context.Background(), "doc-001", "2026-11-12")
	if time.Since(start) > 400*time.Millisecond || !bytes.Contains(logs.Bytes(), []byte(`"cache.unavailable"`)) {
		t.Fatalf("took %v, logs %q", time.Since(start), logs.String())
	}
}
