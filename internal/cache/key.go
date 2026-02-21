package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// CacheKey computes a deterministic SHA-256 cache key from the model name,
// messages, and tools. The key is hex-encoded.
func CacheKey(model string, messages []pipeline.Message, tools []pipeline.Tool) string {
	h := sha256.New()

	// Write model.
	h.Write([]byte(model))
	h.Write([]byte{0}) // separator

	// Write messages as canonical JSON.
	if len(messages) > 0 {
		msgBytes, err := json.Marshal(messages)
		if err != nil {
			// Fall back to writing individual fields.
			for _, m := range messages {
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
	if len(tools) > 0 {
		toolBytes, err := json.Marshal(tools)
		if err == nil {
			h.Write(toolBytes)
		}
	}

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
