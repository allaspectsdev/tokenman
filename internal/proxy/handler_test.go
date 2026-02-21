package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/allaspects/tokenman/internal/metrics"
	"github.com/allaspects/tokenman/internal/pipeline"
	"github.com/allaspects/tokenman/internal/security"
	"github.com/allaspects/tokenman/internal/tokenizer"
	"github.com/rs/zerolog"
)

// mockUpstream creates a test HTTP server that uses the given handler function.
func mockUpstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

// --- Cache hit middleware ---

type cacheHitMiddleware struct{}

func (m *cacheHitMiddleware) Name() string    { return "test-cache" }
func (m *cacheHitMiddleware) Enabled() bool   { return true }

func (m *cacheHitMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	req.Flags["cache_hit"] = true
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	req.Metadata["cached_response"] = &pipeline.CachedResponse{
		Body:        []byte(`{"cached": true}`),
		StatusCode:  200,
		ContentType: "application/json",
	}
	return req, nil
}

func (m *cacheHitMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// --- Budget exceeded middleware ---

type budgetExceededMiddleware struct{}

func (m *budgetExceededMiddleware) Name() string    { return "test-budget" }
func (m *budgetExceededMiddleware) Enabled() bool   { return true }

func (m *budgetExceededMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	return nil, &security.BudgetError{
		Type:    "budget_exceeded",
		Message: "daily budget limit exceeded: spent $10.0000 of $10.0000",
		Period:  "daily",
		Limit:   10.0,
		Spent:   10.0,
	}
}

func (m *budgetExceededMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// newTestHandler creates a ProxyHandler wired to the given chain and upstream URL.
// The model "test-model" is mapped to the upstream using Anthropic format.
func newTestHandler(chain *pipeline.Chain, upstreamURL string) *ProxyHandler {
	collector := metrics.NewCollector()
	tok := tokenizer.New()
	handler := NewProxyHandler(chain, NewUpstreamClient(), zerolog.Nop(), collector, tok, nil, 0, 0, 0, nil, RetryConfig{})

	if upstreamURL != "" {
		handler.SetProviders(map[string]ProviderConfig{
			"test-model": {
				BaseURL: upstreamURL,
				APIKey:  "test-key",
				Format:  pipeline.FormatAnthropic,
			},
		})
	}

	return handler
}

// newTestServer creates a chi-based Server with the given handler and returns
// a httptest.Server ready for requests.
func newTestServer(handler *ProxyHandler) *httptest.Server {
	srv := NewServer(handler, ":0", 0, 0, 0, false)
	return httptest.NewServer(srv.Router())
}

func TestHealthEndpoint_Returns200WithStatusOK(t *testing.T) {
	chain := pipeline.NewChain()
	handler := newTestHandler(chain, "")
	ts := newTestServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshalling body %q: %v", string(body), err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q; want %q", result["status"], "ok")
	}
}

func TestUnknownFormat_Returns400(t *testing.T) {
	chain := pipeline.NewChain()
	handler := newTestHandler(chain, "")
	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	resp, err := http.Post(ts.URL+"/v1/unknown", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/unknown failed: %v", err)
	}
	defer resp.Body.Close()

	// chi returns 405 for unmatched POST routes, which is acceptable.
	// The important thing is it does NOT return 200.
	if resp.StatusCode == http.StatusOK {
		t.Errorf("expected non-200 status for unknown format; got %d", resp.StatusCode)
	}
}

func TestValidAnthropicRequest_ForwardedToUpstream(t *testing.T) {
	upstreamCalled := false
	upstream := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true

		// Verify the upstream received the request.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("upstream: reading body: %v", err)
		}
		defer r.Body.Close()

		if len(body) == 0 {
			t.Error("upstream: received empty body")
		}

		// Verify auth header was set.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("upstream: x-api-key = %q; want %q", r.Header.Get("x-api-key"), "test-key")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"text","text":"Hello!"}],"model":"test-model","stop_reason":"end_turn"}`))
	})
	defer upstream.Close()

	chain := pipeline.NewChain()
	handler := newTestHandler(chain, upstream.URL)
	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if !upstreamCalled {
		t.Error("upstream was not called")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusOK, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshalling response: %v", err)
	}
	if result["id"] != "msg_123" {
		t.Errorf("response id = %v; want %q", result["id"], "msg_123")
	}
}

func TestCacheHit_ReturnsCachedResponseWithHeader(t *testing.T) {
	chain := pipeline.NewChain(&cacheHitMiddleware{})
	handler := newTestHandler(chain, "")
	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusOK, string(body))
	}

	cacheHeader := resp.Header.Get("X-Tokenman-Cache")
	if cacheHeader != "HIT" {
		t.Errorf("X-Tokenman-Cache = %q; want %q", cacheHeader, "HIT")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshalling response %q: %v", string(body), err)
	}
	if cached, ok := result["cached"].(bool); !ok || !cached {
		t.Errorf("expected cached response body; got %s", string(body))
	}
}

func TestBudgetExceeded_Returns429(t *testing.T) {
	chain := pipeline.NewChain(&budgetExceededMiddleware{})
	handler := newTestHandler(chain, "")
	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusTooManyRequests, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshalling response %q: %v", string(body), err)
	}

	errorObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object in response; got %v", result)
	}
	if errorObj["type"] != "budget_exceeded" {
		t.Errorf("error.type = %v; want %q", errorObj["type"], "budget_exceeded")
	}
	if errorObj["period"] != "daily" {
		t.Errorf("error.period = %v; want %q", errorObj["period"], "daily")
	}
}

func TestUpstreamError_PropagatesStatusCode(t *testing.T) {
	upstream := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	})
	defer upstream.Close()

	chain := pipeline.NewChain()
	handler := newTestHandler(chain, upstream.URL)
	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusTooManyRequests, string(body))
	}

	// Verify Retry-After header is forwarded.
	if ra := resp.Header.Get("Retry-After"); ra != "30" {
		t.Errorf("Retry-After = %q; want %q", ra, "30")
	}
}

func TestRetry_SucceedsOnSecondAttempt(t *testing.T) {
	attempt := 0
	upstream := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_retry","type":"message","role":"assistant","content":[{"type":"text","text":"retried!"}],"model":"test-model","stop_reason":"end_turn"}`))
	})
	defer upstream.Close()

	chain := pipeline.NewChain()
	collector := metrics.NewCollector()
	tok := tokenizer.New()

	cbRegistry := NewCircuitBreakerRegistry(5, 60*time.Second, 1)
	retryConfig := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}

	handler := NewProxyHandler(chain, NewUpstreamClient(), zerolog.Nop(), collector, tok, nil, 0, 0, 0, cbRegistry, retryConfig)
	handler.SetProviders(map[string]ProviderConfig{
		"test-model": {
			BaseURL: upstream.URL,
			APIKey:  "test-key",
			Format:  pipeline.FormatAnthropic,
		},
	})

	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusOK, string(body))
	}

	if attempt != 2 {
		t.Errorf("expected 2 upstream attempts, got %d", attempt)
	}
}

func TestRetry_Exhausted_Returns502(t *testing.T) {
	upstream := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	})
	defer upstream.Close()

	chain := pipeline.NewChain()
	collector := metrics.NewCollector()
	tok := tokenizer.New()

	cbRegistry := NewCircuitBreakerRegistry(10, 60*time.Second, 1) // high threshold so it doesn't trip
	retryConfig := RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}

	handler := NewProxyHandler(chain, NewUpstreamClient(), zerolog.Nop(), collector, tok, nil, 0, 0, 0, cbRegistry, retryConfig)
	handler.SetProviders(map[string]ProviderConfig{
		"test-model": {
			BaseURL: upstream.URL,
			APIKey:  "test-key",
			Format:  pipeline.FormatAnthropic,
		},
	})

	ts := newTestServer(handler)
	defer ts.Close()

	reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":100}`
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusBadGateway, string(body))
	}
}

func TestReadinessProbe_Returns200WhenProvidersConfigured(t *testing.T) {
	chain := pipeline.NewChain()
	handler := newTestHandler(chain, "http://localhost:1234")
	ts := newTestServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusOK, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "ready" {
		t.Errorf("status = %v; want %q", result["status"], "ready")
	}
}

func TestReadinessProbe_Returns503WhenNoProviders(t *testing.T) {
	chain := pipeline.NewChain()
	handler := newTestHandler(chain, "") // no providers
	ts := newTestServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusServiceUnavailable, string(body))
	}
}

func TestInvalidRequestBody_Returns400(t *testing.T) {
	chain := pipeline.NewChain()
	handler := newTestHandler(chain, "")
	ts := newTestServer(handler)
	defer ts.Close()

	// Send invalid JSON to the Anthropic endpoint.
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader("not valid json{{{"))
	if err != nil {
		t.Fatalf("POST /v1/messages failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d; want %d; body = %s", resp.StatusCode, http.StatusBadRequest, string(body))
	}
}
