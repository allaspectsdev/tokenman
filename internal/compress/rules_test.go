package compress

import (
	"context"
	"strings"
	"testing"

	"github.com/allaspects/tokenman/internal/pipeline"
)

func TestCollapseWhitespace(t *testing.T) {
	input := "Hello    world.  \t How   are   you?\n\n\n\nFine."
	result := collapseWhitespace(input)

	if strings.Contains(result, "    ") {
		t.Fatalf("expected multiple spaces to be collapsed, got %q", result)
	}
	if strings.Contains(result, "\t") {
		t.Fatalf("expected tabs to be replaced, got %q", result)
	}
	// Should collapse 4 newlines into 2.
	if strings.Contains(result, "\n\n\n") {
		t.Fatalf("expected multiple blank lines to be collapsed, got %q", result)
	}
	if !strings.Contains(result, "Hello world.") {
		t.Fatalf("expected 'Hello world.' in result, got %q", result)
	}
}

func TestMinifyJSON(t *testing.T) {
	input := `Here is some JSON:
{
  "name":  "Alice",
  "age":   30
}
End.`

	result := minifyJSON(input)

	// The JSON object should be compacted.
	if strings.Contains(result, "  \"name\"") {
		t.Fatalf("expected JSON to be compacted, got %q", result)
	}
	if !strings.Contains(result, `{"age":30,"name":"Alice"}`) && !strings.Contains(result, `{"name":"Alice","age":30}`) {
		t.Fatalf("expected compacted JSON in result, got %q", result)
	}
}

func TestMinifyXML(t *testing.T) {
	input := `<root>
    <child>
        text
    </child>
</root>`

	result := minifyXML(input)

	// Whitespace between tags should be removed.
	if strings.Contains(result, ">\n    <") {
		t.Fatalf("expected whitespace between tags to be removed, got %q", result)
	}
	if !strings.Contains(result, "><") {
		t.Fatalf("expected tags to be adjacent, got %q", result)
	}
}

func TestStripMarkdown(t *testing.T) {
	input := "## Heading\n\n**bold text** and *italic text*"
	result := stripMarkdown(input)

	// Heading markers should be removed.
	if strings.Contains(result, "##") {
		t.Fatalf("expected heading markers to be removed, got %q", result)
	}
	if !strings.Contains(result, "Heading") {
		t.Fatalf("expected heading text to remain, got %q", result)
	}

	// Bold markers should be removed.
	if strings.Contains(result, "**") {
		t.Fatalf("expected bold markers to be removed, got %q", result)
	}
	if !strings.Contains(result, "bold text") {
		t.Fatalf("expected bold text to remain, got %q", result)
	}
}

func TestCodeBlocksPreservedDuringWhitespaceCollapse(t *testing.T) {
	input := "Before    spaces.\n\n```go\nfunc main() {\n    fmt.Println(\"hello\")\n}\n```\n\nAfter    spaces."

	result := collapseWhitespace(input)

	// The code block should be untouched (indentation preserved).
	if !strings.Contains(result, "    fmt.Println") {
		t.Fatalf("expected code block indentation to be preserved, got %q", result)
	}

	// Text outside code blocks should have spaces collapsed.
	if strings.Contains(result, "Before    spaces") {
		t.Fatalf("expected spaces outside code blocks to be collapsed, got %q", result)
	}
}

func TestDedupInstructions(t *testing.T) {
	// The dedup threshold is 80 characters, so create a message longer than that.
	longContent := strings.Repeat("Follow these detailed instructions carefully. ", 3)

	messages := []pipeline.Message{
		{Role: "user", Content: longContent},
		{Role: "assistant", Content: "OK, understood."},
		{Role: "user", Content: longContent}, // duplicate
	}

	result := dedupInstructions(messages)

	// First occurrence should remain verbatim.
	if ExtractText(result[0].Content) != longContent {
		t.Fatal("expected first occurrence to be preserved")
	}

	// Second user message (index 2) should be replaced with a back-reference.
	replacement := ExtractText(result[2].Content)
	if !strings.Contains(replacement, "[See instructions above") {
		t.Fatalf("expected back-reference, got %q", replacement)
	}
}

func TestRulesMiddleware_DisabledIsNoOp(t *testing.T) {
	cfg := RulesConfig{
		CollapseWhitespace: false,
		MinifyJSON:         false,
		MinifyXML:          false,
		DedupInstructions:  false,
		StripMarkdown:      false,
	}
	mw := NewRulesMiddleware(cfg)

	if mw.Enabled() {
		t.Fatal("expected middleware to be disabled when all rules are off")
	}

	original := "Hello    world.    \t How are  you?"
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: original},
		},
	}

	result, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	// Content should be unchanged since middleware is disabled.
	// (The chain would skip calling ProcessRequest, but if called directly
	// the rules loop won't fire because Enabled is false and the pipeline
	// checks Enabled(). However, since ProcessRequest still runs the apply
	// loop which checks per-rule flags, none should fire.)
	got := ExtractText(result.Messages[0].Content)
	if got != original {
		t.Fatalf("expected no-op, got %q", got)
	}
}
