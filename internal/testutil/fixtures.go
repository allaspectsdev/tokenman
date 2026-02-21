package testutil

import (
	"encoding/json"
	"fmt"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// SampleAnthropicRequest returns a valid Anthropic Messages API request body.
func SampleAnthropicRequest() []byte {
	req := map[string]interface{}{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello, how are you?"},
		},
		"stream": false,
	}
	data, _ := json.Marshal(req)
	return data
}

// SampleAnthropicStreamRequest returns an Anthropic request with streaming enabled.
func SampleAnthropicStreamRequest() []byte {
	req := map[string]interface{}{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello"},
		},
		"stream": true,
	}
	data, _ := json.Marshal(req)
	return data
}

// SampleOpenAIRequest returns a valid OpenAI Chat Completions API request body.
func SampleOpenAIRequest() []byte {
	req := map[string]interface{}{
		"model": "gpt-4o",
		"messages": []map[string]interface{}{
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Hello, how are you?"},
		},
		"stream": false,
	}
	data, _ := json.Marshal(req)
	return data
}

// SampleAnthropicResponse returns a valid Anthropic Messages API response body.
func SampleAnthropicResponse() []byte {
	resp := map[string]interface{}{
		"id":    "msg_test123",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-sonnet-4-20250514",
		"content": []map[string]interface{}{
			{"type": "text", "text": "Hello! I'm doing well, thank you for asking."},
		},
		"stop_reason": "end_turn",
		"usage": map[string]interface{}{
			"input_tokens":  15,
			"output_tokens": 12,
		},
	}
	data, _ := json.Marshal(resp)
	return data
}

// SampleOpenAIResponse returns a valid OpenAI Chat Completions API response body.
func SampleOpenAIResponse() []byte {
	resp := map[string]interface{}{
		"id":      "chatcmpl-test123",
		"object":  "chat.completion",
		"created": 1234567890,
		"model":   "gpt-4o",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "Hello! I'm doing well, thank you for asking.",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]interface{}{
			"prompt_tokens":     25,
			"completion_tokens": 12,
			"total_tokens":      37,
		},
	}
	data, _ := json.Marshal(resp)
	return data
}

// SampleMessages generates n-turn conversation messages for testing.
func SampleMessages(n int) []pipeline.Message {
	messages := make([]pipeline.Message, 0, n*2)
	for i := 0; i < n; i++ {
		messages = append(messages, pipeline.Message{
			Role:    "user",
			Content: fmt.Sprintf("This is user message number %d with some content to work with.", i+1),
		})
		messages = append(messages, pipeline.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("This is assistant response number %d with some content.", i+1),
		})
	}
	return messages
}

// SamplePipelineRequest creates a pipeline.Request for testing.
func SamplePipelineRequest() *pipeline.Request {
	return &pipeline.Request{
		ID:        "test-request-123",
		Format:    pipeline.FormatAnthropic,
		Model:     "claude-sonnet-4-20250514",
		Messages:  SampleMessages(1),
		System:    "You are a helpful assistant.",
		Stream:    false,
		MaxTokens: 1024,
		RawBody:   SampleAnthropicRequest(),
		Flags:     make(map[string]bool),
		Headers:   make(map[string]string),
		Metadata:  make(map[string]interface{}),
	}
}

// SamplePipelineResponse creates a pipeline.Response for testing.
func SamplePipelineResponse() *pipeline.Response {
	return &pipeline.Response{
		RequestID:  "test-request-123",
		StatusCode: 200,
		Model:      "claude-sonnet-4-20250514",
		TokensOut:  12,
		Body:       SampleAnthropicResponse(),
		Streaming:  false,
		Flags:      make(map[string]bool),
	}
}
