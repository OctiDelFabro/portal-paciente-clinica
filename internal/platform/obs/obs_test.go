package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewarePropagatesValidCorrelationID(t *testing.T) {
	var seen string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, r *http.Request) { seen = CorrelationID(r.Context()) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/things/42", nil)
	req.Header.Set(CorrelationHeader, "abc-123")
	Middleware("test", mux).ServeHTTP(rec, req)
	if seen != "abc-123" || rec.Header().Get(CorrelationHeader) != "abc-123" {
		t.Fatalf("correlation not propagated: handler=%q header=%q", seen, rec.Header().Get(CorrelationHeader))
	}
}

func TestMiddlewareReplacesUnsafeCorrelationID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) {})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(CorrelationHeader, "bad value\nwith newline")
	Middleware("test", mux).ServeHTTP(rec, req)
	if got := rec.Header().Get(CorrelationHeader); !ValidCorrelationID(got) || got == "bad value\nwith newline" {
		t.Fatalf("unsafe correlation id kept: %q", got)
	}
}

func TestLoggerAddsCorrelationField(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(contextHandler{slog.NewJSONHandler(&buf, nil)})
	logger.InfoContext(WithCorrelationID(context.Background(), "corr-1"), "hello")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["correlation_id"] != "corr-1" {
		t.Fatalf("missing correlation_id: %v", line)
	}
}
