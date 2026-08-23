package discovery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLongbridgeFundamentalCallRetriesRateLimitOnce(t *testing.T) {
	longbridgeFundamentalPacer.Lock()
	longbridgeFundamentalPacer.nextRequestAt = time.Time{}
	longbridgeFundamentalPacer.Unlock()
	calls := 0
	result, err := longbridgeFundamentalCall(context.Background(), time.Millisecond, func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("HTTP 429 rate limited")
		}
		return "ok", nil
	})
	if err != nil || result != "ok" || calls != 2 {
		t.Fatalf("result=%q calls=%d err=%v", result, calls, err)
	}
}

func TestLongbridgeFundamentalCallDoesNotRetryOtherFailures(t *testing.T) {
	calls := 0
	_, err := longbridgeFundamentalCall(context.Background(), 0, func(context.Context) (string, error) {
		calls++
		return "", errors.New("permission denied")
	})
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
