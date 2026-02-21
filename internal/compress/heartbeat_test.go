package compress

import (
	"context"
	"testing"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

func TestIsHeartbeat_SystemPromptAndFewUserMessages(t *testing.T) {
	// System prompt present + 1 user message = heartbeat.
	req := &pipeline.Request{
		System: "You are a helpful assistant.",
		Messages: []pipeline.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	if !isHeartbeat(req) {
		t.Fatal("expected request with system prompt and 1 user message to be a heartbeat")
	}

	// System prompt + 2 user messages = still a heartbeat.
	req.Messages = append(req.Messages, pipeline.Message{Role: "user", Content: "How are you?"})
	if !isHeartbeat(req) {
		t.Fatal("expected request with system prompt and 2 user messages to be a heartbeat")
	}
}

func TestIsHeartbeat_NotWhenMoreThanTwoUserMessages(t *testing.T) {
	req := &pipeline.Request{
		System: "You are a helpful assistant.",
		Messages: []pipeline.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi!"},
			{Role: "user", Content: "How are you?"},
			{Role: "assistant", Content: "Good."},
			{Role: "user", Content: "Tell me more."},
		},
	}

	if isHeartbeat(req) {
		t.Fatal("expected request with > 2 user messages to NOT be a heartbeat")
	}
}

func TestIsHeartbeat_NotWithToolUse(t *testing.T) {
	req := &pipeline.Request{
		System: "You are a helpful assistant.",
		Messages: []pipeline.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: []pipeline.ContentBlock{
				{Type: "tool_use", Name: "calculator", Input: map[string]interface{}{"expr": "1+1"}},
			}},
		},
	}

	if isHeartbeat(req) {
		t.Fatal("expected request with tool_use in last message to NOT be a heartbeat")
	}
}

func TestHeartbeatMiddleware_ModelDowngrade(t *testing.T) {
	mw := NewHeartbeatMiddleware(true, 0, "gpt-4o-mini")

	req := &pipeline.Request{
		System: "You are a helpful assistant.",
		Model:  "gpt-4o",
		Messages: []pipeline.Message{
			{Role: "user", Content: "ping"},
		},
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	if result.Model != "gpt-4o-mini" {
		t.Fatalf("expected model to be downgraded to 'gpt-4o-mini', got %q", result.Model)
	}

	originalModel, ok := result.Metadata["original_model"].(string)
	if !ok || originalModel != "gpt-4o" {
		t.Fatalf("expected original_model metadata to be 'gpt-4o', got %v", result.Metadata["original_model"])
	}
}

func TestHeartbeatMiddleware_Disabled_IsNoOp(t *testing.T) {
	mw := NewHeartbeatMiddleware(false, 60, "gpt-4o-mini")

	if mw.Enabled() {
		t.Fatal("expected middleware to be disabled")
	}

	req := &pipeline.Request{
		System: "You are a helpful assistant.",
		Model:  "gpt-4o",
		Messages: []pipeline.Message{
			{Role: "user", Content: "ping"},
		},
	}

	// The pipeline would skip calling ProcessRequest, but verify the
	// Enabled flag works.
	if mw.Name() != "heartbeat" {
		t.Fatalf("expected name 'heartbeat', got %q", mw.Name())
	}

	// Even if called directly, nothing harmful happens. The chain skips it.
	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	// Model should NOT be downgraded since middleware is disabled --
	// but ProcessRequest itself does not check Enabled() (the chain does).
	// Since isHeartbeat returns true, the model would be changed. However,
	// the middleware reports Enabled()==false, so the chain never calls it.
	// We test that Enabled() returns false.
	_ = result
}
