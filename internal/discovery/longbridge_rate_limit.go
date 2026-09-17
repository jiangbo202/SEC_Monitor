package discovery

import (
	"context"
	"sync"
	"time"
)

// Longbridge documents a one-request-per-second limit for the fundamental
// endpoints used by company profiles and analyst ratings. Both enrichments
// share this process-wide pacer so two otherwise independent jobs cannot burst
// against the same credential window.
var longbridgeFundamentalPacer struct {
	sync.Mutex
	nextRequestAt time.Time
}

func waitLongbridgeFundamentalSlot(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	for {
		longbridgeFundamentalPacer.Lock()
		now := time.Now()
		wait := time.Until(longbridgeFundamentalPacer.nextRequestAt)
		if wait <= 0 {
			longbridgeFundamentalPacer.nextRequestAt = now.Add(interval)
			longbridgeFundamentalPacer.Unlock()
			return nil
		}
		longbridgeFundamentalPacer.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func longbridgeFundamentalCall[T any](ctx context.Context, interval time.Duration, call func(context.Context) (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < 3; attempt++ {
		if err := waitLongbridgeFundamentalSlot(ctx, interval); err != nil {
			return zero, err
		}
		result, err := call(ctx)
		if err == nil || companyProfileBulkRetryFailureKind(err) != "rate_limited" || attempt == 2 {
			return result, err
		}
		deferLongbridgeFundamentalRequests(longbridgeRateLimitCooldown(interval, attempt))
	}
	return zero, nil
}

func deferLongbridgeFundamentalRequests(delay time.Duration) {
	if delay <= 0 {
		return
	}
	longbridgeFundamentalPacer.Lock()
	defer longbridgeFundamentalPacer.Unlock()
	next := time.Now().Add(delay)
	if next.After(longbridgeFundamentalPacer.nextRequestAt) {
		longbridgeFundamentalPacer.nextRequestAt = next
	}
}

func longbridgeRateLimitCooldown(interval time.Duration, attempt int) time.Duration {
	if interval <= 0 {
		interval = time.Second
	}
	delay := interval * time.Duration(2<<attempt)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	// A bounded time-derived jitter prevents independently started processes
	// from retrying at exactly the same boundary without making waits unbounded.
	jitterWindow := interval / 2
	if jitterWindow > 0 {
		delay += time.Duration(time.Now().UnixNano() % int64(jitterWindow))
	}
	return delay
}
