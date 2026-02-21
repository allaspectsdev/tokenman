package compress

import (
	"context"
	"sync"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// heartbeatEntry holds a cached heartbeat response along with its expiry.
type heartbeatEntry struct {
	response  []byte
	expiresAt time.Time
}

// HeartbeatMiddleware detects "heartbeat" requests -- lightweight keep-alive
// or status-check interactions -- and applies optimisations such as model
// downgrade and response deduplication.
type HeartbeatMiddleware struct {
	enabled            bool
	dedupWindowSeconds int
	heartbeatModel     string

	// cache stores recent heartbeat responses keyed by a content hash.
	cache sync.Map // map[string]*heartbeatEntry
}

// NewHeartbeatMiddleware creates a HeartbeatMiddleware.
//
// dedupWindow controls how many seconds a heartbeat hash is considered
// "recent" for frequency dedup. heartbeatModel, if non-empty, is the
// model name to downgrade heartbeat requests to.
func NewHeartbeatMiddleware(enabled bool, dedupWindow int, heartbeatModel string) *HeartbeatMiddleware {
	return &HeartbeatMiddleware{
		enabled:            enabled,
		dedupWindowSeconds: dedupWindow,
		heartbeatModel:     heartbeatModel,
	}
}

// Name returns the middleware identifier.
func (h *HeartbeatMiddleware) Name() string { return "heartbeat" }

// Enabled reports whether the middleware is active.
func (h *HeartbeatMiddleware) Enabled() bool { return h.enabled }

// ProcessRequest checks whether the request is a heartbeat and, if so,
// applies optimisations: flags it, deduplicates recent identical heartbeats,
// and optionally downgrades the model.
func (h *HeartbeatMiddleware) ProcessRequest(_ context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if req.Flags == nil {
		req.Flags = make(map[string]bool)
	}

	if !isHeartbeat(req) {
		return req, nil
	}

	// Check priority header -- skip optimisation if the caller explicitly
	// marks the request as high-priority.
	if req.Headers != nil {
		if pri, ok := req.Headers["X-Tokenman-Priority"]; ok && pri == "high" {
			return req, nil
		}
	}

	// Flag the request as a heartbeat.
	req.Flags["heartbeat"] = true
	req.Flags["request_type"] = true
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	req.Metadata["request_type"] = "heartbeat"

	// --- Frequency dedup ---
	hash := heartbeatHash(req)

	if h.dedupWindowSeconds > 0 {
		if entry, ok := h.cache.Load(hash); ok {
			he := entry.(*heartbeatEntry)
			if time.Now().Before(he.expiresAt) {
				req.Flags["heartbeat_cache_hit"] = true
			}
		}
	}

	// --- Model downgrade ---
	if h.heartbeatModel != "" {
		if req.Metadata == nil {
			req.Metadata = make(map[string]interface{})
		}
		req.Metadata["original_model"] = req.Model
		req.Model = h.heartbeatModel
	}

	return req, nil
}

// ProcessResponse caches heartbeat responses by hash for future dedup.
func (h *HeartbeatMiddleware) ProcessResponse(_ context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	if req.Flags == nil || !req.Flags["heartbeat"] {
		return resp, nil
	}

	hash := heartbeatHash(req)

	if h.dedupWindowSeconds > 0 && resp.Body != nil {
		h.cache.Store(hash, &heartbeatEntry{
			response:  resp.Body,
			expiresAt: time.Now().Add(time.Duration(h.dedupWindowSeconds) * time.Second),
		})
	}

	// Lazily evict expired entries. We do a best-effort sweep without
	// blocking the response path.
	go h.evictExpired()

	resp.RequestType = "heartbeat"
	return resp, nil
}

// isHeartbeat returns true if the request looks like a heartbeat:
//   - A system prompt is present
//   - There are at most 2 user messages
//   - The last message does not contain a tool_use block
func isHeartbeat(req *pipeline.Request) bool {
	if req.System == "" && len(req.SystemBlocks) == 0 {
		return false
	}

	userCount := 0
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			userCount++
		}
	}
	if userCount > 2 {
		return false
	}

	// Check the last message for tool_use content.
	if len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if hasToolUse(last) {
			return false
		}
	}

	return true
}

// hasToolUse checks whether a message contains a tool_use content block.
func hasToolUse(msg pipeline.Message) bool {
	// Check ToolCalls field.
	if len(msg.ToolCalls) > 0 {
		return true
	}

	switch v := msg.Content.(type) {
	case []pipeline.ContentBlock:
		for _, block := range v {
			if block.Type == "tool_use" {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				if t, _ := m["type"].(string); t == "tool_use" {
					return true
				}
			}
		}
	}
	return false
}

// heartbeatHash produces a deterministic hash for dedup lookup, combining
// the system prompt and the last user message.
func heartbeatHash(req *pipeline.Request) string {
	systemText := req.System
	if systemText == "" {
		for _, b := range req.SystemBlocks {
			systemText += b.Text
		}
	}

	var lastUserText string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastUserText = ExtractText(req.Messages[i].Content)
			break
		}
	}

	return HashContent(systemText + "\x00" + lastUserText)
}

// evictExpired removes entries from the cache whose expiry has passed.
func (h *HeartbeatMiddleware) evictExpired() {
	now := time.Now()
	h.cache.Range(func(key, value interface{}) bool {
		entry := value.(*heartbeatEntry)
		if now.After(entry.expiresAt) {
			h.cache.Delete(key)
		}
		return true
	})
}

// Ensure HeartbeatMiddleware satisfies pipeline.Middleware at compile time.
var _ pipeline.Middleware = (*HeartbeatMiddleware)(nil)
