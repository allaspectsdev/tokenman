package tokenizer

import (
	"testing"
)

func TestCountTokens_NonZeroForKnownText(t *testing.T) {
	tok := New()
	text := "Hello, world! This is a test of the tokenizer."
	count := tok.CountTokens("gpt-4", text)
	if count == 0 {
		t.Errorf("CountTokens returned 0 for known text %q; want non-zero", text)
	}
}

func TestCountTokens_ZeroForEmptyText(t *testing.T) {
	tok := New()
	count := tok.CountTokens("gpt-4", "")
	if count != 0 {
		t.Errorf("CountTokens returned %d for empty text; want 0", count)
	}
}

func TestGetEncoding_Cl100kForClaudeModels(t *testing.T) {
	tok := New()

	claudeModels := []string{
		"claude-opus-4-20250514",
		"claude-opus-4",
		"claude-sonnet-4-20250514",
		"claude-sonnet-4",
		"claude-sonnet-4-5-20241022",
		"claude-sonnet-4-5",
		"claude-haiku-4-5-20241022",
		"claude-haiku-4-5",
	}

	for _, model := range claudeModels {
		enc := tok.GetEncoding(model)
		if enc != "cl100k_base" {
			t.Errorf("GetEncoding(%q) = %q; want %q", model, enc, "cl100k_base")
		}
	}
}

func TestGetEncoding_O200kForGPT4oMini(t *testing.T) {
	tok := New()
	enc := tok.GetEncoding("gpt-4o-mini")
	if enc != "o200k_base" {
		t.Errorf("GetEncoding(\"gpt-4o-mini\") = %q; want %q", enc, "o200k_base")
	}
}

func TestGetEncoding_Cl100kForUnknownModels(t *testing.T) {
	tok := New()
	unknowns := []string{
		"some-random-model",
		"llama-3-70b",
		"mistral-7b",
	}
	for _, model := range unknowns {
		enc := tok.GetEncoding(model)
		if enc != "cl100k_base" {
			t.Errorf("GetEncoding(%q) = %q; want %q", model, enc, "cl100k_base")
		}
	}
}

func TestCountMessages_IncludesPerMessageOverhead(t *testing.T) {
	tok := New()
	model := "gpt-4"

	messages := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	// Count tokens for just the raw content of each message.
	rawSum := 0
	for _, msg := range messages {
		rawSum += tok.CountTokens(model, msg.Role)
		rawSum += tok.CountTokens(model, msg.Content)
	}

	// CountMessages should include per-message overhead (4 tokens each)
	// and reply priming (3 tokens), so the result must be strictly greater
	// than the sum of individual token counts.
	total := tok.CountMessages(model, messages)
	if total <= rawSum {
		t.Errorf("CountMessages returned %d; expected > %d (raw sum) due to per-message overhead", total, rawSum)
	}
}

func TestGetEncoding_PrefixMatchForVersionedModelNames(t *testing.T) {
	tok := New()

	// Use Claude model names with extra version suffixes. These are unambiguous
	// because no shorter Claude prefix in the map collides with a different encoding.
	tests := []struct {
		model    string
		expected string
	}{
		// "claude-opus-4-20250514" is in the map; appending more should still match via prefix.
		{"claude-opus-4-20250514-extra-version", "cl100k_base"},
		// "claude-sonnet-4-20250514" is in the map.
		{"claude-sonnet-4-20250514-rc1", "cl100k_base"},
		// "claude-haiku-4-5-20241022" is in the map.
		{"claude-haiku-4-5-20241022-beta", "cl100k_base"},
	}

	for _, tt := range tests {
		enc := tok.GetEncoding(tt.model)
		if enc != tt.expected {
			t.Errorf("GetEncoding(%q) = %q; want %q (prefix match)", tt.model, enc, tt.expected)
		}
	}
}
