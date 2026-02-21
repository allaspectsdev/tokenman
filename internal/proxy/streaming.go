package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// HandleStreaming processes a streaming upstream response by forwarding SSE events
// to the client while accumulating content deltas for token counting.
// It returns a pipeline.Response with the accumulated content in the Body field.
// maxAccumulatorSize caps the internal accumulator; when exceeded, events are
// still forwarded to the client but accumulation stops (0 means unlimited).
func HandleStreaming(ctx context.Context, w http.ResponseWriter, upstreamResp *http.Response, format pipeline.APIFormat, maxAccumulatorSize int64) (*pipeline.Response, error) {
	// Set SSE response headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(upstreamResp.StatusCode)

	reader := NewSSEReader(upstreamResp.Body)
	writer := NewSSEWriter(w)

	var contentAccumulator strings.Builder
	var model string
	accumulatorCapped := false

	for {
		// Check for client disconnect.
		select {
		case <-ctx.Done():
			return buildStreamingResponse(upstreamResp.StatusCode, model, contentAccumulator.String()), ctx.Err()
		default:
		}

		evt, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return buildStreamingResponse(upstreamResp.StatusCode, model, contentAccumulator.String()), err
		}

		// Forward the event to the client.
		if writeErr := writer.WriteEvent(evt); writeErr != nil {
			return buildStreamingResponse(upstreamResp.StatusCode, model, contentAccumulator.String()), writeErr
		}

		// Extract content deltas for accumulation based on the API format.
		if evt.Data != "" && evt.Data != "[DONE]" {
			delta, m := extractDelta(evt.Data, format)
			if delta != "" && !accumulatorCapped {
				contentAccumulator.WriteString(delta)
				if maxAccumulatorSize > 0 && int64(contentAccumulator.Len()) > maxAccumulatorSize {
					accumulatorCapped = true
				}
			}
			if m != "" {
				model = m
			}
		}
	}

	return buildStreamingResponse(upstreamResp.StatusCode, model, contentAccumulator.String()), nil
}

// buildStreamingResponse constructs a pipeline.Response from the accumulated stream data.
func buildStreamingResponse(statusCode int, model, content string) *pipeline.Response {
	return &pipeline.Response{
		StatusCode: statusCode,
		Model:      model,
		Streaming:  true,
		Body:       []byte(content),
		Flags:      make(map[string]bool),
	}
}

// extractDelta parses a single SSE data payload and extracts the content delta
// text and model name based on the API format.
func extractDelta(data string, format pipeline.APIFormat) (delta, model string) {
	switch format {
	case pipeline.FormatAnthropic:
		return extractAnthropicDelta(data)
	case pipeline.FormatOpenAI:
		return extractOpenAIDelta(data)
	default:
		return "", ""
	}
}

// anthropicStreamEvent is a minimal representation of an Anthropic streaming event.
type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Message struct {
		Model string `json:"model"`
	} `json:"message"`
}

// extractAnthropicDelta extracts content from an Anthropic streaming event.
// It looks for events of type "content_block_delta" with delta.text content,
// and "message_start" for the model name.
func extractAnthropicDelta(data string) (delta, model string) {
	var evt anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return "", ""
	}

	switch evt.Type {
	case "content_block_delta":
		if evt.Delta.Type == "text_delta" {
			return evt.Delta.Text, ""
		}
	case "message_start":
		return "", evt.Message.Model
	}

	return "", ""
}

// openaiStreamChunk is a minimal representation of an OpenAI streaming chunk.
type openaiStreamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// extractOpenAIDelta extracts content from an OpenAI streaming chunk.
// It reads from choices[0].delta.content and the model field.
func extractOpenAIDelta(data string) (delta, model string) {
	var chunk openaiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return "", ""
	}

	model = chunk.Model
	if len(chunk.Choices) > 0 {
		delta = chunk.Choices[0].Delta.Content
	}
	return delta, model
}
