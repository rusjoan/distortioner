// Package limiter implements per-key token buckets.
package limiter

import (
	"sync"
	"time"
)

// Limiter allows Burst() requests per Window() per key, refilling smoothly.
// Both values are read on every call, so they can change at runtime.
type Limiter struct {
	Burst  func() int
	Window func() time.Duration

	mu      sync.Mutex
	buckets map[int64]*bucket
	calls   int
}

type bucket struct {
	tokens float64
	last   time.Time
}

const sweepEvery = 1024

// New creates a limiter.
func New(burst func() int, window func() time.Duration) *Limiter {
	return &Limiter{Burst: burst, Window: window, buckets: map[int64]*bucket{}}
}

// Allow takes a token for key. When none is left it reports how long to wait.
func (l *Limiter) Allow(key int64, now time.Time) (ok bool, retryAfter time.Duration) {
	capacity := float64(max(l.Burst(), 1))
	perToken := l.Window() / time.Duration(capacity)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%sweepEvery == 0 {
		l.sweep(now, capacity, perToken)
	}

	b, found := l.buckets[key]
	if !found {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[key] = b
	}
	l.refill(b, now, capacity, perToken)

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1-b.tokens)*float64(perToken)).Round(time.Second) + time.Second
}

func (l *Limiter) refill(b *bucket, now time.Time, capacity float64, perToken time.Duration) {
	if perToken <= 0 {
		b.tokens = capacity
	} else if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(capacity, b.tokens+float64(elapsed)/float64(perToken))
	}
	b.last = now
}

// sweep forgets buckets that are full again: they behave like new ones.
func (l *Limiter) sweep(now time.Time, capacity float64, perToken time.Duration) {
	for k, b := range l.buckets {
		l.refill(b, now, capacity, perToken)
		if b.tokens >= capacity {
			delete(l.buckets, k)
		}
	}
}

// Len returns the number of tracked keys.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
