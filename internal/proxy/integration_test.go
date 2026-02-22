package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allaspectsdev/tokenman/internal/metrics"
	"github.com/allaspectsdev/tokenman/internal/pipeline"
	"github.com/allaspectsdev/tokenman/internal/router"
	"github.com/allaspectsdev/tokenman/internal/store"
	"github.com/allaspectsdev/tokenman/internal/tokenizer"
	"github.com/rs/zerolog"
)

// setupIntegration creates a full proxy stack with a mock upstream server.
// It returns the proxy server, mock upstream, and a cleanup function.
func setupIntegration(t *testing.T, upstreamHandler http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	dbPath := filepath.Join(t.TempDir(), "integration.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	collector := metrics.NewCollector()
	tok := tokenizer.New()
	logger := zerolog.Nop()

	chain := pipeline.NewChain() // empty chain for integration tests

	rtr := router.NewRouter(map[string]*router.ProviderConfig{
		"anthropic": {
			Name:    "anthropic",
			BaseURL: upstream.URL,
			APIKey:  "test-key",
			Format:  pipeline.FormatAnthropic,
			Models:  []string{"claude-sonnet-4-20250514"},
			Enabled: true,
			Priority: 1,
		},
		"openai": {
			Name:    "openai",
			BaseURL: upstream.URL,
			APIKey:  "test-key",
			Format:  pipeline.FormatOpenAI,
			Models:  []string{"gpt-4o"},
			Enabled: true,
			Priority: 2,
		},
	}, nil, "anthropic", false)

	handler := NewProxyHandler(
		chain,
		NewUpstreamClient(),
		logger,
		collector,
		tok,
		st,
		10<<20, // 10MB max body
		0,      // no response size limit
		0,      // no stream timeout
		nil,    // no circuit breaker
		RetryConfig{},
		rtr,
		0,     // no stream session limit
		0,     // no session TTL
		false, // no body storage
	)

	srv := NewServer(handler, ":0", 0, 0, 0, false, "")
	return srv, upstream
}

func TestIntegration_AnthropicNormalRequest(t *testing.T) {
	upstreamResp := `{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"text","text":"Hello!"}],"model":"claude-sonnet-4-20250514","usage":{"input_tokens":10,"output_tokens":5}}`

	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify upstream received correct headers.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("upstream x-api-key: got %q, want %q", r.Header.Get("x-api-key"), "test-key")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("upstream missing anthropic-version header")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(upstreamResp))
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	if w.Header().Get("X-Tokenman-Cache") != "MISS" {
		t.Errorf("cache header: got %q, want %q", w.Header().Get("X-Tokenman-Cache"), "MISS")
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if resp["model"] != "claude-sonnet-4-20250514" {
		t.Errorf("response model: got %v", resp["model"])
	}
}

func TestIntegration_OpenAINormalRequest(t *testing.T) {
	upstreamResp := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"Hi there!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`

	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("upstream Authorization: got %q", r.Header.Get("Authorization"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(upstreamResp))
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

func TestIntegration_Upstream429_Propagated(t *testing.T) {
	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if w.Header().Get("Retry-After") != "30" {
		t.Errorf("Retry-After: got %q, want %q", w.Header().Get("Retry-After"), "30")
	}
}

func TestIntegration_Upstream500_Propagated(t *testing.T) {
	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal error"}}`))
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestIntegration_Streaming(t *testing.T) {
	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		events := []string{
			`data: {"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}` + "\n\n",
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}` + "\n\n",
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" World"}}` + "\n\n",
			`data: [DONE]` + "\n\n",
		}
		for _, e := range events {
			w.Write([]byte(e))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":true}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want %q", w.Header().Get("Content-Type"), "text/event-stream")
	}

	responseBody := w.Body.String()
	if !strings.Contains(responseBody, "Hello") {
		t.Error("streaming response should contain 'Hello'")
	}
	if !strings.Contains(responseBody, "World") {
		t.Error("streaming response should contain 'World'")
	}
}

func TestIntegration_InvalidBody(t *testing.T) {
	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called for invalid body")
	})

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIntegration_UnknownEndpoint(t *testing.T) {
	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called for unknown endpoint")
	})

	req := httptest.NewRequest("POST", "/v1/unknown", strings.NewReader("{}"))

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// chi returns 405 for unregistered paths with other methods
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 404 or 405", w.Code)
	}
}

func TestIntegration_HealthEndpoint(t *testing.T) {
	srv, _ := setupIntegration(t, nil)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("health status: got %q, want %q", body["status"], "ok")
	}
}

func TestIntegration_ReadinessEndpoint(t *testing.T) {
	srv, _ := setupIntegration(t, nil)

	req := httptest.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if body["status"] != "ready" {
		t.Errorf("readiness status: got %v, want %q", body["status"], "ready")
	}
}

func TestIntegration_RetrySuccess(t *testing.T) {
	attempt := 0
	srv, _ := setupIntegrationWithRetry(t, func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"temporarily unavailable"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_retry","type":"message","role":"assistant","content":[{"type":"text","text":"Success after retry"}],"model":"claude-sonnet-4-20250514"}`))
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status after retry: got %d, want %d", w.Code, http.StatusOK)
	}
	if attempt < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempt)
	}
}

func TestIntegration_CircuitBreakerTrip(t *testing.T) {
	srv, _ := setupIntegrationWithRetry(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"bad gateway"}`))
	})

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`

	// Send multiple requests to trip the circuit breaker.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, req)
	}

	// The circuit should be open by now; request should get 502.
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status after circuit trip: got %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestIntegration_RequestPersistence(t *testing.T) {
	upstreamResp := `{"id":"msg_persist","type":"message","role":"assistant","content":[{"type":"text","text":"Persisted"}],"model":"claude-sonnet-4-20250514"}`

	var savedStore *store.Store

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(upstreamResp))
	}))
	defer upstream.Close()

	dbPath := filepath.Join(t.TempDir(), "persist.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	savedStore = st

	collector := metrics.NewCollector()
	logger := zerolog.Nop()
	chain := pipeline.NewChain()

	rtr := router.NewRouter(map[string]*router.ProviderConfig{
		"anthropic": {
			Name: "anthropic", BaseURL: upstream.URL, APIKey: "test-key",
			Format: pipeline.FormatAnthropic, Models: []string{"claude-sonnet-4-20250514"},
			Enabled: true, Priority: 1,
		},
	}, nil, "anthropic", false)

	handler := NewProxyHandler(chain, NewUpstreamClient(), logger, collector, nil, st,
		10<<20, 0, 0, nil, RetryConfig{}, rtr, 0, 0, false)

	srv := NewServer(handler, ":0", 0, 0, 0, false, "")

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	// Verify the request was persisted.
	time.Sleep(50 * time.Millisecond) // small delay for async writes
	requests, err := savedStore.ListRequests(10, 0)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Errorf("persisted requests: got %d, want 1", len(requests))
	}
}

func TestIntegration_ProjectHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_proj","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"model":"claude-sonnet-4-20250514"}`))
	}))
	defer upstream.Close()

	dbPath := filepath.Join(t.TempDir(), "project.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	collector := metrics.NewCollector()
	logger := zerolog.Nop()
	chain := pipeline.NewChain()

	rtr := router.NewRouter(map[string]*router.ProviderConfig{
		"anthropic": {
			Name: "anthropic", BaseURL: upstream.URL, APIKey: "test-key",
			Format: pipeline.FormatAnthropic, Models: []string{"claude-sonnet-4-20250514"},
			Enabled: true, Priority: 1,
		},
	}, nil, "anthropic", false)

	handler := NewProxyHandler(chain, NewUpstreamClient(), logger, collector, nil, st,
		10<<20, 0, 0, nil, RetryConfig{}, rtr, 0, 0, false)
	srv := NewServer(handler, ":0", 0, 0, 0, false, "")

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tokenman-Project", "my-project")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	time.Sleep(50 * time.Millisecond)
	requests, _ := st.ListRequests(10, 0)
	if len(requests) == 0 {
		t.Fatal("no requests persisted")
	}

	got, err := st.GetRequest(requests[0].ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Project != "my-project" {
		t.Errorf("Project: got %q, want %q", got.Project, "my-project")
	}
}

func TestIntegration_MaxBodySize(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called when body is too large")
	}))
	defer upstream.Close()

	collector := metrics.NewCollector()
	logger := zerolog.Nop()
	chain := pipeline.NewChain()

	rtr := router.NewRouter(map[string]*router.ProviderConfig{
		"anthropic": {
			Name: "anthropic", BaseURL: upstream.URL, APIKey: "test-key",
			Format: pipeline.FormatAnthropic, Models: []string{"claude-sonnet-4-20250514"},
			Enabled: true, Priority: 1,
		},
	}, nil, "anthropic", false)

	handler := NewProxyHandler(chain, NewUpstreamClient(), logger, collector, nil, nil,
		100, // 100 byte max body size
		0, 0, nil, RetryConfig{}, rtr, 0, 0, false)
	srv := NewServer(handler, ":0", 0, 0, 0, false, "")

	// Build a body larger than 100 bytes.
	largeBody := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"` + strings.Repeat("A", 200) + `"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestIntegration_ResponseSizeLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Send a response larger than the limit.
		w.Write([]byte(`{"id":"msg_big","type":"message","content":"` + strings.Repeat("X", 1000) + `"}`))
	}))
	defer upstream.Close()

	collector := metrics.NewCollector()
	logger := zerolog.Nop()
	chain := pipeline.NewChain()

	rtr := router.NewRouter(map[string]*router.ProviderConfig{
		"anthropic": {
			Name: "anthropic", BaseURL: upstream.URL, APIKey: "test-key",
			Format: pipeline.FormatAnthropic, Models: []string{"claude-sonnet-4-20250514"},
			Enabled: true, Priority: 1,
		},
	}, nil, "anthropic", false)

	handler := NewProxyHandler(chain, NewUpstreamClient(), logger, collector, nil, nil,
		10<<20,
		100, // 100 byte max response size
		0, nil, RetryConfig{}, rtr, 0, 0, false)
	srv := NewServer(handler, ":0", 0, 0, 0, false, "")

	body := `{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Hi"}],"max_tokens":100,"stream":false}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want %d (response too large)", w.Code, http.StatusBadGateway)
	}
}

func TestIntegration_ModelsEndpoint(t *testing.T) {
	srv, _ := setupIntegration(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"claude-sonnet-4-20250514","object":"model"}]}`))
			return
		}
	})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		body, _ := io.ReadAll(w.Body)
		t.Errorf("status: got %d, want %d (body: %s)", w.Code, http.StatusOK, string(body))
	}
}

// setupIntegrationWithRetry creates a proxy stack with retry and circuit breaker enabled.
func setupIntegrationWithRetry(t *testing.T, upstreamHandler http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	dbPath := filepath.Join(t.TempDir(), "retry.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	collector := metrics.NewCollector()
	logger := zerolog.Nop()
	chain := pipeline.NewChain()

	cbRegistry := NewCircuitBreakerRegistry(3, 10*time.Second, 1) // low threshold for testing

	rtr := router.NewRouter(map[string]*router.ProviderConfig{
		"anthropic": {
			Name:    "anthropic",
			BaseURL: upstream.URL,
			APIKey:  "test-key",
			Format:  pipeline.FormatAnthropic,
			Models:  []string{"claude-sonnet-4-20250514"},
			Enabled: true,
			Priority: 1,
		},
	}, nil, "anthropic", false)

	handler := NewProxyHandler(
		chain,
		NewUpstreamClient(),
		logger,
		collector,
		nil,
		st,
		10<<20,
		0,
		0,
		cbRegistry,
		RetryConfig{
			MaxAttempts: 3,
			BaseDelay:   1 * time.Millisecond, // fast retries for testing
			MaxDelay:    10 * time.Millisecond,
		},
		rtr,
		0,
		0,
		false,
	)

	srv := NewServer(handler, ":0", 0, 0, 0, false, "")
	return srv, upstream
}
