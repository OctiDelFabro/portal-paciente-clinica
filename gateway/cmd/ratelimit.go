package main

import (
	"math"
	"sync"
	"time"
)

// maxBuckets bounds the limiter memory; when reached, full (idle) buckets
// are dropped, since a new bucket starts full anyway.
const maxBuckets = 10000

type bucket struct {
	tokens float64
	last   time.Time
}

// tokenBucket limits each key to rate requests per second with bursts of up
// to burst. It has the same Allow shape as apikey.Limiter (fixed window per
// minute), which suits partner quotas but not portal bursts. The state lives
// in each gateway instance.
type tokenBucket struct {
	rate, burst float64

	mu        sync.Mutex
	buckets   map[string]*bucket
	nextPrune time.Time
}

func newTokenBucket(rps float64, burst int) *tokenBucket {
	return &tokenBucket{rate: rps, burst: float64(burst), buckets: map[string]*bucket{}}
}

// Allow takes a token for key; when there is none it returns the seconds to
// wait for Retry-After.
func (l *tokenBucket) Allow(key string, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets && !now.Before(l.nextPrune) {
			l.prune(now)
			l.nextPrune = now.Add(time.Second)
		}
		if len(l.buckets) >= maxBuckets {
			return false, 1
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	l.refill(b, now)
	if b.tokens < 1 {
		return false, max(1, int(math.Ceil((1-b.tokens)/l.rate)))
	}
	b.tokens--
	return true, 0
}

func (l *tokenBucket) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
}

func (l *tokenBucket) prune(now time.Time) {
	for k, b := range l.buckets {
		if l.refill(b, now); b.tokens >= l.burst {
			delete(l.buckets, k)
		}
	}
}
