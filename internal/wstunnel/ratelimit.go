package wstunnel

import (
	"sync"
	"time"
)

// RateLimiter is a token bucket over handshake attempts. It is intentionally
// tiny and dependency-free: the only thing being limited is the rate at which
// new tunnels may be *created*, so a single shared bucket with a burst
// allowance is the whole requirement — no per-key buckets, no fairness.
//
// A nil or zero-rate limiter allows everything, so callers can hold one
// unconditionally.
type RateLimiter struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter of rate handshakes/second with the given
// burst. A non-positive rate disables limiting. A non-positive burst defaults
// to one second's worth (minimum 1), so a rate alone is a usable config.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	if rate <= 0 {
		return nil
	}
	b := float64(burst)
	if b <= 0 {
		b = rate
		if b < 1 {
			b = 1
		}
	}
	return &RateLimiter{rate: rate, burst: b, tokens: b, last: time.Now()}
}

// Allow consumes one token, reporting whether the handshake may proceed.
func (l *RateLimiter) Allow() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
