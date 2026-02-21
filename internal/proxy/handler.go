package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/allaspects/tokenman/internal/compress"
	"github.com/allaspects/tokenman/internal/metrics"
	"github.com/allaspects/tokenman/internal/pipeline"
	"github.com/allaspects/tokenman/internal/security"
	"github.com/allaspects/tokenman/internal/store"
	"github.com/allaspects/tokenman/internal/tokenizer"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// maxBodyStoreSize is the maximum size of a request or response body to
// persist for debugging. Bodies larger than this are not stored.
const maxBodyStoreSize = 1 << 20 // 1 MB

// bodyForStore returns the body as a string if it is under the maximum
// storage size, otherwise it returns an empty string.
func bodyForStore(b []byte) string {
	if len(b) > maxBodyStoreSize {
		return ""
	}
	return string(b)
}

// ProviderConfig holds the configuration needed to route a request to an upstream provider.
type ProviderConfig struct {
	BaseURL string
	APIKey  string
	Format  pipeline.APIFormat
}

// ProxyHandler is the main HTTP handler for the TokenMan proxy. It detects the
// API format, runs the pipeline chain, forwards requests to the upstream provider,
// and handles both streaming and non-streaming responses.
type ProxyHandler struct {
	chain     *pipeline.Chain
	client    *UpstreamClient
	logger    zerolog.Logger
	providers map[string]ProviderConfig
	collector *metrics.Collector
	tokenizer *tokenizer.Tokenizer
	store     *store.Store
	streams   *StreamManager
}

// NewProxyHandler creates a new ProxyHandler with the given pipeline chain,
// upstream client, logger, metrics collector, tokenizer, and store.
func NewProxyHandler(chain *pipeline.Chain, client *UpstreamClient, logger zerolog.Logger, collector *metrics.Collector, tok *tokenizer.Tokenizer, st *store.Store) *ProxyHandler {
	return &ProxyHandler{
		chain:     chain,
		client:    client,
		logger:    logger,
		providers: make(map[string]ProviderConfig),
		collector: collector,
		tokenizer: tok,
		store:     st,
		streams:   NewStreamManager(),
	}
}

// SetProviders configures the model-to-provider mapping.
func (h *ProxyHandler) SetProviders(providers map[string]ProviderConfig) {
	h.providers = providers
}

// resolveProvider looks up the provider configuration for the given model name.
// It returns the base URL, API key, format, and an error if no provider is found.
func (h *ProxyHandler) resolveProvider(model string) (baseURL, apiKey string, format pipeline.APIFormat, err error) {
	if p, ok := h.providers[model]; ok {
		return p.BaseURL, p.APIKey, p.Format, nil
	}

	// Try prefix matching for versioned model names (e.g., "claude-sonnet-4-20250514" matching "claude-sonnet-4").
	for key, p := range h.providers {
		if len(model) > len(key) && model[:len(key)] == key {
			return p.BaseURL, p.APIKey, p.Format, nil
		}
	}

	return "", "", pipeline.FormatUnknown, fmt.Errorf("no provider configured for model %q", model)
}

// HandleRequest is the main proxy handler. It processes incoming API requests
// through the pipeline chain, forwards them to the upstream provider, and
// returns the response to the client.
func (h *ProxyHandler) HandleRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()

	// Step 1: Generate a unique request ID.
	requestID := uuid.New().String()

	// Extract project header for per-project tracking.
	project := r.Header.Get("X-Tokenman-Project")
	if project == "" {
		project = "default"
	}

	// Track active requests for metrics.
	if h.collector != nil {
		h.collector.IncrementActive()
		defer h.collector.DecrementActive()
	}

	logger := h.logger.With().
		Str("request_id", requestID).
		Str("method", r.Method).
		Str("path", r.URL.Path).
		Logger()

	// Step 2: Detect the API format from the request path.
	format := DetectFormat(r)
	if format == pipeline.FormatUnknown {
		logger.Warn().Msg("unknown API format")
		writeJSONError(w, http.StatusBadRequest, "unsupported API endpoint")
		return
	}

	// Step 3: Read and parse the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error().Err(err).Msg("failed to read request body")
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var pipeReq *pipeline.Request
	switch format {
	case pipeline.FormatAnthropic:
		pipeReq, err = ParseAnthropicRequest(body)
	case pipeline.FormatOpenAI:
		pipeReq, err = ParseOpenAIRequest(body)
	default:
		writeJSONError(w, http.StatusBadRequest, "unsupported API format")
		return
	}

	if err != nil {
		logger.Error().Err(err).Msg("failed to parse request")
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse request: %v", err))
		return
	}

	pipeReq.ID = requestID
	pipeReq.ReceivedAt = startTime

	// Count input tokens before pipeline processing.
	if h.tokenizer != nil {
		var msgs []tokenizer.Message
		for _, m := range pipeReq.Messages {
			msgs = append(msgs, tokenizer.Message{
				Role:    m.Role,
				Content: compress.ExtractText(m.Content),
			})
		}
		pipeReq.TokensIn = h.tokenizer.CountMessages(pipeReq.Model, msgs)
	}

	// Copy relevant headers from the original request.
	for _, key := range []string{"X-Request-Id", "User-Agent", "Accept"} {
		if val := r.Header.Get(key); val != "" {
			pipeReq.Headers[key] = val
		}
	}

	logger = logger.With().
		Str("model", pipeReq.Model).
		Bool("stream", pipeReq.Stream).
		Logger()

	logger.Info().Msg("processing request")

	// Step 4: Run the pipeline chain's request phase.
	pipeReq, cachedResp, err := h.chain.ProcessRequest(ctx, pipeReq)
	if err != nil {
		// Check for budget exceeded error -> return 429.
		var budgetErr *security.BudgetError
		if errors.As(err, &budgetErr) {
			logger.Warn().Str("period", budgetErr.Period).Float64("spent", budgetErr.Spent).Float64("limit", budgetErr.Limit).Msg("budget limit exceeded")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write(budgetErr.ToJSON())
			return
		}
		logger.Error().Err(err).Msg("pipeline request processing failed")
		writeJSONError(w, http.StatusInternalServerError, "internal pipeline error")
		return
	}

	// Step 5: If the pipeline returned a cached response, write it directly.
	if cachedResp != nil {
		logger.Info().Msg("returning cached response")
		w.Header().Set("X-Tokenman-Cache", "HIT")
		writeCachedResponse(w, cachedResp)
		// Record cache hit in metrics.
		if h.collector != nil {
			cacheResp := &pipeline.Response{CacheHit: true, TokensSaved: pipeReq.TokensIn}
			h.collector.Record(pipeReq, cacheResp)
		}
		if h.store != nil {
			h.store.InsertRequest(&store.Request{
				ID:           requestID,
				Timestamp:    startTime.UTC().Format(time.RFC3339),
				Method:       r.Method,
				Path:         r.URL.Path,
				Format:       string(format),
				Model:        pipeReq.Model,
				TokensIn:     int64(pipeReq.TokensIn),
				LatencyMs:    time.Since(startTime).Milliseconds(),
				StatusCode:   cachedResp.StatusCode,
				CacheHit:     true,
				RequestType:  "cache_hit",
				RequestBody:  bodyForStore(body),
				ResponseBody: bodyForStore(cachedResp.Body),
				Project:      project,
			})
		}
		return
	}

	// Step 5b: Rebuild RawBody from modified request fields.
	pipeReq.RawBody = rebuildRequestBody(pipeReq)

	// Step 6: Resolve the upstream provider for this model.
	baseURL, apiKey, _, err := h.resolveProvider(pipeReq.Model)
	if err != nil {
		logger.Error().Err(err).Msg("failed to resolve provider")
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("no provider for model: %s", pipeReq.Model))
		return
	}

	// Step 7: Forward the request to the upstream provider.
	upstreamResp, err := h.client.Forward(ctx, pipeReq, baseURL, apiKey)
	if err != nil {
		logger.Error().Err(err).Msg("upstream request failed")
		writeJSONError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	defer upstreamResp.Body.Close()

	// Set cache miss header for non-cached responses.
	w.Header().Set("X-Tokenman-Cache", "MISS")

	// Step 8: Handle the response based on streaming vs non-streaming.
	if pipeReq.Stream {
		pipeResp, err := HandleStreaming(ctx, w, upstreamResp, format)
		if err != nil {
			logger.Error().Err(err).Msg("streaming error")
			// Response headers and partial data may already be written.
			return
		}
		pipeResp.RequestID = requestID
		pipeResp.Latency = time.Since(startTime)

		// Run the response pipeline for metrics/logging purposes.
		if _, respErr := h.chain.ProcessResponse(ctx, pipeReq, pipeResp); respErr != nil {
			logger.Error().Err(respErr).Msg("pipeline response processing failed (streaming)")
		}

		// Record metrics.
		if h.collector != nil {
			h.collector.Record(pipeReq, pipeResp)
		}

		// Persist request record.
		if h.store != nil {
			h.store.InsertRequest(&store.Request{
				ID:           requestID,
				Timestamp:    startTime.UTC().Format(time.RFC3339),
				Method:       r.Method,
				Path:         r.URL.Path,
				Format:       string(format),
				Model:        pipeReq.Model,
				TokensIn:     int64(pipeReq.TokensIn),
				TokensOut:    int64(pipeResp.TokensOut),
				TokensCached: int64(pipeResp.TokensCached),
				TokensSaved:  int64(pipeResp.TokensSaved),
				CostUSD:      pipeResp.CostUSD,
				SavingsUSD:   pipeResp.SavingsUSD,
				LatencyMs:    pipeResp.Latency.Milliseconds(),
				StatusCode:   pipeResp.StatusCode,
				CacheHit:     pipeResp.CacheHit,
				RequestType:  "normal",
				Provider:     pipeResp.Provider,
				RequestBody:  bodyForStore(body),
				Project:      project,
			})
		}

		logger.Info().
			Dur("latency", pipeResp.Latency).
			Int("status", pipeResp.StatusCode).
			Msg("streaming request completed")
		return
	}

	// Non-streaming response.
	respBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		logger.Error().Err(err).Msg("failed to read upstream response")
		writeJSONError(w, http.StatusBadGateway, "failed to read upstream response")
		return
	}

	pipeResp := &pipeline.Response{
		RequestID:  requestID,
		StatusCode: upstreamResp.StatusCode,
		Body:       respBody,
		Streaming:  false,
		Latency:    time.Since(startTime),
		Flags:      make(map[string]bool),
	}

	// Step 9: Run the pipeline chain's response phase.
	pipeResp, err = h.chain.ProcessResponse(ctx, pipeReq, pipeResp)
	if err != nil {
		logger.Error().Err(err).Msg("pipeline response processing failed")
		writeJSONError(w, http.StatusInternalServerError, "internal pipeline error")
		return
	}

	// Copy upstream response headers that are relevant.
	for _, key := range []string{"X-Request-Id", "Request-Id"} {
		if val := upstreamResp.Header.Get(key); val != "" {
			w.Header().Set(key, val)
		}
	}

	// Record metrics.
	if h.collector != nil {
		h.collector.Record(pipeReq, pipeResp)
	}

	// Persist request record.
	if h.store != nil {
		h.store.InsertRequest(&store.Request{
			ID:           requestID,
			Timestamp:    startTime.UTC().Format(time.RFC3339),
			Method:       r.Method,
			Path:         r.URL.Path,
			Format:       string(format),
			Model:        pipeReq.Model,
			TokensIn:     int64(pipeReq.TokensIn),
			TokensOut:    int64(pipeResp.TokensOut),
			TokensCached: int64(pipeResp.TokensCached),
			TokensSaved:  int64(pipeResp.TokensSaved),
			CostUSD:      pipeResp.CostUSD,
			SavingsUSD:   pipeResp.SavingsUSD,
			LatencyMs:    pipeResp.Latency.Milliseconds(),
			StatusCode:   pipeResp.StatusCode,
			CacheHit:     pipeResp.CacheHit,
			RequestType:  "normal",
			Provider:     pipeResp.Provider,
			RequestBody:  bodyForStore(body),
			ResponseBody: bodyForStore(respBody),
			Project:      project,
		})
	}

	// Write the response body.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(pipeResp.StatusCode)
	if _, writeErr := w.Write(pipeResp.Body); writeErr != nil {
		logger.Error().Err(writeErr).Msg("failed to write response body")
	}

	logger.Info().
		Dur("latency", pipeResp.Latency).
		Int("status", pipeResp.StatusCode).
		Msg("request completed")
}

// HandleHealth returns a simple JSON health check response.
func (h *ProxyHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// HandleModels proxies the /v1/models request to the appropriate upstream provider.
// It tries to resolve a default provider by checking common model names.
func (h *ProxyHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := h.logger.With().Str("path", r.URL.Path).Logger()

	// Find any configured provider to forward the models request to.
	var baseURL, apiKey string
	var format pipeline.APIFormat
	var found bool

	for _, p := range h.providers {
		baseURL = p.BaseURL
		apiKey = p.APIKey
		format = p.Format
		found = true
		break
	}

	if !found {
		logger.Warn().Msg("no providers configured for models endpoint")
		writeJSONError(w, http.StatusBadGateway, "no providers configured")
		return
	}

	// Build the upstream URL for the models endpoint.
	var upstreamURL string
	switch format {
	case pipeline.FormatAnthropic:
		upstreamURL = baseURL + "/v1/models"
	case pipeline.FormatOpenAI:
		upstreamURL = baseURL + "/v1/models"
	default:
		upstreamURL = baseURL + "/v1/models"
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create models request")
		writeJSONError(w, http.StatusInternalServerError, "failed to create models request")
		return
	}

	switch format {
	case pipeline.FormatAnthropic:
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	case pipeline.FormatOpenAI:
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	default:
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := h.client.client.Do(httpReq)
	if err != nil {
		logger.Error().Err(err).Msg("upstream models request failed")
		writeJSONError(w, http.StatusBadGateway, "upstream models request failed")
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error().Err(err).Msg("failed to read upstream models response")
		writeJSONError(w, http.StatusBadGateway, "failed to read upstream models response")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// writeCachedResponse writes a pipeline.CachedResponse directly to the client.
func writeCachedResponse(w http.ResponseWriter, cr *pipeline.CachedResponse) {
	contentType := cr.ContentType
	if contentType == "" {
		contentType = "application/json"
	}

	for key, val := range cr.Headers {
		w.Header().Set(key, val)
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(cr.StatusCode)
	_, _ = w.Write(cr.Body)
}

// writeJSONError writes a JSON error response with the given status code and message.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    "proxy_error",
		},
	}
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
}

// rebuildRequestBody serializes the modified pipeline.Request back to JSON
// for forwarding to the upstream provider.
func rebuildRequestBody(req *pipeline.Request) []byte {
	switch req.Format {
	case pipeline.FormatAnthropic:
		return rebuildAnthropicBody(req)
	case pipeline.FormatOpenAI:
		return rebuildOpenAIBody(req)
	default:
		return req.RawBody
	}
}

func rebuildAnthropicBody(req *pipeline.Request) []byte {
	body := map[string]interface{}{
		"model":      req.Model,
		"messages":   req.Messages,
		"stream":     req.Stream,
		"max_tokens": req.MaxTokens,
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.SystemBlocks) > 0 {
		body["system"] = req.SystemBlocks
	} else if req.System != "" {
		body["system"] = req.System
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if len(req.Metadata) > 0 {
		// Only include original metadata, not internal pipeline metadata.
		filtered := make(map[string]interface{})
		for k, v := range req.Metadata {
			// Skip internal keys.
			if k == "cache_key" || k == "cached_response" || k == "cached_tokens_saved" ||
				k == "pii_detections" || k == "pii_mapping" || k == "injection_detections" ||
				k == "request_type" || k == "original_model" ||
				strings.HasPrefix(k, "cache_") || strings.HasPrefix(k, "budget_") ||
				strings.HasPrefix(k, "history_") || strings.HasPrefix(k, "rules_") {
				continue
			}
			filtered[k] = v
		}
		if len(filtered) > 0 {
			body["metadata"] = filtered
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return req.RawBody // fallback to original
	}
	return data
}

func rebuildOpenAIBody(req *pipeline.Request) []byte {
	body := map[string]interface{}{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   req.Stream,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	data, err := json.Marshal(body)
	if err != nil {
		return req.RawBody // fallback to original
	}
	return data
}
