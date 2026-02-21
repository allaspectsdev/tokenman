package compress

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

func TestHistoryMiddleware_NoCompressionWithinWindow(t *testing.T) {
	mw := NewHistoryMiddleware(10, true)

	messages := []pipeline.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
		{Role: "user", Content: "How are you?"},
	}

	req := &pipeline.Request{
		Messages: messages,
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages (no compression), got %d", len(result.Messages))
	}
}

func TestHistoryMiddleware_CompressesExcessMessages(t *testing.T) {
	mw := NewHistoryMiddleware(2, true)

	messages := []pipeline.Message{
		{Role: "user", Content: "First question"},
		{Role: "assistant", Content: "First answer"},
		{Role: "user", Content: "Second question"},
		{Role: "assistant", Content: "Second answer"},
		{Role: "user", Content: "Third question"},
		{Role: "assistant", Content: "Third answer"},
	}

	req := &pipeline.Request{
		Messages: messages,
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	// Window is 2, so 4 old messages get summarised into 1 + 2 recent = 3.
	if len(result.Messages) != 3 {
		t.Fatalf("expected 3 messages (1 summary + 2 recent), got %d", len(result.Messages))
	}

	// First message should be the compressed summary.
	summaryText := ExtractText(result.Messages[0].Content)
	if !strings.Contains(summaryText, "[Compressed context from 4 earlier messages]") {
		t.Fatalf("expected compressed summary, got %q", summaryText)
	}

	// Metadata should track original and compressed counts.
	if result.Metadata == nil {
		t.Fatal("expected metadata to be set")
	}
	if result.Metadata["history_original_messages"] != 6 {
		t.Fatalf("expected original count 6, got %v", result.Metadata["history_original_messages"])
	}
	if result.Metadata["history_compressed_messages"] != 3 {
		t.Fatalf("expected compressed count 3, got %v", result.Metadata["history_compressed_messages"])
	}
}

func TestHistoryMiddleware_ToolResultTruncation(t *testing.T) {
	mw := NewHistoryMiddleware(2, true)

	// Build a tool result with >200 lines so it triggers truncation.
	var lines []string
	for i := 0; i < 250; i++ {
		lines = append(lines, fmt.Sprintf("line %d: some output data here", i))
	}
	longResult := strings.Join(lines, "\n")

	messages := []pipeline.Message{
		{Role: "user", Content: "Run the tool"},
		{Role: "assistant", Content: "Running..."},
		{Role: "user", Content: "More input"},
		{
			Role: "assistant",
			Content: []pipeline.ContentBlock{
				{Type: "tool_result", Text: longResult},
			},
		},
		{Role: "user", Content: "Latest question"},
	}

	req := &pipeline.Request{
		Messages: messages,
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	// The recent window (last 2 messages) includes the tool_result message
	// and the last user message. The tool result should be truncated.
	found := false
	for _, msg := range result.Messages {
		if blocks, ok := msg.Content.([]pipeline.ContentBlock); ok {
			for _, block := range blocks {
				if block.Type == "tool_result" && strings.Contains(block.Text, "[...truncated") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("expected tool result to be truncated with [...truncated notice")
	}
}

func TestHistoryMiddleware_NoCompressHeaderBypass(t *testing.T) {
	mw := NewHistoryMiddleware(2, true)

	messages := []pipeline.Message{
		{Role: "user", Content: "First"},
		{Role: "assistant", Content: "Reply"},
		{Role: "user", Content: "Second"},
		{Role: "assistant", Content: "Reply 2"},
		{Role: "user", Content: "Third"},
	}

	req := &pipeline.Request{
		Messages: messages,
		Headers:  map[string]string{"X-Tokenman-NoCompress": "true"},
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	// All messages should be preserved (no compression due to header).
	if len(result.Messages) != 5 {
		t.Fatalf("expected 5 messages (bypass), got %d", len(result.Messages))
	}
}

func TestHistoryMiddleware_MetadataTracksOriginalAndCompressed(t *testing.T) {
	mw := NewHistoryMiddleware(1, true)

	messages := []pipeline.Message{
		{Role: "user", Content: "Question one"},
		{Role: "assistant", Content: "Answer one"},
		{Role: "user", Content: "Question two"},
	}

	req := &pipeline.Request{
		Messages: messages,
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	if result.Metadata == nil {
		t.Fatal("expected metadata to be populated")
	}

	origCount, ok := result.Metadata["history_original_messages"]
	if !ok {
		t.Fatal("missing history_original_messages in metadata")
	}
	if origCount != 3 {
		t.Fatalf("expected original message count 3, got %v", origCount)
	}

	compCount, ok := result.Metadata["history_compressed_messages"]
	if !ok {
		t.Fatal("missing history_compressed_messages in metadata")
	}
	if compCount != 2 {
		t.Fatalf("expected compressed message count 2, got %v", compCount)
	}
}
