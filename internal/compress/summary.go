package compress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// SummarizationConfig holds the configuration for the summarization middleware.
type SummarizationConfig struct {
	Enabled          bool   // Whether the middleware is active
	MaxMessages      int    // Summarize when message count exceeds this
	SummaryModel     string // Model to use for summarization (e.g., "claude-haiku-4-20250414")
	SummaryMaxTokens int    // Max tokens for summary output
	ProviderURL      string // Base URL of the summarization provider
	APIKey           string // API key for the summarization provider
}

// SummarizationMiddleware summarizes old conversation messages when the
// message count exceeds a threshold, using an LLM to produce a concise
// summary of the older portion of the conversation.
type SummarizationMiddleware struct {
	config SummarizationConfig
	client *http.Client
}

// Ensure SummarizationMiddleware satisfies pipeline.Middleware at compile time.
var _ pipeline.Middleware = (*SummarizationMiddleware)(nil)

// NewSummarizationMiddleware creates a SummarizationMiddleware with the given
// configuration.
func NewSummarizationMiddleware(cfg SummarizationConfig) *SummarizationMiddleware {
	return &SummarizationMiddleware{
		config: cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Name returns the middleware identifier.
func (s *SummarizationMiddleware) Name() string { return "summarization" }

// Enabled reports whether the middleware is active.
func (s *SummarizationMiddleware) Enabled() bool { return s.config.Enabled }

// ProcessRequest summarizes older messages when the conversation exceeds the
// configured MaxMessages threshold. If the summarization API call fails, the
// request is passed through unchanged.
func (s *SummarizationMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
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
	if totalMessages <= s.config.MaxMessages {
		return req, nil
	}

	// Split: keep the most recent half, summarize the rest.
	keepCount := s.config.MaxMessages / 2
	if keepCount < 1 {
		keepCount = 1
	}
	cutoff := totalMessages - keepCount
	oldMessages := req.Messages[:cutoff]
	recentMessages := req.Messages[cutoff:]

	// Build conversation text for the summarization prompt.
	conversationText := formatMessagesForSummary(oldMessages)

	// Call the LLM to produce a summary.
	summary, err := s.callSummarizationAPI(ctx, conversationText)
	if err != nil {
		log.Warn().Err(err).Msg("summarization API call failed; passing request through unchanged")
		return req, nil
	}

	// Determine the role for the summary message.
	summaryRole := "user"
	if req.Format == pipeline.FormatOpenAI {
		summaryRole = "system"
	}

	summaryMessage := pipeline.Message{
		Role:    summaryRole,
		Content: fmt.Sprintf("[Summary of %d earlier messages]: %s", len(oldMessages), summary),
	}

	// Assemble the new message list: summary + recent window.
	newMessages := make([]pipeline.Message, 0, 1+len(recentMessages))
	newMessages = append(newMessages, summaryMessage)
	newMessages = append(newMessages, recentMessages...)
	req.Messages = newMessages

	// Track summarization metrics.
	req.Flags["summarization_applied"] = true
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	req.Metadata["summarization_original_messages"] = totalMessages
	req.Metadata["summarization_compressed_messages"] = len(req.Messages)

	return req, nil
}

// ProcessResponse is a no-op for the summarization middleware.
func (s *SummarizationMiddleware) ProcessResponse(_ context.Context, _ *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// summarizationRequest is the request body sent to the summarization provider.
type summarizationRequest struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Messages  []summarizationMessage   `json:"messages"`
}

// summarizationMessage is a single message in the summarization request.
type summarizationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// summarizationResponse is the expected response from the Anthropic messages API.
type summarizationResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// callSummarizationAPI sends the conversation text to the configured LLM
// provider and returns the generated summary.
func (s *SummarizationMiddleware) callSummarizationAPI(ctx context.Context, conversationText string) (string, error) {
	prompt := fmt.Sprintf(
		"Summarize the following conversation concisely, preserving key facts, decisions, and context that would be needed to continue the conversation:\n\n%s",
		conversationText,
	)

	reqBody := summarizationRequest{
		Model:     s.config.SummaryModel,
		MaxTokens: s.config.SummaryMaxTokens,
		Messages: []summarizationMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling summarization request: %w", err)
	}

	url := s.config.ProviderURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("creating summarization HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", s.config.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("calling summarization API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return "", fmt.Errorf("reading summarization response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("summarization API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var sumResp summarizationResponse
	if err := json.Unmarshal(respBody, &sumResp); err != nil {
		return "", fmt.Errorf("unmarshalling summarization response: %w", err)
	}

	// Extract the text from the first content block.
	for _, block := range sumResp.Content {
		if block.Type == "text" && block.Text != "" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("summarization response contained no text content")
}

// formatMessagesForSummary converts a slice of messages into a human-readable
// text representation suitable for the summarization prompt.
func formatMessagesForSummary(messages []pipeline.Message) string {
	var buf bytes.Buffer
	for _, msg := range messages {
		text := ExtractText(msg.Content)
		if text == "" {
			continue
		}
		buf.WriteString(msg.Role)
		buf.WriteString(": ")
		// Truncate very long individual messages to keep the prompt manageable.
		if len(text) > 2000 {
			buf.WriteString(text[:2000])
			buf.WriteString("...[truncated]")
		} else {
			buf.WriteString(text)
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}
