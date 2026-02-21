package pipeline

import (
	"context"
	"io"
	"time"
)

// APIFormat represents the API format being used.
type APIFormat string

const (
	FormatAnthropic APIFormat = "anthropic"
	FormatOpenAI    APIFormat = "openai"
	FormatUnknown   APIFormat = "unknown"
)

// Message represents a chat message in normalized form.
type Message struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // string or []ContentBlock
	Name       string      `json:"name,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
}

// ContentBlock represents a content block (for multi-part messages).
type ContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Source       map[string]interface{} `json:"source,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        interface{}            `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      interface{}            `json:"content,omitempty"`
	CacheControl map[string]string      `json:"cache_control,omitempty"`
}

// ToolCall represents a tool call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the function details in a tool call.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool represents a tool definition.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema,omitempty"`
	Type        string      `json:"type,omitempty"`
	Function    interface{} `json:"function,omitempty"`
}

// Request represents a normalized API request flowing through the pipeline.
type Request struct {
	ID           string
	ReceivedAt   time.Time
	Format       APIFormat
	Model        string
	Messages     []Message
	System       string         // system prompt text
	SystemBlocks []ContentBlock // structured system blocks (Anthropic)
	Tools        []Tool
	Stream       bool
	MaxTokens    int
	Temperature  *float64
	RawBody      []byte
	Metadata     map[string]interface{}
	TokensIn     int
	Flags        map[string]bool
	Headers      map[string]string // original request headers
}

// Response represents a normalized API response flowing through the pipeline.
type Response struct {
	RequestID    string
	StatusCode   int
	Model        string
	TokensOut    int
	TokensCached int
	TokensSaved  int
	Streaming    bool
	Body         []byte
	StreamReader io.ReadCloser
	Flags        map[string]bool
	CostUSD      float64
	SavingsUSD   float64
	Latency      time.Duration
	CacheHit     bool
	RequestType  string // "normal", "heartbeat", etc.
	Provider     string
	Error        string
}

// CachedResponse is returned when middleware short-circuits with a cached result.
type CachedResponse struct {
	Body        []byte
	StatusCode  int
	ContentType string
	Headers     map[string]string
}

// contextKey is an unexported type for context keys in this package.
type contextKey string

const (
	// cachedResponseKey is the context key for storing a CachedResponse.
	cachedResponseKey contextKey = "cached_response"

	// middlewareTimingsKey is the context key for storing per-middleware latency.
	middlewareTimingsKey contextKey = "middleware_timings"
)

// WithCachedResponse stores a CachedResponse in the context.
func WithCachedResponse(ctx context.Context, cr *CachedResponse) context.Context {
	return context.WithValue(ctx, cachedResponseKey, cr)
}

// GetCachedResponse retrieves a CachedResponse from the context, if present.
func GetCachedResponse(ctx context.Context) (*CachedResponse, bool) {
	cr, ok := ctx.Value(cachedResponseKey).(*CachedResponse)
	return cr, ok
}

// WithMiddlewareTimings stores the middleware timing map in the context.
func WithMiddlewareTimings(ctx context.Context, timings map[string]time.Duration) context.Context {
	return context.WithValue(ctx, middlewareTimingsKey, timings)
}

// GetMiddlewareTimings retrieves the middleware timing map from the context.
func GetMiddlewareTimings(ctx context.Context) (map[string]time.Duration, bool) {
	t, ok := ctx.Value(middlewareTimingsKey).(map[string]time.Duration)
	return t, ok
}
