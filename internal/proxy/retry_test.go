package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestIsRetryableStatus(t *testing.T) {
	retryable := []int{429, 502, 503, 504}
	for _, code := range retryable {
		if !isRetryableStatus(code) {
			t.Errorf("expected %d to be retryable", code)
		}
	}

	nonRetryable := []int{200, 201, 400, 401, 403, 404, 500}
	for _, code := range nonRetryable {
		if isRetryableStatus(code) {
			t.Errorf("expected %d to NOT be retryable", code)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	base := 100 * time.Millisecond
	maxDelay := 10 * time.Second

	// Attempt 0: delay in [0, 100ms)
	for i := 0; i < 100; i++ {
		d := backoffDelay(0, base, maxDelay)
		if d < 0 || d >= base {
			t.Fatalf("attempt 0: delay %v out of range [0, %v)", d, base)
		}
	}

	// Attempt 5: base * 2^5 = 3200ms, capped at maxDelay
	for i := 0; i < 100; i++ {
		d := backoffDelay(5, base, maxDelay)
		if d < 0 || d >= 3200*time.Millisecond {
			t.Fatalf("attempt 5: delay %v out of range [0, 3200ms)", d)
		}
	}

	// Attempt 20: delay capped at maxDelay
	for i := 0; i < 100; i++ {
		d := backoffDelay(20, base, maxDelay)
		if d < 0 || d >= maxDelay {
			t.Fatalf("attempt 20: delay %v out of range [0, %v)", d, maxDelay)
		}
	}

	// Zero base returns zero.
	d := backoffDelay(0, 0, maxDelay)
	if d != 0 {
		t.Fatalf("zero base: expected 0, got %v", d)
	}
}

func TestSleepWithContext_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	err := sleepWithContext(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context cancelled error")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("sleep should have returned immediately; took %v", elapsed)
	}
}

func TestSleepWithContext_Completes(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	err := sleepWithContext(ctx, 10*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 10*time.Millisecond {
		t.Fatalf("sleep should have waited at least 10ms; took %v", elapsed)
	}
}

func TestRetryAfterDuration(t *testing.T) {
	// No header.
	d := retryAfterDuration(nil)
	if d != 0 {
		t.Fatalf("nil response: expected 0, got %v", d)
	}

	// Integer seconds.
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "5")
	d = retryAfterDuration(resp)
	if d != 5*time.Second {
		t.Fatalf("expected 5s, got %v", d)
	}

	// Missing header.
	resp2 := &http.Response{Header: http.Header{}}
	d = retryAfterDuration(resp2)
	if d != 0 {
		t.Fatalf("no header: expected 0, got %v", d)
	}
}
