package proxy

import (
	"testing"
	"time"
)

func TestCB_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Second, 1)

	if cb.State() != CBClosed {
		t.Fatalf("initial state: got %d, want CBClosed", cb.State())
	}

	// Allow should work in closed state.
	if !cb.Allow() {
		t.Fatal("closed circuit should allow requests")
	}

	// Two failures — still closed.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CBClosed {
		t.Fatalf("after 2 failures: got %d, want CBClosed", cb.State())
	}

	// Third failure — trips to open.
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatalf("after 3 failures: got %d, want CBOpen", cb.State())
	}

	// Open circuit rejects requests.
	if cb.Allow() {
		t.Fatal("open circuit should reject requests")
	}
}

func TestCB_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond, 1)

	cb.RecordFailure() // trips to open
	if cb.State() != CBOpen {
		t.Fatalf("expected CBOpen, got %d", cb.State())
	}

	// Wait for reset timeout.
	time.Sleep(60 * time.Millisecond)

	// Allow should transition to half-open.
	if !cb.Allow() {
		t.Fatal("should allow after reset timeout")
	}
	if cb.State() != CBHalfOpen {
		t.Fatalf("expected CBHalfOpen, got %d", cb.State())
	}
}

func TestCB_HalfOpenToClosed(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond, 2)

	cb.RecordFailure() // open
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // half-open

	if cb.State() != CBHalfOpen {
		t.Fatalf("expected CBHalfOpen, got %d", cb.State())
	}

	// One success — still half-open (need 2).
	cb.RecordSuccess()
	if cb.State() != CBHalfOpen {
		t.Fatalf("expected CBHalfOpen after 1 success, got %d", cb.State())
	}

	// Second success — closed.
	cb.RecordSuccess()
	if cb.State() != CBClosed {
		t.Fatalf("expected CBClosed after 2 successes, got %d", cb.State())
	}
}

func TestCB_HalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond, 2)

	cb.RecordFailure() // open
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // half-open

	cb.RecordFailure() // back to open
	if cb.State() != CBOpen {
		t.Fatalf("expected CBOpen after half-open failure, got %d", cb.State())
	}
}

func TestCB_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Second, 1)

	cb.RecordFailure()
	cb.RecordFailure()
	// One success resets the counter.
	cb.RecordSuccess()
	cb.RecordFailure()
	cb.RecordFailure()

	// Should still be closed (only 2 consecutive failures since last success).
	if cb.State() != CBClosed {
		t.Fatalf("expected CBClosed, got %d", cb.State())
	}
}

func TestCBRegistry_LazyCreation(t *testing.T) {
	reg := NewCircuitBreakerRegistry(5, 60*time.Second, 1)

	cb1 := reg.Get("provider-a")
	cb2 := reg.Get("provider-a")

	if cb1 != cb2 {
		t.Fatal("expected same circuit breaker for same provider")
	}

	cb3 := reg.Get("provider-b")
	if cb3 == cb1 {
		t.Fatal("expected different circuit breaker for different provider")
	}

	if cb1.State() != CBClosed {
		t.Fatalf("new breaker should be closed, got %d", cb1.State())
	}
}
