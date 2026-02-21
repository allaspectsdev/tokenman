package proxy

import (
	"sync"
	"time"
)

// CBState represents the state of a circuit breaker.
type CBState int

const (
	// CBClosed means the circuit is healthy; requests flow through.
	CBClosed CBState = iota
	// CBOpen means the circuit has tripped; requests are rejected.
	CBOpen
	// CBHalfOpen means the circuit is testing recovery; limited requests are allowed.
	CBHalfOpen
)

// CircuitBreaker implements a per-provider circuit breaker with three states:
// Closed → Open (after failureThreshold consecutive failures)
// Open → HalfOpen (after resetTimeout elapses)
// HalfOpen → Closed (after halfOpenMax consecutive successes) or back to Open on failure.
type CircuitBreaker struct {
	mu sync.Mutex

	state            CBState
	failureThreshold int
	resetTimeout     time.Duration
	halfOpenMax      int

	consecutiveFailures int
	halfOpenSuccesses   int
	lastFailureTime     time.Time
}

// NewCircuitBreaker creates a circuit breaker with the given parameters.
func NewCircuitBreaker(failureThreshold int, resetTimeout time.Duration, halfOpenMax int) *CircuitBreaker {
	return &CircuitBreaker{
		state:            CBClosed,
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		halfOpenMax:      halfOpenMax,
	}
}

// Allow reports whether a request should be permitted through the circuit.
// In the Open state, it transitions to HalfOpen once the reset timeout has elapsed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CBClosed:
		return true
	case CBOpen:
		if time.Since(cb.lastFailureTime) >= cb.resetTimeout {
			cb.state = CBHalfOpen
			cb.halfOpenSuccesses = 0
			return true
		}
		return false
	case CBHalfOpen:
		return true
	default:
		return true
	}
}

// RecordSuccess records a successful request. In HalfOpen state, after enough
// successes the circuit transitions back to Closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0

	if cb.state == CBHalfOpen {
		cb.halfOpenSuccesses++
		if cb.halfOpenSuccesses >= cb.halfOpenMax {
			cb.state = CBClosed
		}
	}
}

// RecordFailure records a failed request. In Closed state, transitions to Open
// after the failure threshold is reached. In HalfOpen state, transitions
// directly back to Open.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures++
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case CBClosed:
		if cb.consecutiveFailures >= cb.failureThreshold {
			cb.state = CBOpen
		}
	case CBHalfOpen:
		cb.state = CBOpen
		cb.halfOpenSuccesses = 0
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CBState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// CircuitBreakerRegistry is a thread-safe registry of per-provider circuit breakers.
// Breakers are created lazily on first access via Get.
type CircuitBreakerRegistry struct {
	mu sync.Mutex

	breakers         map[string]*CircuitBreaker
	failureThreshold int
	resetTimeout     time.Duration
	halfOpenMax      int
}

// NewCircuitBreakerRegistry creates a new registry with the given default parameters.
func NewCircuitBreakerRegistry(failureThreshold int, resetTimeout time.Duration, halfOpenMax int) *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers:         make(map[string]*CircuitBreaker),
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		halfOpenMax:      halfOpenMax,
	}
}

// Get returns the circuit breaker for the given provider, creating one if necessary.
func (r *CircuitBreakerRegistry) Get(provider string) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()

	cb, ok := r.breakers[provider]
	if !ok {
		cb = NewCircuitBreaker(r.failureThreshold, r.resetTimeout, r.halfOpenMax)
		r.breakers[provider] = cb
	}
	return cb
}
