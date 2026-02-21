package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// mockFlusher wraps httptest.ResponseRecorder and satisfies http.Flusher.
type mockFlusher struct {
	*httptest.ResponseRecorder
}

func (f *mockFlusher) Flush() {
	// no-op for testing
}

func newFlushableRecorder() *mockFlusher {
	return &mockFlusher{httptest.NewRecorder()}
}

func buildSSEBody(events []string) io.ReadCloser {
	var sb strings.Builder
	for _, e := range events {
		sb.WriteString("data: " + e + "\n\n")
	}
	return io.NopCloser(strings.NewReader(sb.String()))
}

func TestHandleStreaming_Anthropic(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":" World"}}`,
		`[DONE]`,
	}

	body := buildSSEBody(events)
	resp := &http.Response{
		StatusCode: 200,
		Body:       body,
		Header:     http.Header{},
	}

	w := newFlushableRecorder()
	ctx := context.Background()

	pipeResp, err := HandleStreaming(ctx, w, resp, pipeline.FormatAnthropic, 0)
	if err != nil {
		t.Fatalf("HandleStreaming: %v", err)
	}

	if pipeResp.StatusCode != 200 {
		t.Errorf("StatusCode: got %d, want 200", pipeResp.StatusCode)
	}
	if pipeResp.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Model: got %q, want %q", pipeResp.Model, "claude-sonnet-4-20250514")
	}
	if !pipeResp.Streaming {
		t.Error("Streaming: got false, want true")
	}

	body_str := string(pipeResp.Body)
	if body_str != "Hello World" {
		t.Errorf("accumulated body: got %q, want %q", body_str, "Hello World")
	}

	// Verify SSE headers were set.
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want %q", w.Header().Get("Content-Type"), "text/event-stream")
	}
}

func TestHandleStreaming_OpenAI(t *testing.T) {
	events := []string{
		`{"model":"gpt-4o","choices":[{"delta":{"content":"Hello"}}]}`,
		`{"model":"gpt-4o","choices":[{"delta":{"content":" GPT"}}]}`,
		`[DONE]`,
	}

	body := buildSSEBody(events)
	resp := &http.Response{
		StatusCode: 200,
		Body:       body,
		Header:     http.Header{},
	}

	w := newFlushableRecorder()
	ctx := context.Background()

	pipeResp, err := HandleStreaming(ctx, w, resp, pipeline.FormatOpenAI, 0)
	if err != nil {
		t.Fatalf("HandleStreaming: %v", err)
	}

	if pipeResp.Model != "gpt-4o" {
		t.Errorf("Model: got %q, want %q", pipeResp.Model, "gpt-4o")
	}
	if string(pipeResp.Body) != "Hello GPT" {
		t.Errorf("accumulated body: got %q, want %q", string(pipeResp.Body), "Hello GPT")
	}
}

func TestHandleStreaming_AccumulatorCap(t *testing.T) {
	events := []string{
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"AAAA"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"BBBB"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"CCCC"}}`,
	}

	body := buildSSEBody(events)
	resp := &http.Response{
		StatusCode: 200,
		Body:       body,
		Header:     http.Header{},
	}

	w := newFlushableRecorder()
	ctx := context.Background()

	// Cap at 5 bytes — should accumulate "AAAA" then stop after adding "BBBB" (8 > 5).
	pipeResp, err := HandleStreaming(ctx, w, resp, pipeline.FormatAnthropic, 5)
	if err != nil {
		t.Fatalf("HandleStreaming: %v", err)
	}

	// Content should be at most "AAAABBBB" (capped after exceeding, CCCC not accumulated).
	accumulated := string(pipeResp.Body)
	if strings.Contains(accumulated, "CCCC") {
		t.Errorf("accumulated should not contain CCCC (cap exceeded), got %q", accumulated)
	}

	// But events should still have been forwarded to the client.
	responseBody := w.Body.String()
	if !strings.Contains(responseBody, "CCCC") {
		t.Error("all events should be forwarded to client regardless of accumulator cap")
	}
}

func TestHandleStreaming_ContextCancellation(t *testing.T) {
	// Create a slow stream that we'll cancel.
	pr, pw := io.Pipe()

	go func() {
		pw.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n"))
		// Don't close — simulate a slow stream.
	}()

	resp := &http.Response{
		StatusCode: 200,
		Body:       pr,
		Header:     http.Header{},
	}

	w := newFlushableRecorder()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var pipeResp *pipeline.Response
	var streamErr error

	go func() {
		pipeResp, streamErr = HandleStreaming(ctx, w, resp, pipeline.FormatAnthropic, 0)
		close(done)
	}()

	// Cancel the context to simulate timeout.
	cancel()
	// Also close the pipe to unblock the reader.
	pw.Close()

	<-done

	// Either we get a context error or the stream ends normally.
	if streamErr != nil && streamErr != context.Canceled {
		// streamErr could be a read error from the closed pipe, which is acceptable.
		_ = streamErr
	}

	if pipeResp == nil {
		t.Fatal("pipeResp should not be nil")
	}
	if !pipeResp.Streaming {
		t.Error("Streaming should be true")
	}
}

func TestHandleStreaming_EmptyStream(t *testing.T) {
	body := io.NopCloser(strings.NewReader(""))
	resp := &http.Response{
		StatusCode: 200,
		Body:       body,
		Header:     http.Header{},
	}

	w := newFlushableRecorder()
	ctx := context.Background()

	pipeResp, err := HandleStreaming(ctx, w, resp, pipeline.FormatAnthropic, 0)
	if err != nil {
		t.Fatalf("HandleStreaming empty: %v", err)
	}
	if string(pipeResp.Body) != "" {
		t.Errorf("empty stream body: got %q, want empty", string(pipeResp.Body))
	}
}

func TestExtractDelta_Anthropic(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantDelta string
		wantModel string
	}{
		{
			name:      "text delta",
			data:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			wantDelta: "hello",
		},
		{
			name:      "message start",
			data:      `{"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}`,
			wantModel: "claude-sonnet-4-20250514",
		},
		{
			name: "other event",
			data: `{"type":"content_block_start"}`,
		},
		{
			name: "invalid json",
			data: `not json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, model := extractDelta(tt.data, pipeline.FormatAnthropic)
			if delta != tt.wantDelta {
				t.Errorf("delta: got %q, want %q", delta, tt.wantDelta)
			}
			if model != tt.wantModel {
				t.Errorf("model: got %q, want %q", model, tt.wantModel)
			}
		})
	}
}

func TestExtractDelta_OpenAI(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantDelta string
		wantModel string
	}{
		{
			name:      "content delta",
			data:      `{"model":"gpt-4o","choices":[{"delta":{"content":"hi"}}]}`,
			wantDelta: "hi",
			wantModel: "gpt-4o",
		},
		{
			name:      "empty choices",
			data:      `{"model":"gpt-4o","choices":[]}`,
			wantModel: "gpt-4o",
		},
		{
			name: "invalid json",
			data: `broken`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, model := extractDelta(tt.data, pipeline.FormatOpenAI)
			if delta != tt.wantDelta {
				t.Errorf("delta: got %q, want %q", delta, tt.wantDelta)
			}
			if model != tt.wantModel {
				t.Errorf("model: got %q, want %q", model, tt.wantModel)
			}
		})
	}
}

func TestExtractDelta_UnknownFormat(t *testing.T) {
	delta, model := extractDelta(`{"data":"test"}`, pipeline.FormatUnknown)
	if delta != "" || model != "" {
		t.Errorf("unknown format should return empty, got delta=%q model=%q", delta, model)
	}
}
