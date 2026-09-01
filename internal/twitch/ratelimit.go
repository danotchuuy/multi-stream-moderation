package twitch

import (
	"context"
	"sync"
	"time"
)

// rateLimiter is a simple token-bucket-over-a-sliding-window limiter: at
// most max calls to Wait succeed within any window-length interval.
type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	times  []time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window}
}

// Wait blocks until a slot is available or ctx is done.
func (r *rateLimiter) Wait(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-r.window)
		i := 0
		for i < len(r.times) && r.times[i].Before(cutoff) {
			i++
		}
		r.times = r.times[i:]

		if len(r.times) < r.max {
			r.times = append(r.times, now)
			r.mu.Unlock()
			return nil
		}
		wait := r.times[0].Add(r.window).Sub(now)
		r.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
