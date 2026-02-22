package compress

import (
	"context"
	"encoding/json"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// FingerprintStore is the interface required by DedupMiddleware to persist
// and query content fingerprints. A concrete implementation backed by SQLite
// lives in the store package.
type FingerprintStore interface {
	// UpsertFingerprint records a fingerprint. If the hash already exists the
	// store increments its hit count and updates the last-seen timestamp.
	UpsertFingerprint(hash, contentType string, tokenCount int) error

	// GetFingerprint returns the hit count and last-seen time for the given
	// hash. If the hash has never been recorded the implementation should
	// return hitCount==0 with a zero time and a nil error (or an appropriate
	// sentinel that callers can detect).
	GetFingerprint(hash string) (hitCount int, lastSeen time.Time, err error)
}

// DedupMiddleware detects repeated content blocks (system prompts, tool
// definitions) and annotates the request so that the upstream provider can
// leverage its caching mechanism.
type DedupMiddleware struct {
	store   FingerprintStore
	ttl     time.Duration
	enabled bool
}

// NewDedupMiddleware creates a DedupMiddleware. ttlSeconds controls how long a
// fingerprint is considered "recent" for cache-control annotation.
func NewDedupMiddleware(store FingerprintStore, ttlSeconds int, enabled bool) *DedupMiddleware {
	return &DedupMiddleware{
		store:   store,
		ttl:     time.Duration(ttlSeconds) * time.Second,
		enabled: enabled,
	}
}

// Name returns the middleware identifier.
func (d *DedupMiddleware) Name() string { return "dedup" }

// Enabled reports whether the middleware is active.
func (d *DedupMiddleware) Enabled() bool { return d.enabled }

// ProcessRequest hashes static content, upserts fingerprints, and annotates
// the request for provider-side caching when duplicates are detected.
func (d *DedupMiddleware) ProcessRequest(_ context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if req.Flags == nil {
		req.Flags = make(map[string]bool)
	}

	cacheEligible := 0

	// --- System prompt fingerprinting ---
	if req.System != "" {
		hash := HashContent(req.System)
		tokenCount := roughTokenEstimate(req.System)
		if err := d.store.UpsertFingerprint(hash, "system", tokenCount); err != nil {
			return req, err
		}

		if d.seenWithinTTL(hash) {
			cacheEligible += tokenCount
			if req.Format == pipeline.FormatAnthropic {
				req.SystemBlocks = annotateCacheControl(req.SystemBlocks, req.System)
			}
		}
	}

	// Also handle structured system blocks.
	for i, block := range req.SystemBlocks {
		if block.Text == "" {
			continue
		}
		hash := HashContent(block.Text)
		tokenCount := roughTokenEstimate(block.Text)
		if err := d.store.UpsertFingerprint(hash, "system_block", tokenCount); err != nil {
			return req, err
		}
		if d.seenWithinTTL(hash) {
			cacheEligible += tokenCount
			if req.Format == pipeline.FormatAnthropic {
				// Only add cache_control if the block doesn't already have
				// one set by the client, to avoid breaking TTL ordering.
				if req.SystemBlocks[i].CacheControl == nil {
					req.SystemBlocks[i].CacheControl = map[string]interface{}{
						"type": "ephemeral",
					}
				}
			}
		}
	}

	// --- Tool definition fingerprinting ---
	for i, tool := range req.Tools {
		serialised := serializeTool(tool)
		hash := HashContent(serialised)
		tokenCount := roughTokenEstimate(serialised)
		if err := d.store.UpsertFingerprint(hash, "tool", tokenCount); err != nil {
			return req, err
		}
		if d.seenWithinTTL(hash) {
			cacheEligible += tokenCount
			if req.Format == pipeline.FormatAnthropic {
				// Tools don't have CacheControl directly, but we can mark
				// the last tool with cache_control via the request metadata.
				// For now, track it via metadata so the proxy layer can
				// inject the annotation.
				if req.Metadata == nil {
					req.Metadata = make(map[string]interface{})
				}
				key := "cache_tool_" + tool.Name
				req.Metadata[key] = true
				_ = i // index available if needed
			}
		}
	}

	// --- OpenAI prefix-matching optimisation ---
	if req.Format == pipeline.FormatOpenAI && len(req.Messages) > 1 {
		req.Messages = reorderForPrefixMatch(req.Messages)
	}

	// Record cache-eligible token count.
	if cacheEligible > 0 {
		req.Flags["cache_eligible"] = true
		if req.Metadata == nil {
			req.Metadata = make(map[string]interface{})
		}
		req.Metadata["cache_eligible_tokens"] = cacheEligible
	}

	return req, nil
}

// ProcessResponse is a no-op for the dedup middleware.
func (d *DedupMiddleware) ProcessResponse(_ context.Context, _ *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// seenWithinTTL returns true if the hash was recorded and its last-seen time
// falls within the configured TTL window.
func (d *DedupMiddleware) seenWithinTTL(hash string) bool {
	hitCount, lastSeen, err := d.store.GetFingerprint(hash)
	if err != nil || hitCount <= 1 {
		// First time seeing this hash (just upserted) or lookup error.
		return false
	}
	if d.ttl <= 0 {
		return true
	}
	return time.Since(lastSeen) <= d.ttl
}

// annotateCacheControl ensures the system blocks contain at least one text
// block with cache_control set to ephemeral. If SystemBlocks is empty it
// creates one from the plain System string.
func annotateCacheControl(blocks []pipeline.ContentBlock, systemText string) []pipeline.ContentBlock {
	if len(blocks) == 0 && systemText != "" {
		blocks = []pipeline.ContentBlock{
			{
				Type: "text",
				Text: systemText,
				CacheControl: map[string]interface{}{
					"type": "ephemeral",
				},
			},
		}
		return blocks
	}

	// Annotate the last text block for maximum prefix-cache coverage.
	// Only add cache_control if the block doesn't already have one from
	// the client, to avoid breaking TTL ordering constraints.
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Type == "text" {
			if blocks[i].CacheControl == nil {
				blocks[i].CacheControl = map[string]interface{}{
					"type": "ephemeral",
				}
			}
			break
		}
	}
	return blocks
}

// reorderForPrefixMatch moves system and static instruction messages to the
// front of the message list so that OpenAI's prefix caching is more likely
// to match. Messages that are already at the front are not moved.
func reorderForPrefixMatch(messages []pipeline.Message) []pipeline.Message {
	var systemMsgs []pipeline.Message
	var otherMsgs []pipeline.Message

	for _, msg := range messages {
		if msg.Role == "system" {
			systemMsgs = append(systemMsgs, msg)
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	if len(systemMsgs) == 0 {
		return messages
	}

	result := make([]pipeline.Message, 0, len(messages))
	result = append(result, systemMsgs...)
	result = append(result, otherMsgs...)
	return result
}

// serializeTool produces a stable string representation of a tool for hashing.
func serializeTool(t pipeline.Tool) string {
	data, err := json.Marshal(t)
	if err != nil {
		return t.Name + ":" + t.Description
	}
	return string(data)
}

// roughTokenEstimate returns a rough token count based on character length.
// This avoids importing the tokenizer package and is sufficient for fingerprint
// bookkeeping. The heuristic is ~4 characters per token.
func roughTokenEstimate(s string) int {
	n := len(s) / 4
	if n == 0 && len(s) > 0 {
		return 1
	}
	return n
}

// Ensure DedupMiddleware satisfies pipeline.Middleware at compile time.
var _ pipeline.Middleware = (*DedupMiddleware)(nil)
