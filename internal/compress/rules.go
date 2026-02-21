package compress

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// RulesConfig controls which text-compression rules are applied.
type RulesConfig struct {
	CollapseWhitespace bool
	MinifyJSON         bool
	MinifyXML          bool
	DedupInstructions  bool
	StripMarkdown      bool
}

// RulesMiddleware applies text-level compression rules to message content.
type RulesMiddleware struct {
	cfg     RulesConfig
	enabled bool
}

// NewRulesMiddleware creates a RulesMiddleware with the given configuration.
func NewRulesMiddleware(cfg RulesConfig) *RulesMiddleware {
	return &RulesMiddleware{
		cfg:     cfg,
		enabled: anyRuleEnabled(cfg),
	}
}

// Name returns the middleware identifier.
func (r *RulesMiddleware) Name() string { return "rules" }

// Enabled reports whether the middleware is active. It is active when at least
// one compression rule is turned on.
func (r *RulesMiddleware) Enabled() bool { return r.enabled }

// ProcessRequest applies the enabled compression rules to every message and
// the system prompt. Token savings are tracked in req.Flags.
func (r *RulesMiddleware) ProcessRequest(_ context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if req.Flags == nil {
		req.Flags = make(map[string]bool)
	}

	totalBefore := 0
	totalAfter := 0

	// Compress system prompt.
	if req.System != "" {
		before := len(req.System)
		req.System = r.applyRules(req.System)
		totalBefore += before
		totalAfter += len(req.System)
	}

	// Compress system blocks.
	for i, block := range req.SystemBlocks {
		if block.Text != "" {
			before := len(block.Text)
			req.SystemBlocks[i].Text = r.applyRules(block.Text)
			totalBefore += before
			totalAfter += len(req.SystemBlocks[i].Text)
		}
	}

	// Compress message content.
	for i, msg := range req.Messages {
		switch v := msg.Content.(type) {
		case string:
			before := len(v)
			compressed := r.applyRules(v)
			req.Messages[i].Content = compressed
			totalBefore += before
			totalAfter += len(compressed)

		case []pipeline.ContentBlock:
			for j, block := range v {
				if block.Type == "text" || block.Type == "" {
					before := len(block.Text)
					v[j].Text = r.applyRules(block.Text)
					totalBefore += before
					totalAfter += len(v[j].Text)
				}
			}
			req.Messages[i].Content = v

		case []interface{}:
			for j, item := range v {
				if blockMap, ok := item.(map[string]interface{}); ok {
					blockType, _ := blockMap["type"].(string)
					if blockType == "text" || blockType == "" {
						if text, ok := blockMap["text"].(string); ok {
							before := len(text)
							blockMap["text"] = r.applyRules(text)
							totalBefore += before
							totalAfter += len(blockMap["text"].(string))
						}
					}
					v[j] = blockMap
				}
			}
			req.Messages[i].Content = v
		}
	}

	// Dedup instructions across messages.
	if r.cfg.DedupInstructions {
		req.Messages = dedupInstructions(req.Messages)
	}

	// Track savings.
	saved := totalBefore - totalAfter
	if saved > 0 {
		req.Flags["rules_tokens_saved"] = true
		if req.Metadata == nil {
			req.Metadata = make(map[string]interface{})
		}
		// Store approximate token savings (chars / 4).
		tokensSaved := saved / 4
		if tokensSaved == 0 {
			tokensSaved = 1
		}
		req.Metadata["rules_tokens_saved"] = tokensSaved
	}

	return req, nil
}

// ProcessResponse is a no-op for the rules middleware.
func (r *RulesMiddleware) ProcessResponse(_ context.Context, _ *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	return resp, nil
}

// applyRules runs the enabled compression rules in sequence on the input.
func (r *RulesMiddleware) applyRules(s string) string {
	if r.cfg.CollapseWhitespace {
		s = collapseWhitespace(s)
	}
	if r.cfg.MinifyJSON {
		s = minifyJSON(s)
	}
	if r.cfg.MinifyXML {
		s = minifyXML(s)
	}
	if r.cfg.StripMarkdown {
		s = stripMarkdown(s)
	}
	return s
}

// ---------------------------------------------------------------------------
// Individual compression rules
// ---------------------------------------------------------------------------

// codeBlockRe matches fenced code blocks (``` ... ```).
var codeBlockRe = regexp.MustCompile("(?s)```[^\n]*\n.*?```")

// indentedBlockRe matches lines that start with 4+ spaces (indented code).
var indentedBlockRe = regexp.MustCompile(`(?m)^(    .+\n?)+`)

// collapseWhitespace reduces multiple spaces to one, multiple blank lines to
// at most two newlines, converts tabs to spaces, and trims trailing whitespace
// from each line. Code blocks (fenced with ``` or indented by 4+ spaces) are
// preserved verbatim.
func collapseWhitespace(s string) string {
	// Collect all code-block ranges.
	var spans []span
	for _, loc := range codeBlockRe.FindAllStringIndex(s, -1) {
		spans = append(spans, span{loc[0], loc[1], true})
	}
	for _, loc := range indentedBlockRe.FindAllStringIndex(s, -1) {
		spans = append(spans, span{loc[0], loc[1], true})
	}

	if len(spans) == 0 {
		return collapseWhitespaceRaw(s)
	}

	// Sort spans by start position and merge overlaps.
	spans = mergeSpans(spans)

	// Process text outside code blocks; preserve code blocks.
	var b strings.Builder
	b.Grow(len(s))
	cursor := 0
	for _, sp := range spans {
		if cursor < sp.start {
			b.WriteString(collapseWhitespaceRaw(s[cursor:sp.start]))
		}
		b.WriteString(s[sp.start:sp.end])
		cursor = sp.end
	}
	if cursor < len(s) {
		b.WriteString(collapseWhitespaceRaw(s[cursor:]))
	}
	return b.String()
}

// collapseWhitespaceRaw performs whitespace collapsing on a plain text
// string that does not contain code blocks.
func collapseWhitespaceRaw(s string) string {
	// Tabs to spaces.
	s = strings.ReplaceAll(s, "\t", " ")

	// Collapse runs of spaces (not newlines) to a single space.
	s = multiSpaceRe.ReplaceAllString(s, " ")

	// Trim trailing whitespace per line.
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRightFunc(lines[i], unicode.IsSpace)
	}
	s = strings.Join(lines, "\n")

	// Collapse 3+ consecutive newlines into exactly 2.
	s = multiNewlineRe.ReplaceAllString(s, "\n\n")

	return s
}

var multiSpaceRe = regexp.MustCompile(`[^\S\n]{2,}`)
var multiNewlineRe = regexp.MustCompile(`\n{3,}`)

// mergeSpans sorts span slices by start and merges overlapping ranges.
func mergeSpans(spans []span) []span {
	if len(spans) <= 1 {
		return spans
	}

	// Simple insertion sort (spans are typically few).
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].start < spans[j-1].start; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}

	merged := []span{spans[0]}
	for _, sp := range spans[1:] {
		last := &merged[len(merged)-1]
		if sp.start <= last.end {
			if sp.end > last.end {
				last.end = sp.end
			}
		} else {
			merged = append(merged, sp)
		}
	}
	return merged
}

type span struct {
	start, end int
	isCode     bool
}

// jsonObjectRe matches top-level JSON objects or arrays in text.
var jsonObjectRe = regexp.MustCompile(`(?s)(\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}|\[[^\[\]]*(?:\[[^\[\]]*\][^\[\]]*)*\])`)

// minifyJSON finds JSON objects and arrays embedded in text, parses them, and
// re-serialises in compact form. If parsing fails the original text is kept.
func minifyJSON(s string) string {
	return jsonObjectRe.ReplaceAllStringFunc(s, func(match string) string {
		match = strings.TrimSpace(match)
		// Quick sanity: must start with { or [.
		if len(match) == 0 {
			return match
		}
		if match[0] != '{' && match[0] != '[' {
			return match
		}

		var parsed interface{}
		dec := json.NewDecoder(strings.NewReader(match))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err != nil {
			return match
		}

		compact, err := json.Marshal(parsed)
		if err != nil {
			return match
		}
		return string(compact)
	})
}

// xmlWhitespaceRe matches whitespace between XML tags.
var xmlWhitespaceRe = regexp.MustCompile(`>\s+<`)

// xmlLeadTrailRe matches leading/trailing whitespace inside text nodes.
var xmlLeadTrailRe = regexp.MustCompile(`>\s+([^<])`)

// minifyXML removes extraneous whitespace between XML tags. Content within
// tags is left intact apart from collapsing whitespace between adjacent tags.
func minifyXML(s string) string {
	// Only operate if the string looks like it contains XML.
	if !strings.Contains(s, "<") || !strings.Contains(s, ">") {
		return s
	}

	s = xmlWhitespaceRe.ReplaceAllString(s, "><")
	// Preserve a single space when text follows a tag.
	s = xmlLeadTrailRe.ReplaceAllString(s, "> $1")
	return s
}

// dedupInstructions detects identical instruction text across messages and
// replaces duplicates with a short back-reference. The first occurrence is
// kept verbatim.
func dedupInstructions(messages []pipeline.Message) []pipeline.Message {
	seen := make(map[string]int) // text -> index of first occurrence

	for i, msg := range messages {
		text := ExtractText(msg.Content)
		if text == "" {
			continue
		}

		// Only consider messages with substantial content (>80 chars) to
		// avoid false-positive dedup of short phrases like "yes" or "ok".
		if len(text) < 80 {
			continue
		}

		hash := HashContent(text)
		if firstIdx, exists := seen[hash]; exists {
			replacement := fmt.Sprintf("[See instructions above (message %d)]", firstIdx+1)
			messages[i].Content = replacement
		} else {
			seen[hash] = i
		}
	}

	return messages
}

// mdHeaderRe matches Markdown heading markers (# to ######).
var mdHeaderRe = regexp.MustCompile(`(?m)^#{1,6}\s+`)

// mdBoldRe matches **bold** or __bold__.
var mdBoldRe = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)

// mdItalicRe matches *italic* or _italic_ (but not inside words for underscores).
var mdItalicRe = regexp.MustCompile(`(?:^|[^\\*_])(\*|_)([^\s*_](?:.*?[^\s*_])?)(\*|_)`)

// mdStrikethroughRe matches ~~strikethrough~~.
var mdStrikethroughRe = regexp.MustCompile(`~~(.+?)~~`)

// mdInlineCodeRe matches `inline code`.
var mdInlineCodeRe = regexp.MustCompile("`([^`]+)`")

// stripMarkdown removes Markdown formatting characters while preserving the
// text content. Fenced code blocks are left untouched.
func stripMarkdown(s string) string {
	// Preserve fenced code blocks.
	var codeBlocks []string
	s = codeBlockRe.ReplaceAllStringFunc(s, func(match string) string {
		placeholder := fmt.Sprintf("\x00CODEBLOCK_%d\x00", len(codeBlocks))
		codeBlocks = append(codeBlocks, match)
		return placeholder
	})

	// Remove heading markers.
	s = mdHeaderRe.ReplaceAllString(s, "")

	// Bold: **text** or __text__ -> text
	s = mdBoldRe.ReplaceAllString(s, "$2")

	// Strikethrough: ~~text~~ -> text
	s = mdStrikethroughRe.ReplaceAllString(s, "$1")

	// Inline code: `code` -> code
	s = mdInlineCodeRe.ReplaceAllString(s, "$1")

	// Italic: *text* or _text_ -> text (after bold to avoid conflicts).
	// Use a simple approach: remove lone * or _ used for emphasis.
	s = mdItalicRe.ReplaceAllStringFunc(s, func(match string) string {
		// Keep leading character if it was captured as context.
		for i, r := range match {
			if r == '*' || r == '_' {
				// Strip the opening marker, find the closing one.
				inner := match[i+1:]
				if len(inner) > 0 {
					marker := inner[len(inner)-1]
					if marker == '*' || marker == '_' {
						inner = inner[:len(inner)-1]
					}
				}
				return match[:i] + inner
			}
		}
		return match
	})

	// Restore code blocks.
	for i, block := range codeBlocks {
		placeholder := fmt.Sprintf("\x00CODEBLOCK_%d\x00", i)
		s = strings.ReplaceAll(s, placeholder, block)
	}

	return s
}

// anyRuleEnabled returns true if at least one rule flag is set.
func anyRuleEnabled(cfg RulesConfig) bool {
	return cfg.CollapseWhitespace ||
		cfg.MinifyJSON ||
		cfg.MinifyXML ||
		cfg.DedupInstructions ||
		cfg.StripMarkdown
}

// Ensure RulesMiddleware satisfies pipeline.Middleware at compile time.
var _ pipeline.Middleware = (*RulesMiddleware)(nil)
