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
	for attempt := 0; attempt < 2; attempt++ {
		if err := waitLongbridgeFundamentalSlot(ctx, interval); err != nil {
			return zero, err
		}
		result, err := call(ctx)
		if err == nil || companyProfileBulkRetryFailureKind(err) != "rate_limited" || attempt == 1 {
			return result, err
		}
	}
	return zero, nil
}
