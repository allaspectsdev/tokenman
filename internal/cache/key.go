package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// CacheKey computes a deterministic SHA-256 cache key from the request's
// model, messages, tools, system prompt, system blocks, and max_tokens.
// The key is hex-encoded.
func CacheKey(req *pipeline.Request) string {
	h := sha256.New()

	// Write model.
	h.Write([]byte(req.Model))
	h.Write([]byte{0}) // separator

	// Write messages as canonical JSON.
	if len(req.Messages) > 0 {
		msgBytes, err := json.Marshal(req.Messages)
		if err != nil {
			// Fall back to writing individual fields.
			for _, m := range req.Messages {
				h.Write([]byte(m.Role))
				h.Write([]byte{0})
				if s, ok := m.Content.(string); ok {
					h.Write([]byte(s))
				} else {
					b, _ := json.Marshal(m.Content)
					h.Write(b)
				}
				h.Write([]byte{0})
			}
		} else {
			h.Write(msgBytes)
		}
	}
	h.Write([]byte{0}) // separator

	// Write tools as canonical JSON.
	if len(req.Tools) > 0 {
		toolBytes, err := json.Marshal(req.Tools)
		if err == nil {
			h.Write(toolBytes)
		}
	}
	h.Write([]byte{0}) // separator

	// Write system prompt.
	h.Write([]byte(req.System))
	h.Write([]byte{0}) // separator

	// Write system blocks as canonical JSON.
	if len(req.SystemBlocks) > 0 {
		sysBytes, err := json.Marshal(req.SystemBlocks)
		if err == nil {
			h.Write(sysBytes)
		}
	}
	h.Write([]byte{0}) // separator

	// Write max_tokens.
	fmt.Fprintf(h, "%d", req.MaxTokens)

	return fmt.Sprintf("%x", h.Sum(nil))
}

// IsCacheable returns true if the request is eligible for caching.
// A request is cacheable if:
//   - Temperature is nil (not set) or exactly 0 (deterministic).
//   - Streaming is not enabled.
func IsCacheable(req *pipeline.Request) bool {
	// Streaming responses cannot be cached as a single body.
	if req.Stream {
		return false
	}

	// Only cache deterministic requests (temperature 0 or unset).
	if req.Temperature != nil && *req.Temperature != 0 {
		return false
	}

	return true
}
