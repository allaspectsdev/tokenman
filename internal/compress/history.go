package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// HistoryMiddleware compresses older messages that fall outside a recent
// window, replacing them with a compact summary and truncating large tool
// results. This keeps the context size manageable for long conversations.
type HistoryMiddleware struct {
	windowSize int
	enabled    bool
}

// NewHistoryMiddleware creates a HistoryMiddleware that preserves the most
// recent windowSize messages at full fidelity.
func NewHistoryMiddleware(windowSize int, enabled bool) *HistoryMiddleware {
	if windowSize < 1 {
		windowSize = 1
	}
	return &HistoryMiddleware{
		windowSize: windowSize,
		enabled:    enabled,
	}
}

// Name returns the middleware identifier.
func (h *HistoryMiddleware) Name() string { return "history" }

// Enabled reports whether the middleware is active.
func (h *HistoryMiddleware) Enabled() bool { return h.enabled }

// ProcessRequest compresses messages that fall outside the recent window.
func (h *HistoryMiddleware) ProcessRequest(_ context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if req.Flags == nil {
		req.Flags = make(map[string]bool)
	}

	// Check bypass header.
	if req.Headers != nil {
		if _, ok := req.Headers["X-Tokenman-NoCompress"]; ok {
			return req, nil
		}
	}

	totalMessages := len(req.Messages)
	if totalMessages <= h.windowSize {
		return req, nil
	}

	// Count original tokens (rough estimate).
	originalChars := 0
	for _, msg := range req.Messages {
		originalChars += len(ExtractText(msg.Content))
	}

	// Split into old (to compress) and recent (to keep).
	cutoff := totalMessages - h.windowSize
	oldMessages := req.Messages[:cutoff]
	recentMessages := req.Messages[cutoff:]

	// Build summary from old messages.
	summary := buildSummary(oldMessages)

	// Truncate tool results in old messages that we reference in the summary.
	// (The summary replaces them so this is just for completeness if anyone
	// ever inspects the raw slice.)

	// Truncate tool results in recent messages that are large.
	for i, msg := range recentMessages {
		recentMessages[i] = truncateToolResults(msg)
	}

	// Determine the role for the summary message. Use "system" when the
	// format supports it natively; fall back to "user" otherwise.
	summaryRole := "user"
	if req.Format == pipeline.FormatOpenAI {
		summaryRole = "system"
	}

	summaryMessage := pipeline.Message{
		Role:    summaryRole,
		Content: summary,
	}

	// Assemble the new message list: summary + recent window.
	newMessages := make([]pipeline.Message, 0, 1+len(recentMessages))
	newMessages = append(newMessages, summaryMessage)
	newMessages = append(newMessages, recentMessages...)
	req.Messages = newMessages

	// Track compression metrics.
	compressedChars := 0
	for _, msg := range req.Messages {
		compressedChars += len(ExtractText(msg.Content))
	}

	req.Flags["history_compressed"] = true
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	req.Metadata["history_original_messages"] = totalMessages
	req.Metadata["history_compressed_messages"] = len(req.Messages)
	req.Metadata["history_original_tokens"] = originalChars / 4
	req.Metadata["history_compressed_tokens"] = compressedChars / 4

	return req, nil
}

// ProcessResponse is a no-op for the history middleware.
func (h *HistoryMiddleware) ProcessResponse(_ context.Context, _ *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// buildSummary creates a compressed summary from the omitted messages.
func buildSummary(messages []pipeline.Message) string {
	n := len(messages)

	// Find the first user message to extract a preview.
	var firstUserPreview string
	for _, msg := range messages {
		if msg.Role == "user" {
			text := ExtractText(msg.Content)
			if text != "" {
				firstUserPreview = truncateString(text, 50)
				break
			}
		}
	}

	if firstUserPreview == "" {
		// Fall back to any message content.
		for _, msg := range messages {
			text := ExtractText(msg.Content)
			if text != "" {
				firstUserPreview = truncateString(text, 50)
				break
			}
		}
	}

	if firstUserPreview == "" {
		firstUserPreview = "(no text content)"
	}

	return fmt.Sprintf("[Compressed context from %d earlier messages]: %s", n, firstUserPreview)
}

// truncateToolResults truncates tool_result content blocks that exceed 100
// lines, keeping the first and last 100 lines with a truncation notice.
func truncateToolResults(msg pipeline.Message) pipeline.Message {
	const keepLines = 100

	switch v := msg.Content.(type) {
	case []pipeline.ContentBlock:
		for i, block := range v {
			if block.Type == "tool_result" || block.Type == "tool" {
				text := extractToolText(block)
				if text == "" {
					continue
				}
				v[i] = truncateBlockContent(v[i], text, keepLines)
			}
		}
		msg.Content = v

	case []interface{}:
		for i, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				blockType, _ := m["type"].(string)
				if blockType == "tool_result" || blockType == "tool" {
					if text, ok := m["text"].(string); ok && text != "" {
						m["text"] = truncateText(text, keepLines)
					}
					if content, ok := m["content"].(string); ok && content != "" {
						m["content"] = truncateText(content, keepLines)
					}
					v[i] = m
				}
			}
		}
		msg.Content = v

	case string:
		// Plain string content is not a tool result; leave it alone.
	}

	return msg
}

// extractToolText extracts the text content from a tool result block.
func extractToolText(block pipeline.ContentBlock) string {
	if block.Text != "" {
		return block.Text
	}
	// tool_result blocks may store their content in the Content field.
	if block.Content != nil {
		if s, ok := block.Content.(string); ok {
			return s
		}
	}
	return ""
}

// truncateBlockContent truncates a content block's text, keeping the first and
// last keepLines lines.
func truncateBlockContent(block pipeline.ContentBlock, text string, keepLines int) pipeline.ContentBlock {
	truncated := truncateText(text, keepLines)
	if block.Text != "" {
		block.Text = truncated
	} else if block.Content != nil {
		block.Content = truncated
	}
	return block
}

// truncateText keeps the first and last keepLines lines of text. If the text
// has fewer than 2*keepLines lines it is returned unchanged.
func truncateText(text string, keepLines int) string {
	lines := strings.Split(text, "\n")
	totalLines := len(lines)

	if totalLines <= keepLines*2 {
		return text
	}

	omitted := totalLines - keepLines*2
	head := lines[:keepLines]
	tail := lines[totalLines-keepLines:]

	var b strings.Builder
	b.Grow(len(text))
	for _, line := range head {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(fmt.Sprintf("[...truncated %d lines...]\n", omitted))
	for i, line := range tail {
		b.WriteString(line)
		if i < len(tail)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// truncateString returns at most maxLen characters of s, appending "..." if
// truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// Ensure HistoryMiddleware satisfies pipeline.Middleware at compile time.
var _ pipeline.Middleware = (*HistoryMiddleware)(nil)
