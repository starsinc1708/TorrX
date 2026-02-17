package search

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryWithBackoff_SucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), DefaultRetryConfig(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryWithBackoff_SucceedsOnNthAttempt(t *testing.T) {
	var calls atomic.Int32
	transientErr := fmt.Errorf("connection reset")
	err := RetryWithBackoff(context.Background(), DefaultRetryConfig(), func() error {
		n := calls.Add(1)
		if n < 3 {
			return transientErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error after retries, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 calls, got %d", got)
	}
}

func TestRetryWithBackoff_ExhaustsAllAttempts(t *testing.T) {
	transientErr := fmt.Errorf("timeout")
	calls := 0
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return transientErr
	})
	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if err.Error() != "timeout" {
		t.Fatalf("expected last error 'timeout', got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryWithBackoff_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cfg := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}
	// Cancel after first attempt completes.
	err := RetryWithBackoff(ctx, cfg, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return fmt.Errorf("connection reset")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call before cancellation, got %d", calls)
	}
}

func TestRetryWithBackoff_ExponentialDelay(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  4,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}

	var timestamps []time.Time
	_ = RetryWithBackoff(context.Background(), cfg, func() error {
		timestamps = append(timestamps, time.Now())
		return fmt.Errorf("timeout")
	})

	if len(timestamps) != 4 {
		t.Fatalf("expected 4 timestamps, got %d", len(timestamps))
	}

	// Check that delays roughly double each time (with ±25% jitter tolerance).
	// Expected: ~50ms, ~100ms, ~200ms between calls.
	for i := 1; i < len(timestamps); i++ {
		gap := timestamps[i].Sub(timestamps[i-1])
		expectedBase := cfg.InitialDelay
		for j := 1; j < i; j++ {
			expectedBase = time.Duration(float64(expectedBase) * cfg.Multiplier)
		}
		// Allow generous tolerance: 50% of expected base to 200% (jitter + scheduling).
		minGap := time.Duration(float64(expectedBase) * 0.5)
		maxGap := time.Duration(float64(expectedBase) * 2.0)
		if gap < minGap || gap > maxGap {
			t.Errorf("gap[%d] = %v, expected roughly %v (range %v - %v)", i, gap, expectedBase, minGap, maxGap)
		}
	}
}

func TestRetryWithBackoff_MaxDelayCap(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:  4,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     60 * time.Millisecond,
		Multiplier:   10.0,
	}

	var timestamps []time.Time
	_ = RetryWithBackoff(context.Background(), cfg, func() error {
		timestamps = append(timestamps, time.Now())
		return fmt.Errorf("timeout")
	})

	if len(timestamps) != 4 {
		t.Fatalf("expected 4 timestamps, got %d", len(timestamps))
	}

	// After the first attempt, delays should be capped at MaxDelay (60ms).
	// Gap[1] = 50ms (initial), gap[2] = capped at 60ms, gap[3] = capped at 60ms.
	for i := 2; i < len(timestamps); i++ {
		gap := timestamps[i].Sub(timestamps[i-1])
		// With jitter and cap at 60ms, gap should be under ~80ms.
		maxAllowed := time.Duration(float64(cfg.MaxDelay) * 1.5)
		if gap > maxAllowed {
			t.Errorf("gap[%d] = %v exceeds max delay cap of %v (with tolerance %v)", i, gap, cfg.MaxDelay, maxAllowed)
		}
	}
}

func TestRetryWithBackoff_NonTransientErrorFailsImmediately(t *testing.T) {
	nonTransientErr := fmt.Errorf("parse error: invalid JSON")
	calls := 0
	cfg := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}
	err := RetryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return nonTransientErr
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (non-transient should not retry), got %d", calls)
	}
}

func TestExponentialBlockDuration(t *testing.T) {
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{3, 2 * time.Minute},  // 2min × 2^0 = 2min
		{4, 4 * time.Minute},  // 2min × 2^1 = 4min
		{5, 8 * time.Minute},  // 2min × 2^2 = 8min
		{6, 15 * time.Minute}, // 2min × 2^3 = 16min → capped at 15min
		{7, 15 * time.Minute}, // capped
		{10, 15 * time.Minute},
	}
	for _, tt := range tests {
		got := exponentialBlockDuration(tt.failures)
		if got != tt.want {
			t.Errorf("exponentialBlockDuration(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}

func TestCircuitBreakerExponentialBlock(t *testing.T) {
	svc := NewService([]Provider{
		&fakeProvider{name: "testprov"},
	}, 2*time.Second)

	providerKey := "testprov"
	baseTime := time.Now()
	testErr := fmt.Errorf("connection timeout")

	// Record failures up to threshold (3).
	for i := 0; i < providerFailureThreshold; i++ {
		svc.recordProviderResult(providerKey, "test", testErr, 100*time.Millisecond, baseTime)
	}

	// Provider should be blocked for 2min (base duration).
	blocked, until, _ := svc.isProviderBlocked(providerKey, baseTime)
	if !blocked {
		t.Fatal("expected provider to be blocked after threshold failures")
	}
	expectedDuration := providerBlockBase
	actualDuration := until.Sub(baseTime)
	if actualDuration != expectedDuration {
		t.Fatalf("first block: expected %v, got %v", expectedDuration, actualDuration)
	}

	// Simulate time passing, block expires, then another failure batch.
	afterBlock := until.Add(1 * time.Second)
	blocked, _, _ = svc.isProviderBlocked(providerKey, afterBlock)
	if blocked {
		t.Fatal("provider should be unblocked after block expires")
	}

	// One more failure (consecutive count is now threshold+1).
	svc.recordProviderResult(providerKey, "test", testErr, 100*time.Millisecond, afterBlock)

	blocked, until, _ = svc.isProviderBlocked(providerKey, afterBlock)
	if !blocked {
		t.Fatal("expected provider to be blocked after additional failure")
	}
	// consecutiveFailures = 4 → 2min × 2^1 = 4min
	expectedDuration = 4 * time.Minute
	actualDuration = until.Sub(afterBlock)
	if actualDuration != expectedDuration {
		t.Fatalf("second block: expected %v, got %v", expectedDuration, actualDuration)
	}

	// Success should reset consecutive failures.
	svc.recordProviderResult(providerKey, "test", nil, 50*time.Millisecond, afterBlock.Add(1*time.Second))
	blocked, _, _ = svc.isProviderBlocked(providerKey, afterBlock.Add(2*time.Second))
	if blocked {
		t.Fatal("provider should be unblocked after success")
	}

	// After success reset, next failure batch should start from base duration again.
	resetTime := afterBlock.Add(3 * time.Second)
	for i := 0; i < providerFailureThreshold; i++ {
		svc.recordProviderResult(providerKey, "test", testErr, 100*time.Millisecond, resetTime)
	}
	blocked, until, _ = svc.isProviderBlocked(providerKey, resetTime)
	if !blocked {
		t.Fatal("expected provider to be blocked again")
	}
	actualDuration = until.Sub(resetTime)
	if actualDuration != providerBlockBase {
		t.Fatalf("block after reset: expected %v, got %v", providerBlockBase, actualDuration)
	}
}
