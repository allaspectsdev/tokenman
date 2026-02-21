package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/allaspects/tokenman/internal/tracing"
)

// recoverMiddleware runs fn inside a deferred recover so that a panicking
// middleware does not crash the entire process. If a panic is caught it is
// converted into an error that includes the middleware name.
func recoverMiddleware(name string, fn func() error) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("middleware %s: panic: %v", name, r)
		}
	}()
	return fn()
}

// Chain executes an ordered sequence of Middleware.
// Requests flow through middlewares in order; responses flow in reverse order.
type Chain struct {
	middlewares []Middleware

	mu      sync.RWMutex
	timings map[string]time.Duration // latest per-middleware execution times
}

// NewChain creates a new Chain from the given middlewares.
// Middlewares are executed in the order provided for requests
// and in reverse order for responses.
func NewChain(middlewares ...Middleware) *Chain {
	return &Chain{
		middlewares: middlewares,
		timings:     make(map[string]time.Duration),
	}
}

// ProcessRequest runs each enabled middleware's ProcessRequest in order.
// If any middleware signals a cache hit (by setting req.Flags["cache_hit"] = true),
// the pipeline short-circuits and returns the CachedResponse stored in the context.
// The returned context carries per-middleware timing information.
func (c *Chain) ProcessRequest(ctx context.Context, req *Request) (*Request, *CachedResponse, error) {
	// Ensure the request has an initialized Flags map.
	if req.Flags == nil {
		req.Flags = make(map[string]bool)
	}

	// Prepare a timing map and inject it into the context so middlewares
	// further down the chain (or callers) can inspect latency data.
	timings := make(map[string]time.Duration, len(c.middlewares))
	ctx = WithMiddlewareTimings(ctx, timings)

	for _, mw := range c.middlewares {
		if !mw.Enabled() {
			continue
		}

		name := mw.Name()
		mwCtx, mwSpan := tracing.StartMiddlewareSpan(ctx, name, "request")
		start := time.Now()

		var innerReq *Request
		err := recoverMiddleware(name, func() error {
			var mwErr error
			innerReq, mwErr = mw.ProcessRequest(mwCtx, req)
			return mwErr
		})
		elapsed := time.Since(start)

		// Record timing regardless of success or failure.
		timings[name] = elapsed
		c.recordTiming(name, elapsed)

		if err != nil {
			tracing.RecordError(mwCtx, err)
			mwSpan.End()
			return nil, nil, fmt.Errorf("middleware %s: request processing failed: %w", name, err)
		}
		mwSpan.End()

		req = innerReq
		if req == nil {
			return nil, nil, fmt.Errorf("middleware %s: returned nil request without error", name)
		}

		// Check for cache-hit short-circuit.
		if req.Flags["cache_hit"] {
			// Check metadata first (set by cache middleware), then fall back to context.
			if cr, ok := req.Metadata["cached_response"].(*CachedResponse); ok && cr != nil {
				return req, cr, nil
			}
			cached, ok := GetCachedResponse(ctx)
			if !ok || cached == nil {
				return nil, nil, fmt.Errorf("middleware %s: flagged cache_hit but no CachedResponse found", name)
			}
			return req, cached, nil
		}
	}

	return req, nil, nil
}

// ProcessResponse runs each enabled middleware's ProcessResponse in reverse order.
func (c *Chain) ProcessResponse(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	// Retrieve or create the timing map.
	timings, ok := GetMiddlewareTimings(ctx)
	if !ok {
		timings = make(map[string]time.Duration, len(c.middlewares))
		ctx = WithMiddlewareTimings(ctx, timings)
	}

	// Build a list of enabled middlewares to iterate in reverse.
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		mw := c.middlewares[i]
		if !mw.Enabled() {
			continue
		}

		name := mw.Name()
		mwCtx, mwSpan := tracing.StartMiddlewareSpan(ctx, name, "response")
		start := time.Now()

		var innerResp *Response
		err := recoverMiddleware(name, func() error {
			var mwErr error
			innerResp, mwErr = mw.ProcessResponse(mwCtx, req, resp)
			return mwErr
		})
		elapsed := time.Since(start)

		// Accumulate response-phase timing onto any existing request-phase timing.
		timings[name+".response"] = elapsed
		c.recordTiming(name+".response", elapsed)

		if err != nil {
			tracing.RecordError(mwCtx, err)
			mwSpan.End()
			return nil, fmt.Errorf("middleware %s: response processing failed: %w", name, err)
		}
		mwSpan.End()

		resp = innerResp
		if resp == nil {
			return nil, fmt.Errorf("middleware %s: returned nil response without error", name)
		}
	}

	return resp, nil
}

// Timings returns a snapshot of the latest per-middleware execution times.
func (c *Chain) Timings() map[string]time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := make(map[string]time.Duration, len(c.timings))
	for k, v := range c.timings {
		snapshot[k] = v
	}
	return snapshot
}

// Middlewares returns the ordered list of middlewares in the chain.
func (c *Chain) Middlewares() []Middleware {
	result := make([]Middleware, len(c.middlewares))
	copy(result, c.middlewares)
	return result
}

// recordTiming stores the latest execution time for a middleware phase.
func (c *Chain) recordTiming(name string, d time.Duration) {
	c.mu.Lock()
	c.timings[name] = d
	c.mu.Unlock()
}
