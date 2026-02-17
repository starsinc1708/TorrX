package search

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"time"
)

// RetryConfig controls the exponential backoff behavior for RetryWithBackoff.
type RetryConfig struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
}

// DefaultRetryConfig returns sensible defaults: 3 attempts, 500ms→1s→2s.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}
}

// RetryWithBackoff retries fn with exponential backoff and ±25% jitter.
// It returns nil on first success, or the last error if all attempts fail.
// Context cancellation between attempts is respected.
func RetryWithBackoff(ctx context.Context, cfg RetryConfig, fn func() error) error {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}

	var lastErr error
	delay := cfg.InitialDelay

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Don't retry non-transient errors.
		if !isTransientError(lastErr) {
			return lastErr
		}

		// Don't sleep after the last attempt.
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Apply ±25% jitter.
		jittered := applyJitter(delay)
		if jittered > cfg.MaxDelay {
			jittered = cfg.MaxDelay
		}

		timer := time.NewTimer(jittered)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		// Increase delay for next attempt.
		delay = time.Duration(float64(delay) * cfg.Multiplier)
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
	return lastErr
}

// applyJitter adds ±25% randomization to duration to prevent thundering herd.
func applyJitter(d time.Duration) time.Duration {
	// jitter in range [0.75, 1.25)
	factor := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(d) * factor)
}

// isTransientError returns true for network errors that may succeed on retry:
// timeouts, connection resets, EOF, TLS handshake failures.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "tls") ||
		strings.Contains(lower, "eof")
}
