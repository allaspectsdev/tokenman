package security

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/allaspectsdev/tokenman/internal/config"
	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// RateLimitError is returned when a provider's rate limit is exceeded. It carries
// structured data that the HTTP handler can serialize to a JSON response with
// HTTP 429 status.
type RateLimitError struct {
	Provider   string  `json:"provider"`
	Rate       float64 `json:"rate"`
	RetryAfter float64 `json:"retry_after"`
	Message    string  `json:"message"`
}

// Error implements the error interface.
func (e *RateLimitError) Error() string {
	return e.Message
}

// ToJSON serializes the rate limit error to a JSON body suitable for an HTTP response.
func (e *RateLimitError) ToJSON() []byte {
	body := map[string]interface{}{
		"error": map[string]interface{}{
			"type":        "rate_limit_error",
			"message":     e.Message,
			"provider":    e.Provider,
			"retry_after": e.RetryAfter,
		},
	}
	data, _ := json.Marshal(body)
	return data
}

// tokenBucket implements a token-bucket rate limiter for a single provider.
type tokenBucket struct {
	rate       float64 // tokens per second
	burst      int     // max burst size
	tokens     float64
	lastRefill time.Time
	mu         sync.Mutex
}

// newTokenBucket creates a token bucket with the given rate and burst.
func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{
		rate:       rate,
		burst:      burst,
		tokens:     float64(burst),
		lastRefill: time.Now(),
	}
}

// allow attempts to consume one token from the bucket. It returns true if the
// request is allowed, or false if the bucket is empty (rate limited).
func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	// Refill tokens based on elapsed time.
	tb.tokens += elapsed * tb.rate
	if tb.tokens > float64(tb.burst) {
		tb.tokens = float64(tb.burst)
	}

	if tb.tokens < 1.0 {
		return false
	}

	tb.tokens -= 1.0
	return true
}

// RateLimitMiddleware is a pipeline.Middleware that enforces per-provider
// token-bucket rate limits.
type RateLimitMiddleware struct {
	limiters    map[string]*tokenBucket // keyed by provider name
	defaultRate float64
	defaultBurst int
	enabled     bool
	mu          sync.RWMutex
}

// Compile-time assertion that RateLimitMiddleware implements pipeline.Middleware.
var _ pipeline.Middleware = (*RateLimitMiddleware)(nil)

// NewRateLimitMiddleware creates a new RateLimitMiddleware with per-provider
// limits and a default fallback rate.
func NewRateLimitMiddleware(defaultRate float64, defaultBurst int, providerLimits map[string]config.ProviderRateLimit, enabled bool) *RateLimitMiddleware {
	limiters := make(map[string]*tokenBucket, len(providerLimits))
	for name, pl := range providerLimits {
		limiters[name] = newTokenBucket(pl.Rate, pl.Burst)
	}

	return &RateLimitMiddleware{
		limiters:     limiters,
		defaultRate:  defaultRate,
		defaultBurst: defaultBurst,
		enabled:      enabled,
	}
}

// Name returns the middleware name.
func (rl *RateLimitMiddleware) Name() string {
	return "ratelimit"
}

// Enabled reports whether this middleware is active.
func (rl *RateLimitMiddleware) Enabled() bool {
	return rl.enabled
}

// ProcessRequest checks the resolved provider against its rate limit.
// If the rate limit is exceeded, it returns an error containing "rate_limited".
func (rl *RateLimitMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	provider := rl.resolveProvider(req)
	if provider == "" {
		return req, nil
	}

	bucket := rl.getOrCreateBucket(provider)
	if !bucket.allow() {
		retryAfter := 1.0 / bucket.rate
		if retryAfter < 0.1 {
			retryAfter = 0.1
		}
		return nil, &RateLimitError{
			Provider:   provider,
			Rate:       bucket.rate,
			RetryAfter: retryAfter,
			Message:    fmt.Sprintf("rate_limited: provider %q has exceeded its rate limit of %.1f req/s", provider, bucket.rate),
		}
	}

	return req, nil
}

// ProcessResponse is a no-op for rate limiting.
func (rl *RateLimitMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// resolveProvider determines the provider name from the request metadata
// or by inferring it from the model name.
func (rl *RateLimitMiddleware) resolveProvider(req *pipeline.Request) string {
	// Check metadata for explicit provider.
	if req.Metadata != nil {
		if p, ok := req.Metadata["provider"]; ok {
			if ps, ok := p.(string); ok && ps != "" {
				return ps
			}
		}
	}

	// Infer provider from model name.
	model := req.Model
	if model == "" {
		return ""
	}

	// Common prefixes.
	switch {
	case len(model) >= 6 && model[:6] == "claude":
		return "anthropic"
	case len(model) >= 3 && model[:3] == "gpt":
		return "openai"
	case len(model) >= 2 && model[:2] == "o1":
		return "openai"
	case len(model) >= 2 && model[:2] == "o3":
		return "openai"
	default:
		return "default"
	}
}

// Reconfigure replaces the default rate/burst and rebuilds all token buckets
// with the new settings. This is called when the config is hot-reloaded.
func (rl *RateLimitMiddleware) Reconfigure(defaultRate float64, defaultBurst int, providerLimits map[string]config.ProviderRateLimit) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.defaultRate = defaultRate
	rl.defaultBurst = defaultBurst

	// Rebuild all buckets.
	newLimiters := make(map[string]*tokenBucket, len(providerLimits))
	for name, pl := range providerLimits {
		newLimiters[name] = newTokenBucket(pl.Rate, pl.Burst)
	}
	rl.limiters = newLimiters
}

// getOrCreateBucket returns the token bucket for a provider, creating one
// with default settings if it does not exist yet.
func (rl *RateLimitMiddleware) getOrCreateBucket(provider string) *tokenBucket {
	rl.mu.RLock()
	bucket, ok := rl.limiters[provider]
	rl.mu.RUnlock()

	if ok {
		return bucket
	}

	// Create a new bucket with default rate/burst.
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock.
	if bucket, ok = rl.limiters[provider]; ok {
		return bucket
	}

	bucket = newTokenBucket(rl.defaultRate, rl.defaultBurst)
	rl.limiters[provider] = bucket
	return bucket
}
