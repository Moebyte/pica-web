// Package ratelimit implements a small per-key token bucket.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens   float64
	last     time.Time
	lastSeen time.Time
}

// Limiter is a keyed (per-IP) token bucket. RPS <= 0 disables limiting.
type Limiter struct {
	mu        sync.Mutex
	rps       float64
	burst     float64
	buckets   map[string]*bucket
	lastSweep time.Time
	now       func() time.Time
}

// New creates a limiter.
func New(rps float64, burst int) *Limiter {
	if rps > 0 && burst <= 0 {
		burst = int(rps * 2)
		if burst < 1 {
			burst = 1
		}
	}
	return &Limiter{
		rps:     rps,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow consumes one token for key.
func (l *Limiter) Allow(key string) bool {
	if l == nil || l.rps <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastSweep.IsZero() || now.Sub(l.lastSweep) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.lastSeen) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now, lastSeen: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rps)
			b.last = now
		}
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
