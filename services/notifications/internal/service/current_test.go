package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReminderReadsCurrentReservationAndFailsClosed(t *testing.T) {
	now := time.Now()
	start := now.Add(time.Hour).Truncate(time.Second)
	status := "booked"
	reply := 200
	redirectHits := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectHits++ }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Key") != strings.Repeat("k", 32) {
			t.Error("missing service auth")
		}
		if reply == 302 {
			http.Redirect(w, r, other.URL, 302)
			return
		}
		w.WriteHeader(reply)
		json.NewEncoder(w).Encode(map[string]any{"status": status, "startAt": start})
	}))
	defer server.Close()
	client := NewAppointmentClient(server.URL, strings.Repeat("k", 32))
	client.Now = func() time.Time { return now }
	check := func(want bool, wantErr bool) {
		t.Helper()
		got, err := client.Current(context.Background(), "t-1", start.Format(time.RFC3339))
		if got != want || (err != nil) != wantErr {
			t.Fatalf("current=%v err=%v", got, err)
		}
	}
	check(true, false)
	status = "cancelled"
	check(false, false)
	status = "booked"
	now = start
	check(false, false)
	now = start.Add(-time.Hour)
	reply = 404
	check(false, false)
	reply = 503
	check(false, true)
	reply = 302
	check(false, true)
	if redirectHits != 0 {
		t.Fatal("service key forwarded through redirect")
	}
}
