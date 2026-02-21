package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock middleware
// ---------------------------------------------------------------------------

type mockMiddleware struct {
	name      string
	enabled   bool
	onReq     func(ctx context.Context, req *Request) (*Request, error)
	onResp    func(ctx context.Context, req *Request, resp *Response) (*Response, error)
	reqOrder  *[]string // append name when ProcessRequest is called
	respOrder *[]string // append name when ProcessResponse is called
}

func (m *mockMiddleware) Name() string  { return m.name }
func (m *mockMiddleware) Enabled() bool { return m.enabled }

func (m *mockMiddleware) ProcessRequest(ctx context.Context, req *Request) (*Request, error) {
	if m.reqOrder != nil {
		*m.reqOrder = append(*m.reqOrder, m.name)
	}
	if m.onReq != nil {
		return m.onReq(ctx, req)
	}
	return req, nil
}

func (m *mockMiddleware) ProcessResponse(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	if m.respOrder != nil {
		*m.respOrder = append(*m.respOrder, m.name)
	}
	if m.onResp != nil {
		return m.onResp(ctx, req, resp)
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newRequest() *Request {
	return &Request{
		ID:       "test-req",
		Format:   FormatOpenAI,
		Model:    "gpt-4",
		Metadata: make(map[string]interface{}),
		Flags:    make(map[string]bool),
	}
}

func newResponse() *Response {
	return &Response{
		RequestID:  "test-req",
		StatusCode: 200,
		Body:       []byte(`{"ok":true}`),
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestMiddlewareExecutionOrder verifies that the request phase runs middlewares
// in forward order and the response phase runs them in reverse order.
func TestMiddlewareExecutionOrder(t *testing.T) {
	reqOrder := make([]string, 0, 3)
	respOrder := make([]string, 0, 3)

	mw1 := &mockMiddleware{name: "first", enabled: true, reqOrder: &reqOrder, respOrder: &respOrder}
	mw2 := &mockMiddleware{name: "second", enabled: true, reqOrder: &reqOrder, respOrder: &respOrder}
	mw3 := &mockMiddleware{name: "third", enabled: true, reqOrder: &reqOrder, respOrder: &respOrder}

	chain := NewChain(mw1, mw2, mw3)
	ctx := context.Background()
	req := newRequest()

	// --- Request phase ---
	outReq, cached, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}
	if cached != nil {
		t.Fatal("expected no cached response")
	}
	if outReq == nil {
		t.Fatal("expected non-nil request")
	}

	expectedReqOrder := []string{"first", "second", "third"}
	if len(reqOrder) != len(expectedReqOrder) {
		t.Fatalf("request order length: got %d, want %d", len(reqOrder), len(expectedReqOrder))
	}
	for i, name := range expectedReqOrder {
		if reqOrder[i] != name {
			t.Errorf("request order[%d]: got %q, want %q", i, reqOrder[i], name)
		}
	}

	// --- Response phase ---
	resp := newResponse()
	outResp, err := chain.ProcessResponse(ctx, req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse returned unexpected error: %v", err)
	}
	if outResp == nil {
		t.Fatal("expected non-nil response")
	}

	expectedRespOrder := []string{"third", "second", "first"}
	if len(respOrder) != len(expectedRespOrder) {
		t.Fatalf("response order length: got %d, want %d", len(respOrder), len(expectedRespOrder))
	}
	for i, name := range expectedRespOrder {
		if respOrder[i] != name {
			t.Errorf("response order[%d]: got %q, want %q", i, respOrder[i], name)
		}
	}
}

// TestCacheHitShortCircuitViaMetadata verifies that when a middleware sets
// req.Flags["cache_hit"] = true and stores a *CachedResponse in
// req.Metadata["cached_response"], the chain returns the cached response and
// does not execute subsequent middlewares.
func TestCacheHitShortCircuitViaMetadata(t *testing.T) {
	reqOrder := make([]string, 0, 3)

	cachedBody := []byte(`{"cached":true}`)
	cr := &CachedResponse{
		Body:        cachedBody,
		StatusCode:  200,
		ContentType: "application/json",
	}

	cacheMW := &mockMiddleware{
		name:     "cache",
		enabled:  true,
		reqOrder: &reqOrder,
		onReq: func(_ context.Context, req *Request) (*Request, error) {
			req.Flags["cache_hit"] = true
			req.Metadata["cached_response"] = cr
			return req, nil
		},
	}

	neverReached := &mockMiddleware{
		name:     "never",
		enabled:  true,
		reqOrder: &reqOrder,
	}

	chain := NewChain(cacheMW, neverReached)
	ctx := context.Background()
	req := newRequest()

	outReq, cached, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}
	if cached == nil {
		t.Fatal("expected a cached response, got nil")
	}
	if string(cached.Body) != string(cachedBody) {
		t.Errorf("cached body: got %q, want %q", cached.Body, cachedBody)
	}
	if cached.StatusCode != 200 {
		t.Errorf("cached status: got %d, want 200", cached.StatusCode)
	}
	if outReq == nil {
		t.Fatal("expected non-nil request after cache hit")
	}

	// "never" middleware must not have been reached.
	if len(reqOrder) != 1 {
		t.Fatalf("expected 1 middleware invoked, got %d: %v", len(reqOrder), reqOrder)
	}
	if reqOrder[0] != "cache" {
		t.Errorf("only middleware invoked should be 'cache', got %q", reqOrder[0])
	}
}

// TestCacheHitShortCircuitViaContext verifies the fallback path where
// cache_hit is flagged but the CachedResponse is stored in the context
// (via WithCachedResponse) rather than req.Metadata.
func TestCacheHitShortCircuitViaContext(t *testing.T) {
	cr := &CachedResponse{
		Body:        []byte(`{"ctx_cached":true}`),
		StatusCode:  200,
		ContentType: "application/json",
	}

	cacheMW := &mockMiddleware{
		name:    "ctx-cache",
		enabled: true,
		onReq: func(ctx context.Context, req *Request) (*Request, error) {
			req.Flags["cache_hit"] = true
			// Do NOT store in Metadata — store in context instead.
			// Note: the middleware cannot modify the context directly, so we
			// need to inject it before calling ProcessRequest.
			return req, nil
		},
	}

	chain := NewChain(cacheMW)

	// Pre-inject the CachedResponse into the context.
	ctx := WithCachedResponse(context.Background(), cr)
	req := newRequest()

	_, cached, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}
	if cached == nil {
		t.Fatal("expected cached response from context, got nil")
	}
	if string(cached.Body) != string(cr.Body) {
		t.Errorf("cached body: got %q, want %q", cached.Body, cr.Body)
	}
}

// TestCacheHitMissingCachedResponse verifies that when cache_hit is flagged
// but no CachedResponse is available anywhere, an error is returned.
func TestCacheHitMissingCachedResponse(t *testing.T) {
	cacheMW := &mockMiddleware{
		name:    "bad-cache",
		enabled: true,
		onReq: func(_ context.Context, req *Request) (*Request, error) {
			req.Flags["cache_hit"] = true
			// No CachedResponse stored anywhere.
			return req, nil
		},
	}

	chain := NewChain(cacheMW)
	ctx := context.Background()
	req := newRequest()

	_, _, err := chain.ProcessRequest(ctx, req)
	if err == nil {
		t.Fatal("expected error when cache_hit is set but no CachedResponse exists")
	}
}

// TestRequestErrorPropagation verifies that when a middleware returns an error
// during the request phase, the chain stops and returns that error wrapped with
// the middleware name.
func TestRequestErrorPropagation(t *testing.T) {
	reqOrder := make([]string, 0, 3)
	sentinel := errors.New("something went wrong")

	mw1 := &mockMiddleware{name: "ok", enabled: true, reqOrder: &reqOrder}
	mw2 := &mockMiddleware{
		name:     "fail",
		enabled:  true,
		reqOrder: &reqOrder,
		onReq: func(_ context.Context, req *Request) (*Request, error) {
			return nil, sentinel
		},
	}
	mw3 := &mockMiddleware{name: "after-fail", enabled: true, reqOrder: &reqOrder}

	chain := NewChain(mw1, mw2, mw3)
	ctx := context.Background()
	req := newRequest()

	_, _, err := chain.ProcessRequest(ctx, req)
	if err == nil {
		t.Fatal("expected error from failing middleware")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain should contain sentinel: got %v", err)
	}

	// "after-fail" must not have been reached.
	for _, name := range reqOrder {
		if name == "after-fail" {
			t.Error("middleware after the failing one should not have been invoked")
		}
	}
}

// TestResponseErrorPropagation verifies that when a middleware returns an error
// during the response phase, the chain stops and returns that error.
func TestResponseErrorPropagation(t *testing.T) {
	respOrder := make([]string, 0, 3)
	sentinel := errors.New("response error")

	mw1 := &mockMiddleware{name: "first", enabled: true, respOrder: &respOrder}
	mw2 := &mockMiddleware{
		name:      "fail-resp",
		enabled:   true,
		respOrder: &respOrder,
		onResp: func(_ context.Context, _ *Request, resp *Response) (*Response, error) {
			return nil, sentinel
		},
	}
	mw3 := &mockMiddleware{name: "third", enabled: true, respOrder: &respOrder}

	chain := NewChain(mw1, mw2, mw3)
	ctx := context.Background()
	req := newRequest()
	resp := newResponse()

	// Response phase iterates in reverse: third -> fail-resp -> first.
	_, err := chain.ProcessResponse(ctx, req, resp)
	if err == nil {
		t.Fatal("expected error from failing response middleware")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain should contain sentinel: got %v", err)
	}

	// "third" runs first (reverse order), then "fail-resp" errors, so "first"
	// should never be invoked.
	for _, name := range respOrder {
		if name == "first" {
			t.Error("middleware 'first' should not have been reached after 'fail-resp' errored")
		}
	}
}

// TestDisabledMiddlewareIsSkipped verifies that middlewares with Enabled() == false
// are not invoked in either phase.
func TestDisabledMiddlewareIsSkipped(t *testing.T) {
	reqOrder := make([]string, 0, 3)
	respOrder := make([]string, 0, 3)

	mw1 := &mockMiddleware{name: "enabled-1", enabled: true, reqOrder: &reqOrder, respOrder: &respOrder}
	mw2 := &mockMiddleware{name: "disabled", enabled: false, reqOrder: &reqOrder, respOrder: &respOrder}
	mw3 := &mockMiddleware{name: "enabled-2", enabled: true, reqOrder: &reqOrder, respOrder: &respOrder}

	chain := NewChain(mw1, mw2, mw3)
	ctx := context.Background()
	req := newRequest()

	outReq, _, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}

	resp := newResponse()
	_, err = chain.ProcessResponse(ctx, outReq, resp)
	if err != nil {
		t.Fatalf("ProcessResponse returned unexpected error: %v", err)
	}

	// Request phase: only enabled middlewares in forward order.
	expectedReqOrder := []string{"enabled-1", "enabled-2"}
	if len(reqOrder) != len(expectedReqOrder) {
		t.Fatalf("request order length: got %d, want %d (%v)", len(reqOrder), len(expectedReqOrder), reqOrder)
	}
	for i, name := range expectedReqOrder {
		if reqOrder[i] != name {
			t.Errorf("request order[%d]: got %q, want %q", i, reqOrder[i], name)
		}
	}

	// Response phase: only enabled middlewares in reverse order.
	expectedRespOrder := []string{"enabled-2", "enabled-1"}
	if len(respOrder) != len(expectedRespOrder) {
		t.Fatalf("response order length: got %d, want %d (%v)", len(respOrder), len(expectedRespOrder), respOrder)
	}
	for i, name := range expectedRespOrder {
		if respOrder[i] != name {
			t.Errorf("response order[%d]: got %q, want %q", i, respOrder[i], name)
		}
	}
}

// TestMiddlewareTimingIsRecorded verifies that the chain records per-middleware
// timing data both in the context (via GetMiddlewareTimings) and in the
// chain's own Timings() snapshot.
func TestMiddlewareTimingIsRecorded(t *testing.T) {
	slowMW := &mockMiddleware{
		name:    "slow",
		enabled: true,
		onReq: func(_ context.Context, req *Request) (*Request, error) {
			time.Sleep(10 * time.Millisecond)
			return req, nil
		},
		onResp: func(_ context.Context, _ *Request, resp *Response) (*Response, error) {
			time.Sleep(10 * time.Millisecond)
			return resp, nil
		},
	}

	chain := NewChain(slowMW)
	ctx := context.Background()
	req := newRequest()

	// --- Request phase ---
	outReq, _, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}

	// Check chain-level timings.
	timings := chain.Timings()
	reqDuration, ok := timings["slow"]
	if !ok {
		t.Fatal("expected timing entry for 'slow' in chain.Timings()")
	}
	if reqDuration < 10*time.Millisecond {
		t.Errorf("request timing for 'slow': got %v, want >= 10ms", reqDuration)
	}

	// --- Response phase ---
	resp := newResponse()

	// Use the context produced by ProcessRequest to share timings. Since
	// ProcessRequest returns a modified request (not context), we get the
	// timings through the chain's own Timings() method and by passing a
	// fresh context (ProcessResponse creates its own timing map if needed).
	_, err = chain.ProcessResponse(ctx, outReq, resp)
	if err != nil {
		t.Fatalf("ProcessResponse returned unexpected error: %v", err)
	}

	timings = chain.Timings()
	respDuration, ok := timings["slow.response"]
	if !ok {
		t.Fatal("expected timing entry for 'slow.response' in chain.Timings()")
	}
	if respDuration < 10*time.Millisecond {
		t.Errorf("response timing for 'slow.response': got %v, want >= 10ms", respDuration)
	}
}

// TestContextMiddlewareTimings verifies that ProcessRequest injects a timing
// map into the context (via WithMiddlewareTimings) that can be read back with
// GetMiddlewareTimings.
func TestContextMiddlewareTimings(t *testing.T) {
	var capturedCtx context.Context

	spy := &mockMiddleware{
		name:    "spy",
		enabled: true,
		onReq: func(ctx context.Context, req *Request) (*Request, error) {
			// The chain injects the timing map before calling middlewares,
			// so we should be able to read it here.
			capturedCtx = ctx
			return req, nil
		},
	}

	chain := NewChain(spy)
	ctx := context.Background()
	req := newRequest()

	_, _, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}

	if capturedCtx == nil {
		t.Fatal("expected spy middleware to capture context")
	}

	timings, ok := GetMiddlewareTimings(capturedCtx)
	if !ok {
		t.Fatal("expected middleware timings in context")
	}
	if timings == nil {
		t.Fatal("timings map should not be nil")
	}
}

// TestNilFlagsMapInitialized verifies that if req.Flags is nil, ProcessRequest
// initializes it before any middleware runs.
func TestNilFlagsMapInitialized(t *testing.T) {
	var flagsSeen bool

	mw := &mockMiddleware{
		name:    "flag-checker",
		enabled: true,
		onReq: func(_ context.Context, req *Request) (*Request, error) {
			if req.Flags != nil {
				flagsSeen = true
			}
			return req, nil
		},
	}

	chain := NewChain(mw)
	req := &Request{
		ID:       "no-flags",
		Metadata: make(map[string]interface{}),
		// Flags intentionally left nil.
	}

	_, _, err := chain.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}
	if !flagsSeen {
		t.Error("expected Flags map to be initialized before middleware runs")
	}
}

// TestEmptyChain verifies that a chain with no middlewares passes the request
// through unchanged and returns no cached response.
func TestEmptyChain(t *testing.T) {
	chain := NewChain()
	ctx := context.Background()
	req := newRequest()

	outReq, cached, err := chain.ProcessRequest(ctx, req)
	if err != nil {
		t.Fatalf("ProcessRequest returned unexpected error: %v", err)
	}
	if cached != nil {
		t.Fatal("expected no cached response from empty chain")
	}
	if outReq != req {
		t.Error("expected same request object to pass through")
	}

	resp := newResponse()
	outResp, err := chain.ProcessResponse(ctx, req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse returned unexpected error: %v", err)
	}
	if outResp != resp {
		t.Error("expected same response object to pass through")
	}
}

// TestProcessRequest_PanicRecovery verifies that a panicking middleware in the
// request phase is recovered and converted into an error rather than crashing.
func TestProcessRequest_PanicRecovery(t *testing.T) {
	panicMW := &mockMiddleware{
		name:    "panicker",
		enabled: true,
		onReq: func(_ context.Context, req *Request) (*Request, error) {
			panic("request boom")
		},
	}

	chain := NewChain(panicMW)
	ctx := context.Background()
	req := newRequest()

	_, _, err := chain.ProcessRequest(ctx, req)
	if err == nil {
		t.Fatal("expected error from panicking middleware")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error should mention panic: got %v", err)
	}
	if !strings.Contains(err.Error(), "request boom") {
		t.Errorf("error should contain panic value: got %v", err)
	}
}

// TestProcessResponse_PanicRecovery verifies that a panicking middleware in the
// response phase is recovered and converted into an error rather than crashing.
func TestProcessResponse_PanicRecovery(t *testing.T) {
	panicMW := &mockMiddleware{
		name:    "panicker",
		enabled: true,
		onResp: func(_ context.Context, _ *Request, resp *Response) (*Response, error) {
			panic("response boom")
		},
	}

	chain := NewChain(panicMW)
	ctx := context.Background()
	req := newRequest()
	resp := newResponse()

	_, err := chain.ProcessResponse(ctx, req, resp)
	if err == nil {
		t.Fatal("expected error from panicking middleware")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error should mention panic: got %v", err)
	}
	if !strings.Contains(err.Error(), "response boom") {
		t.Errorf("error should contain panic value: got %v", err)
	}
}

// TestMiddlewaresAccessor verifies that Chain.Middlewares() returns a copy of
// the internal middleware slice.
func TestMiddlewaresAccessor(t *testing.T) {
	mw1 := &mockMiddleware{name: "a", enabled: true}
	mw2 := &mockMiddleware{name: "b", enabled: true}

	chain := NewChain(mw1, mw2)
	mws := chain.Middlewares()

	if len(mws) != 2 {
		t.Fatalf("Middlewares() length: got %d, want 2", len(mws))
	}
	if mws[0].Name() != "a" || mws[1].Name() != "b" {
		t.Errorf("Middlewares() names: got [%q, %q], want [a, b]", mws[0].Name(), mws[1].Name())
	}

	// Mutating the returned slice should not affect the chain.
	mws[0] = nil
	original := chain.Middlewares()
	if original[0] == nil {
		t.Error("mutating returned Middlewares() slice should not affect the chain")
	}
}
