package proxy

import (
	"context"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// RetryConfig holds retry parameters for upstream requests.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// isRetryableStatus returns true if the HTTP status code indicates a
// transient error that may succeed on retry.
func isRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// backoffDelay calculates the delay for the given attempt using exponential
// backoff with full jitter. The result is clamped to [0, maxDelay].
func backoffDelay(attempt int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	exp := math.Pow(2, float64(attempt))
	delay := time.Duration(float64(base) * exp)
	if delay > maxDelay {
		delay = maxDelay
	}
	// Full jitter: uniform random in [0, delay).
	if delay > 0 {
		delay = time.Duration(rand.Int63n(int64(delay)))
	}
	return delay
}

// sleepWithContext sleeps for the given duration, returning early if the
// context is cancelled. Returns ctx.Err() if cancelled, nil otherwise.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryAfterDuration parses the Retry-After header from an HTTP response.
// It returns the parsed duration or 0 if the header is absent or unparsable.
func retryAfterDuration(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	// Try parsing as seconds (integer).
	if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// Try parsing as HTTP-date.
	if t, err := http.ParseTime(ra); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
