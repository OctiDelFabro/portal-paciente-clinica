//go:build integration

// Integration test against a real Redis (ADR-007). Requires Docker:
// go test -tags integration ./services/turnos/...
package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func startCache(t *testing.T, next *countingQuery) *Cache {
	t.Helper()
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:7.4-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opt, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	c := New(opt.Addr, next)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestMissThenHitServesTheSecondReadFromRedis(t *testing.T) {
	next := &countingQuery{}
	c := startCache(t, next)
	hits, misses := testutil.ToFloat64(requests.WithLabelValues("hit")), testutil.ToFloat64(requests.WithLabelValues("miss"))

	first, err := c.Availability(context.Background(), "doc-001", day)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Availability(context.Background(), "doc-001", day)
	if err != nil {
		t.Fatal(err)
	}
	if next.calls.Load() != 1 {
		t.Fatalf("want one database read, got %d", next.calls.Load())
	}
	if !second.Slots[0].StartAt.Equal(first.Slots[0].StartAt) || second.Slots[0].StartAt.Format(time.RFC3339) != "2026-11-12T09:00:00-03:00" {
		t.Fatalf("cached value differs: %+v vs %+v", second, first)
	}
	if testutil.ToFloat64(requests.WithLabelValues("miss"))-misses != 1 || testutil.ToFloat64(requests.WithLabelValues("hit"))-hits != 1 {
		t.Fatal("want one miss and one hit counted")
	}
	ttl, err := c.client.TTL(context.Background(), Key("doc-001", "2026-11-12")).Result()
	if err != nil || ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("want TTL within 30 s, got %v err=%v", ttl, err)
	}
}

func TestInvalidateForcesTheNextReadToTheDatabase(t *testing.T) {
	next := &countingQuery{}
	c := startCache(t, next)
	_, _ = c.Availability(context.Background(), "doc-001", day)
	c.Invalidate(context.Background(), "doc-001", "2026-11-12")
	_, _ = c.Availability(context.Background(), "doc-001", day)
	if next.calls.Load() != 2 {
		t.Fatalf("want a database read after invalidation, got %d reads", next.calls.Load())
	}
	// Another doctor's key is untouched.
	_, _ = c.Availability(context.Background(), "doc-002", day)
	c.Invalidate(context.Background(), "doc-001", "2026-11-12")
	_, _ = c.Availability(context.Background(), "doc-002", day)
	if next.calls.Load() != 3 {
		t.Fatalf("invalidation leaked to another doctor: %d reads", next.calls.Load())
	}
}
