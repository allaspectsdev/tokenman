package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// DetectFormat inspects the request path and returns the corresponding API format.
// /v1/messages maps to Anthropic, /v1/chat/completions maps to OpenAI.
func DetectFormat(r *http.Request) pipeline.APIFormat {
	path := r.URL.Path
	if strings.HasPrefix(path, "/v1/messages") {
		return pipeline.FormatAnthropic
	}
	if strings.HasPrefix(path, "/v1/chat/completions") {
		return pipeline.FormatOpenAI
	}
	return pipeline.FormatUnknown
}

// anthropicRawRequest is the raw JSON structure for an Anthropic Messages API request.
type anthropicRawRequest struct {
	Model       string          `json:"model"`
	Messages    json.RawMessage `json:"messages"`
	System      json.RawMessage `json:"system,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature *float64        `json:"temperature,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// ParseAnthropicRequest parses an Anthropic Messages API request body into a
// normalized pipeline.Request. It extracts the model, messages, system prompt,
// tools, stream flag, max_tokens, and temperature fields.
func ParseAnthropicRequest(body []byte) (*pipeline.Request, error) {
	var raw anthropicRawRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing anthropic request: %w", err)
	}

	req := &pipeline.Request{
		Format:    pipeline.FormatAnthropic,
		Model:     raw.Model,
		Stream:    raw.Stream,
		MaxTokens: raw.MaxTokens,
		RawBody:   body,
		Flags:     make(map[string]bool),
		Headers:   make(map[string]string),
	}

	if raw.Temperature != nil {
		req.Temperature = raw.Temperature
	}

	// Parse messages.
	if raw.Messages != nil {
		var messages []pipeline.Message
		if err := json.Unmarshal(raw.Messages, &messages); err != nil {
			return nil, fmt.Errorf("parsing anthropic messages: %w", err)
		}
		req.Messages = messages
	}

	// Parse system prompt: can be a plain string or an array of ContentBlock.
	if raw.System != nil && len(raw.System) > 0 {
		trimmed := strings.TrimSpace(string(raw.System))
		if strings.HasPrefix(trimmed, "\"") {
			// System is a plain string.
			var systemStr string
			if err := json.Unmarshal(raw.System, &systemStr); err != nil {
				return nil, fmt.Errorf("parsing anthropic system string: %w", err)
			}
			req.System = systemStr
		} else if strings.HasPrefix(trimmed, "[") {
			// System is an array of content blocks.
			var blocks []pipeline.ContentBlock
			if err := json.Unmarshal(raw.System, &blocks); err != nil {
				return nil, fmt.Errorf("parsing anthropic system blocks: %w", err)
			}
			req.SystemBlocks = blocks
			// Also build a flat text representation for convenience.
			var parts []string
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			req.System = strings.Join(parts, "\n")
		}
	}

	// Parse tools.
	if raw.Tools != nil {
		var tools []pipeline.Tool
		if err := json.Unmarshal(raw.Tools, &tools); err != nil {
			return nil, fmt.Errorf("parsing anthropic tools: %w", err)
		}
		req.Tools = tools
	}

	// Parse metadata.
	if raw.Metadata != nil {
		var metadata map[string]interface{}
		if err := json.Unmarshal(raw.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("parsing anthropic metadata: %w", err)
		}
		req.Metadata = metadata
	}

	return req, nil
}

// openaiRawRequest is the raw JSON structure for an OpenAI Chat Completions request.
type openaiRawRequest struct {
	Model       string          `json:"model"`
	Messages    json.RawMessage `json:"messages"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

// openaiRawMessage is the raw JSON structure for an individual OpenAI message.
type openaiRawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []pipeline.ToolCall `json:"tool_calls,omitempty"`
}

// ParseOpenAIRequest parses an OpenAI Chat Completions API request body into a
// normalized pipeline.Request. It extracts the model, messages, system prompt
// (from messages with role "system"), tools, stream flag, max_tokens, and temperature.
func ParseOpenAIRequest(body []byte) (*pipeline.Request, error) {
	var raw openaiRawRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parsing openai request: %w", err)
	}

	req := &pipeline.Request{
		Format:  pipeline.FormatOpenAI,
		Model:   raw.Model,
		Stream:  raw.Stream,
		RawBody: body,
		Flags:   make(map[string]bool),
		Headers: make(map[string]string),
	}

	if raw.MaxTokens != nil {
		req.MaxTokens = *raw.MaxTokens
	}

	if raw.Temperature != nil {
		req.Temperature = raw.Temperature
	}

	// Parse messages and extract system prompt.
	if raw.Messages != nil {
		var rawMsgs []openaiRawMessage
		if err := json.Unmarshal(raw.Messages, &rawMsgs); err != nil {
			return nil, fmt.Errorf("parsing openai messages: %w", err)
		}

		var systemParts []string
		for _, rm := range rawMsgs {
			msg := pipeline.Message{
				Role:       rm.Role,
				Name:       rm.Name,
				ToolCallID: rm.ToolCallID,
				ToolCalls:  rm.ToolCalls,
			}

			// Content can be a string or an array (multi-modal).
			if rm.Content != nil {
				trimmed := strings.TrimSpace(string(rm.Content))
				if strings.HasPrefix(trimmed, "\"") {
					var s string
					if err := json.Unmarshal(rm.Content, &s); err != nil {
						return nil, fmt.Errorf("parsing openai message content string: %w", err)
					}
					msg.Content = s
				} else if strings.HasPrefix(trimmed, "[") {
					var blocks []pipeline.ContentBlock
					if err := json.Unmarshal(rm.Content, &blocks); err != nil {
						return nil, fmt.Errorf("parsing openai message content blocks: %w", err)
					}
					msg.Content = blocks
				} else if trimmed == "null" {
					msg.Content = ""
				} else {
					msg.Content = string(rm.Content)
				}
			}

			// Collect system messages into the system prompt.
			if rm.Role == "system" {
				if s, ok := msg.Content.(string); ok {
					systemParts = append(systemParts, s)
				}
			}

			req.Messages = append(req.Messages, msg)
		}

		if len(systemParts) > 0 {
			req.System = strings.Join(systemParts, "\n")
		}
	}

	// Parse tools.
	if raw.Tools != nil {
		var tools []pipeline.Tool
		if err := json.Unmarshal(raw.Tools, &tools); err != nil {
			return nil, fmt.Errorf("parsing openai tools: %w", err)
		}
		req.Tools = tools
	}

	return req, nil
}
