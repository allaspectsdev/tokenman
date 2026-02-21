package router

import (
	"encoding/json"
	"fmt"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// --------------------------------------------------------------------------
// Anthropic JSON wire types
// --------------------------------------------------------------------------

type anthropicRequest struct {
	Model       string                   `json:"model"`
	Messages    []anthropicMessage       `json:"messages"`
	System      interface{}              `json:"system,omitempty"`
	Tools       []anthropicTool          `json:"tools,omitempty"`
	Stream      bool                     `json:"stream,omitempty"`
	MaxTokens   int                      `json:"max_tokens"`
	Temperature *float64                 `json:"temperature,omitempty"`
	Metadata    map[string]interface{}   `json:"metadata,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

type anthropicResponse struct {
	ID           string                  `json:"id,omitempty"`
	Type         string                  `json:"type,omitempty"`
	Role         string                  `json:"role,omitempty"`
	Content      []anthropicContentBlock `json:"content,omitempty"`
	Model        string                  `json:"model,omitempty"`
	StopReason   string                  `json:"stop_reason,omitempty"`
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        *anthropicUsage         `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --------------------------------------------------------------------------
// OpenAI JSON wire types
// --------------------------------------------------------------------------

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Tools       []openaiTool    `json:"tools,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    interface{}      `json:"content"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiTool struct {
	Type     string            `json:"type"`
	Function openaiToolDefFunc `json:"function"`
}

type openaiToolDefFunc struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type openaiResponse struct {
	ID      string         `json:"id,omitempty"`
	Object  string         `json:"object,omitempty"`
	Created int64          `json:"created,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []openaiChoice `json:"choices,omitempty"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --------------------------------------------------------------------------
// TranslateRequest translates a normalized pipeline.Request from one API
// format to another, returning the serialized JSON body for the target format.
// --------------------------------------------------------------------------
func TranslateRequest(req *pipeline.Request, from, to pipeline.APIFormat) ([]byte, error) {
	if from == to {
		return BuildRequestBody(req, to)
	}

	switch {
	case from == pipeline.FormatAnthropic && to == pipeline.FormatOpenAI:
		return translateAnthropicToOpenAI(req)
	case from == pipeline.FormatOpenAI && to == pipeline.FormatAnthropic:
		return translateOpenAIToAnthropic(req)
	default:
		return nil, fmt.Errorf("unsupported translation: %s -> %s", from, to)
	}
}

// translateAnthropicToOpenAI converts an Anthropic-format request to OpenAI format.
func translateAnthropicToOpenAI(req *pipeline.Request) ([]byte, error) {
	oReq := openaiRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		oReq.MaxTokens = &mt
	}

	// System prompt becomes a system message at the beginning.
	if req.System != "" {
		oReq.Messages = append(oReq.Messages, openaiMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	// Convert messages.
	for _, msg := range req.Messages {
		oMsg := openaiMessage{
			Role: msg.Role,
			Name: msg.Name,
		}

		// Convert Anthropic content blocks to OpenAI format.
		switch c := msg.Content.(type) {
		case string:
			oMsg.Content = c
		case []interface{}:
			// Content is an array of blocks; check for tool_use and tool_result.
			blocks, err := parseContentBlocks(c)
			if err != nil {
				oMsg.Content = c
			} else {
				oMsg, err = convertAnthropicBlocksToOpenAI(oMsg, blocks)
				if err != nil {
					return nil, fmt.Errorf("converting content blocks: %w", err)
				}
			}
		default:
			oMsg.Content = c
		}

		// Convert tool_calls from Anthropic format.
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				oMsg.ToolCalls = append(oMsg.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiToolFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}

		// Tool results: in Anthropic the role is "user" with tool_result blocks.
		// In OpenAI, tool results use role "tool" with tool_call_id.
		if msg.ToolCallID != "" {
			oMsg.Role = "tool"
			oMsg.ToolCallID = msg.ToolCallID
		}

		oReq.Messages = append(oReq.Messages, oMsg)
	}

	// Convert tools.
	for _, t := range req.Tools {
		oReq.Tools = append(oReq.Tools, openaiTool{
			Type: "function",
			Function: openaiToolDefFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return json.Marshal(oReq)
}

// translateOpenAIToAnthropic converts an OpenAI-format request to Anthropic format.
func translateOpenAIToAnthropic(req *pipeline.Request) ([]byte, error) {
	aReq := anthropicRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	if req.MaxTokens == 0 {
		aReq.MaxTokens = 4096 // Anthropic requires max_tokens
	}

	// Extract system messages and convert remaining messages.
	var systemParts []string
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			if s, ok := msg.Content.(string); ok {
				systemParts = append(systemParts, s)
			}
			continue
		}

		aMsg := anthropicMessage{
			Role: msg.Role,
		}

		// OpenAI "tool" role maps to Anthropic tool_result content blocks.
		if msg.Role == "tool" {
			contentStr := ""
			if s, ok := msg.Content.(string); ok {
				contentStr = s
			}
			aMsg.Role = "user"
			aMsg.Content = []anthropicContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: msg.ToolCallID,
					Content:   contentStr,
				},
			}
			aReq.Messages = append(aReq.Messages, aMsg)
			continue
		}

		// Convert function_call tool_calls to tool_use content blocks.
		if len(msg.ToolCalls) > 0 {
			var blocks []anthropicContentBlock
			// Preserve any text content.
			if s, ok := msg.Content.(string); ok && s != "" {
				blocks = append(blocks, anthropicContentBlock{
					Type: "text",
					Text: s,
				})
			}
			for _, tc := range msg.ToolCalls {
				var input interface{}
				if tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
						input = tc.Function.Arguments
					}
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			aMsg.Content = blocks
			aReq.Messages = append(aReq.Messages, aMsg)
			continue
		}

		// Regular message.
		aMsg.Content = msg.Content
		aReq.Messages = append(aReq.Messages, aMsg)
	}

	// Set system prompt (prefer explicit system field, fall back to extracted system messages).
	if req.System != "" {
		aReq.System = req.System
	} else if len(systemParts) > 0 {
		combined := ""
		for i, part := range systemParts {
			if i > 0 {
				combined += "\n"
			}
			combined += part
		}
		aReq.System = combined
	}

	// Convert tools.
	for _, t := range req.Tools {
		schema := t.InputSchema
		if schema == nil {
			schema = t.Function
		}
		aReq.Tools = append(aReq.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}

	return json.Marshal(aReq)
}

// parseContentBlocks attempts to parse a []interface{} as content blocks.
func parseContentBlocks(raw []interface{}) ([]anthropicContentBlock, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// convertAnthropicBlocksToOpenAI processes Anthropic content blocks into an
// OpenAI message. Text blocks become message content; tool_use blocks become
// tool_calls on the message.
func convertAnthropicBlocksToOpenAI(oMsg openaiMessage, blocks []anthropicContentBlock) (openaiMessage, error) {
	var textParts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			args := ""
			if block.Input != nil {
				b, err := json.Marshal(block.Input)
				if err != nil {
					return oMsg, fmt.Errorf("marshalling tool_use input: %w", err)
				}
				args = string(b)
			}
			oMsg.ToolCalls = append(oMsg.ToolCalls, openaiToolCall{
				ID:   block.ID,
				Type: "function",
				Function: openaiToolFunction{
					Name:      block.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			// Tool results in blocks: convert to tool message.
			oMsg.Role = "tool"
			oMsg.ToolCallID = block.ToolUseID
			if s, ok := block.Content.(string); ok {
				oMsg.Content = s
			}
		}
	}

	if len(textParts) > 0 {
		combined := ""
		for i, part := range textParts {
			if i > 0 {
				combined += "\n"
			}
			combined += part
		}
		oMsg.Content = combined
	}

	return oMsg, nil
}

// --------------------------------------------------------------------------
// TranslateResponse translates a JSON response body from one API format to
// another.
// --------------------------------------------------------------------------
func TranslateResponse(body []byte, from, to pipeline.APIFormat) ([]byte, error) {
	if from == to {
		return body, nil
	}

	switch {
	case from == pipeline.FormatAnthropic && to == pipeline.FormatOpenAI:
		return translateAnthropicResponseToOpenAI(body)
	case from == pipeline.FormatOpenAI && to == pipeline.FormatAnthropic:
		return translateOpenAIResponseToAnthropic(body)
	default:
		return nil, fmt.Errorf("unsupported response translation: %s -> %s", from, to)
	}
}

func translateAnthropicResponseToOpenAI(body []byte) ([]byte, error) {
	var aResp anthropicResponse
	if err := json.Unmarshal(body, &aResp); err != nil {
		return nil, fmt.Errorf("parsing anthropic response: %w", err)
	}

	oResp := openaiResponse{
		ID:     aResp.ID,
		Object: "chat.completion",
		Model:  aResp.Model,
	}

	oMsg := openaiMessage{
		Role: "assistant",
	}

	var textParts []string
	for _, block := range aResp.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			args := ""
			if block.Input != nil {
				b, err := json.Marshal(block.Input)
				if err != nil {
					return nil, fmt.Errorf("marshalling tool_use input: %w", err)
				}
				args = string(b)
			}
			oMsg.ToolCalls = append(oMsg.ToolCalls, openaiToolCall{
				ID:   block.ID,
				Type: "function",
				Function: openaiToolFunction{
					Name:      block.Name,
					Arguments: args,
				},
			})
		}
	}

	if len(textParts) > 0 {
		combined := ""
		for i, part := range textParts {
			if i > 0 {
				combined += "\n"
			}
			combined += part
		}
		oMsg.Content = combined
	} else {
		oMsg.Content = ""
	}

	finishReason := mapAnthropicStopReason(aResp.StopReason)

	oResp.Choices = []openaiChoice{
		{
			Index:        0,
			Message:      oMsg,
			FinishReason: finishReason,
		},
	}

	if aResp.Usage != nil {
		oResp.Usage = &openaiUsage{
			PromptTokens:     aResp.Usage.InputTokens,
			CompletionTokens: aResp.Usage.OutputTokens,
			TotalTokens:      aResp.Usage.InputTokens + aResp.Usage.OutputTokens,
		}
	}

	return json.Marshal(oResp)
}

func translateOpenAIResponseToAnthropic(body []byte) ([]byte, error) {
	var oResp openaiResponse
	if err := json.Unmarshal(body, &oResp); err != nil {
		return nil, fmt.Errorf("parsing openai response: %w", err)
	}

	aResp := anthropicResponse{
		ID:    oResp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: oResp.Model,
	}

	if len(oResp.Choices) > 0 {
		choice := oResp.Choices[0]
		aResp.StopReason = mapOpenAIFinishReason(choice.FinishReason)

		// Convert message content.
		if s, ok := choice.Message.Content.(string); ok && s != "" {
			aResp.Content = append(aResp.Content, anthropicContentBlock{
				Type: "text",
				Text: s,
			})
		}

		// Convert tool calls to tool_use blocks.
		for _, tc := range choice.Message.ToolCalls {
			var input interface{}
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
					input = tc.Function.Arguments
				}
			}
			aResp.Content = append(aResp.Content, anthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: input,
			})
		}
	}

	if oResp.Usage != nil {
		aResp.Usage = &anthropicUsage{
			InputTokens:  oResp.Usage.PromptTokens,
			OutputTokens: oResp.Usage.CompletionTokens,
		}
	}

	return json.Marshal(aResp)
}

// mapAnthropicStopReason converts an Anthropic stop_reason to an OpenAI finish_reason.
func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}

// mapOpenAIFinishReason converts an OpenAI finish_reason to an Anthropic stop_reason.
func mapOpenAIFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return reason
	}
}

// --------------------------------------------------------------------------
// BuildRequestBody serializes a pipeline.Request to JSON in the target format.
// --------------------------------------------------------------------------
func BuildRequestBody(req *pipeline.Request, format pipeline.APIFormat) ([]byte, error) {
	switch format {
	case pipeline.FormatAnthropic:
		return buildAnthropicBody(req)
	case pipeline.FormatOpenAI:
		return buildOpenAIBody(req)
	default:
		return nil, fmt.Errorf("unsupported format for body building: %s", format)
	}
}

func buildAnthropicBody(req *pipeline.Request) ([]byte, error) {
	aReq := anthropicRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	if req.MaxTokens == 0 {
		aReq.MaxTokens = 4096
	}

	if req.System != "" {
		aReq.System = req.System
	}

	for _, msg := range req.Messages {
		aReq.Messages = append(aReq.Messages, anthropicMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	for _, t := range req.Tools {
		aReq.Tools = append(aReq.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	if req.Metadata != nil {
		aReq.Metadata = req.Metadata
	}

	return json.Marshal(aReq)
}

func buildOpenAIBody(req *pipeline.Request) ([]byte, error) {
	oReq := openaiRequest{
		Model:       req.Model,
		Stream:      req.Stream,
		Temperature: req.Temperature,
	}

	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		oReq.MaxTokens = &mt
	}

	// Include system prompt as a system message.
	if req.System != "" {
		oReq.Messages = append(oReq.Messages, openaiMessage{
			Role:    "system",
			Content: req.System,
		})
	}

	for _, msg := range req.Messages {
		oMsg := openaiMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				oMsg.ToolCalls = append(oMsg.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: openaiToolFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		oReq.Messages = append(oReq.Messages, oMsg)
	}

	for _, t := range req.Tools {
		oReq.Tools = append(oReq.Tools, openaiTool{
			Type: "function",
			Function: openaiToolDefFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return json.Marshal(oReq)
}
