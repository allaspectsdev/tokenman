package security

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// injectionPattern holds a compiled regex and a human-readable category
// for a prompt injection technique.
type injectionPattern struct {
	Name    string
	Regex   *regexp.Regexp
	Category string
}

// InjectionDetection records a single detected injection attempt.
type InjectionDetection struct {
	Pattern  string `json:"pattern"`
	Category string `json:"category"`
	Match    string `json:"match"`
	Field    string `json:"field"`
}

// InjectionMiddleware is a pipeline.Middleware that scans user messages and
// tool results for prompt injection patterns.
type InjectionMiddleware struct {
	patterns []*injectionPattern
	action   string // "log", "sanitize", or "block"
	enabled  bool
}

// Compile-time assertion that InjectionMiddleware implements pipeline.Middleware.
var _ pipeline.Middleware = (*InjectionMiddleware)(nil)

// compileInjectionPatterns builds the full set of injection detection regexes.
func compileInjectionPatterns() []*injectionPattern {
	return []*injectionPattern{
		// Instruction override patterns.
		{
			Name:     "ignore_previous",
			Regex:    regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|directives?|rules?)`),
			Category: "instruction_override",
		},
		{
			Name:     "disregard_above",
			Regex:    regexp.MustCompile(`(?i)disregard\s+(all\s+)?(above|previous|prior|earlier)\s*(instructions?|prompts?|directives?|text)?`),
			Category: "instruction_override",
		},
		{
			Name:     "new_instructions",
			Regex:    regexp.MustCompile(`(?i)(new|updated|revised|real)\s+instructions?\s*:`),
			Category: "instruction_override",
		},
		{
			Name:     "system_prompt_override",
			Regex:    regexp.MustCompile(`(?i)system\s+prompt\s*:`),
			Category: "instruction_override",
		},
		{
			Name:     "forget_instructions",
			Regex:    regexp.MustCompile(`(?i)forget\s+(all\s+)?(your\s+)?(previous\s+)?(instructions?|rules?|guidelines?)`),
			Category: "instruction_override",
		},

		// Delimiter injection patterns.
		{
			Name:     "code_block_system",
			Regex:    regexp.MustCompile("(?i)```\\s*system"),
			Category: "delimiter_injection",
		},
		{
			Name:     "markdown_system",
			Regex:    regexp.MustCompile(`(?i)###\s+SYSTEM`),
			Category: "delimiter_injection",
		},
		{
			Name:     "chatml_system",
			Regex:    regexp.MustCompile(`<\|im_start\|>system`),
			Category: "delimiter_injection",
		},
		{
			Name:     "xml_system_tag",
			Regex:    regexp.MustCompile(`(?i)<system\s*>`),
			Category: "delimiter_injection",
		},
		{
			Name:     "chatml_end_start",
			Regex:    regexp.MustCompile(`<\|im_end\|>\s*<\|im_start\|>`),
			Category: "delimiter_injection",
		},

		// Role confusion patterns.
		{
			Name:     "you_are_now",
			Regex:    regexp.MustCompile(`(?i)you\s+are\s+now\s+`),
			Category: "role_confusion",
		},
		{
			Name:     "act_as_if",
			Regex:    regexp.MustCompile(`(?i)act\s+as\s+if\s+you\s+are\s+`),
			Category: "role_confusion",
		},
		{
			Name:     "pretend_you_are",
			Regex:    regexp.MustCompile(`(?i)pretend\s+(that\s+)?you\s+are\s+`),
			Category: "role_confusion",
		},
		{
			Name:     "roleplay_as",
			Regex:    regexp.MustCompile(`(?i)(roleplay|role[\-\s]play)\s+as\s+`),
			Category: "role_confusion",
		},

		// Base64-encoded injection detection.
		{
			Name:     "base64_ignore",
			Regex:    regexp.MustCompile(`(?i)aWdub3Jl`), // base64 for "ignore"
			Category: "encoded_injection",
		},
		{
			Name:     "base64_block",
			Regex:    regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`),
			Category: "encoded_injection",
		},
	}
}

// NewInjectionMiddleware creates a new InjectionMiddleware.
//
//   - action is one of "log", "sanitize", or "block".
//   - enabled controls whether the middleware is active.
func NewInjectionMiddleware(action string, enabled bool) *InjectionMiddleware {
	return &InjectionMiddleware{
		patterns: compileInjectionPatterns(),
		action:   action,
		enabled:  enabled,
	}
}

// Name returns the middleware name.
func (m *InjectionMiddleware) Name() string {
	return "injection"
}

// Enabled reports whether this middleware is active.
func (m *InjectionMiddleware) Enabled() bool {
	return m.enabled
}

// ProcessRequest scans user messages and tool_result content for injection
// patterns and takes the configured action.
func (m *InjectionMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}

	var detections []InjectionDetection

	for i := range req.Messages {
		msg := &req.Messages[i]

		// Only scan user messages and tool results.
		if msg.Role != "user" && msg.ToolCallID == "" {
			continue
		}

		field := fmt.Sprintf("messages[%d].content", i)

		switch c := msg.Content.(type) {
		case string:
			dets := m.scanText(c, field)
			detections = append(detections, dets...)
			if m.action == "sanitize" && len(dets) > 0 {
				msg.Content = m.sanitizeText(c)
			}

		case []interface{}:
			for j, block := range c {
				blockPath := fmt.Sprintf("messages[%d].content[%d]", i, j)
				blockMap, ok := block.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := blockMap["text"].(string); ok {
					dets := m.scanText(text, blockPath+".text")
					detections = append(detections, dets...)
					if m.action == "sanitize" && len(dets) > 0 {
						blockMap["text"] = m.sanitizeText(text)
						c[j] = blockMap
					}
				}
				if content, ok := blockMap["content"].(string); ok {
					dets := m.scanText(content, blockPath+".content")
					detections = append(detections, dets...)
					if m.action == "sanitize" && len(dets) > 0 {
						blockMap["content"] = m.sanitizeText(content)
						c[j] = blockMap
					}
				}
			}
			if m.action == "sanitize" {
				msg.Content = c
			}
		}
	}

	if len(detections) > 0 {
		req.Metadata["injection_detections"] = detections

		if m.action == "block" {
			categories := make(map[string]bool)
			for _, d := range detections {
				categories[d.Category] = true
			}
			catList := make([]string, 0, len(categories))
			for cat := range categories {
				catList = append(catList, cat)
			}
			return nil, fmt.Errorf("prompt injection detected: %s", strings.Join(catList, ", "))
		}
	}

	return req, nil
}

// ProcessResponse is a no-op for injection detection.
func (m *InjectionMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// scanText checks text against all injection patterns, including decoding
// base64 blocks to check their content.
func (m *InjectionMiddleware) scanText(text, field string) []InjectionDetection {
	var detections []InjectionDetection

	for _, pattern := range m.patterns {
		// Special handling for base64 blocks: decode and scan the decoded content.
		if pattern.Name == "base64_block" {
			matches := pattern.Regex.FindAllString(text, -1)
			for _, match := range matches {
				decoded, err := base64.StdEncoding.DecodeString(match)
				if err != nil {
					// Try URL-safe encoding.
					decoded, err = base64.URLEncoding.DecodeString(match)
					if err != nil {
						continue
					}
				}
				decodedStr := string(decoded)
				// Scan the decoded content against non-encoded patterns.
				for _, innerPattern := range m.patterns {
					if innerPattern.Category == "encoded_injection" {
						continue // skip to avoid recursion
					}
					if innerPattern.Regex.MatchString(decodedStr) {
						detections = append(detections, InjectionDetection{
							Pattern:  innerPattern.Name + " (base64 encoded)",
							Category: "encoded_injection",
							Match:    match,
							Field:    field,
						})
					}
				}
			}
			continue
		}

		matches := pattern.Regex.FindAllString(text, -1)
		for _, match := range matches {
			detections = append(detections, InjectionDetection{
				Pattern:  pattern.Name,
				Category: pattern.Category,
				Match:    match,
				Field:    field,
			})
		}
	}

	return detections
}

// sanitizeText removes or neutralizes detected injection patterns from text.
func (m *InjectionMiddleware) sanitizeText(text string) string {
	result := text
	for _, pattern := range m.patterns {
		if pattern.Category == "encoded_injection" && pattern.Name == "base64_block" {
			// For base64 blocks, remove only those that decode to injection content.
			matches := pattern.Regex.FindAllString(result, -1)
			for _, match := range matches {
				decoded, err := base64.StdEncoding.DecodeString(match)
				if err != nil {
					decoded, err = base64.URLEncoding.DecodeString(match)
					if err != nil {
						continue
					}
				}
				decodedStr := string(decoded)
				for _, innerPattern := range m.patterns {
					if innerPattern.Category == "encoded_injection" {
						continue
					}
					if innerPattern.Regex.MatchString(decodedStr) {
						result = strings.ReplaceAll(result, match, "[REMOVED]")
						break
					}
				}
			}
			continue
		}
		result = pattern.Regex.ReplaceAllString(result, "[REMOVED]")
	}
	return result
}
