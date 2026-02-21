package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
	"github.com/allaspectsdev/tokenman/internal/tracing"
)

// UpstreamClient manages forwarding requests to upstream LLM API providers.
// It uses a shared http.Client with connection pooling and a 60-second timeout.
type UpstreamClient struct {
	client *http.Client
}

// NewUpstreamClient creates a new UpstreamClient with sensible defaults
// for connection pooling and timeouts.
func NewUpstreamClient() *UpstreamClient {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &UpstreamClient{
		client: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		},
	}
}

// Forward sends the pipeline request to the upstream provider and returns the
// raw http.Response. The caller is responsible for closing the response body.
// For streaming requests the timeout is removed to allow long-lived connections.
func (u *UpstreamClient) Forward(ctx context.Context, req *pipeline.Request, baseURL, apiKey string) (*http.Response, error) {
	// Build the upstream URL: baseURL + the original API path.
	upstreamURL := buildUpstreamURL(baseURL, req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, fmt.Errorf("creating upstream request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Set provider-specific authentication headers.
	switch req.Format {
	case pipeline.FormatAnthropic:
		httpReq.Header.Set("x-api-key", apiKey)
		// Use the original anthropic-version if forwarded from the client,
		// otherwise fall back to a default.
		if v, ok := req.Headers["Anthropic-Version"]; ok {
			httpReq.Header.Set("anthropic-version", v)
		} else {
			httpReq.Header.Set("anthropic-version", "2023-06-01")
		}
	case pipeline.FormatOpenAI:
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	default:
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// Forward any custom headers from the pipeline request that the user set,
	// except for those already established above.
	for key, val := range req.Headers {
		lk := http.CanonicalHeaderKey(key)
		if lk == "Content-Type" || lk == "X-Api-Key" || lk == "Authorization" || lk == "Anthropic-Version" {
			continue
		}
		httpReq.Header.Set(key, val)
	}

	// Inject trace context (traceparent / tracestate) into the upstream request.
	tracing.InjectHeaders(ctx, httpReq)

	// Create a span for the upstream call.
	ctx, span := tracing.StartUpstreamSpan(ctx, upstreamURL, string(req.Format))
	defer span.End()

	// For streaming requests, use a client without a timeout so the connection
	// can stay open for the duration of the stream.
	client := u.client
	if req.Stream {
		client = &http.Client{
			Transport: u.client.Transport,
			// No timeout for streaming.
		}
	}

	resp, err := client.Do(httpReq.WithContext(ctx))
	if err != nil {
		tracing.RecordError(ctx, err)
		return nil, fmt.Errorf("forwarding to upstream %s: %w", upstreamURL, err)
	}

	return resp, nil
}

// buildUpstreamURL constructs the full upstream URL based on the provider format.
func buildUpstreamURL(baseURL string, req *pipeline.Request) string {
	switch req.Format {
	case pipeline.FormatAnthropic:
		return baseURL + "/v1/messages"
	case pipeline.FormatOpenAI:
		return baseURL + "/v1/chat/completions"
	default:
		return baseURL + "/v1/chat/completions"
	}
}
