package engine

import (
	"context"
	"sync"
	"time"
)

// tokenBucket limits throughput to rate messages per second with a burst of one second's worth.
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	tokens float64
	last   time.Time
}

func newTokenBucket(rate int) *tokenBucket {
	return &tokenBucket{rate: float64(rate), tokens: float64(rate), last: time.Now()}
}

func (b *tokenBucket) setRate(rate int) {
	b.mu.Lock()
	b.rate = float64(rate)
	b.mu.Unlock()
}

func (b *tokenBucket) refill(now time.Time) {
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.rate {
		b.tokens = b.rate
	}
	b.last = now
}

// Allow takes a token if one is available.
func (b *tokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(time.Now())
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Wait blocks until a token is available.
func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		b.refill(time.Now())
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
