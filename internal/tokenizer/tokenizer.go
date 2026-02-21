package tokenizer

import (
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Message represents a chat message for token counting purposes.
type Message struct {
	Role    string
	Content string
	Name    string // optional
}

// Tokenizer provides token counting using tiktoken encodings.
// Encodings are cached via sync.Once to avoid repeated initialization.
type Tokenizer struct {
	cl100kOnce sync.Once
	cl100kEnc  *tiktoken.Tiktoken
	cl100kErr  error

	o200kOnce sync.Once
	o200kEnc  *tiktoken.Tiktoken
	o200kErr  error
}

// modelEncodings maps model names to their tiktoken encoding.
var modelEncodings = map[string]string{
	// Claude models — cl100k_base
	"claude-opus-4-20250514":      "cl100k_base",
	"claude-opus-4":               "cl100k_base",
	"claude-sonnet-4-20250514":    "cl100k_base",
	"claude-sonnet-4":             "cl100k_base",
	"claude-sonnet-4-5-20241022":  "cl100k_base",
	"claude-sonnet-4-5":           "cl100k_base",
	"claude-haiku-4-5-20241022":   "cl100k_base",
	"claude-haiku-4-5":            "cl100k_base",

	// OpenAI models — cl100k_base
	"gpt-4":       "cl100k_base",
	"gpt-4-turbo": "cl100k_base",
	"gpt-4o":      "cl100k_base",

	// OpenAI models — o200k_base
	"gpt-4o-2024-08-06": "o200k_base",
	"gpt-4o-mini":       "o200k_base",
	"gpt-4o-mini-2024-07-18": "o200k_base",
}

// New creates a new Tokenizer instance.
func New() *Tokenizer {
	return &Tokenizer{}
}

// GetEncoding returns the encoding name for the given model.
// Unknown models default to cl100k_base.
func (t *Tokenizer) GetEncoding(model string) string {
	if enc, ok := modelEncodings[model]; ok {
		return enc
	}

	// Try prefix matching for versioned model names.
	lower := strings.ToLower(model)
	for m, enc := range modelEncodings {
		if strings.HasPrefix(lower, m) {
			return enc
		}
	}

	return "cl100k_base"
}

// getEncoder returns the cached tiktoken encoder for the given model.
func (t *Tokenizer) getEncoder(model string) (*tiktoken.Tiktoken, error) {
	encName := t.GetEncoding(model)

	switch encName {
	case "o200k_base":
		t.o200kOnce.Do(func() {
			t.o200kEnc, t.o200kErr = tiktoken.GetEncoding("o200k_base")
		})
		return t.o200kEnc, t.o200kErr
	default:
		t.cl100kOnce.Do(func() {
			t.cl100kEnc, t.cl100kErr = tiktoken.GetEncoding("cl100k_base")
		})
		return t.cl100kEnc, t.cl100kErr
	}
}

// CountTokens counts the number of tokens in the given text for the specified model.
func (t *Tokenizer) CountTokens(model, text string) int {
	enc, err := t.getEncoder(model)
	if err != nil {
		return 0
	}
	tokens := enc.Encode(text, nil, nil)
	return len(tokens)
}

// CountMessages counts the total number of tokens across a slice of chat messages
// for the specified model. Each message incurs a 4-token overhead (role framing),
// and an additional 3 tokens are added for reply priming.
func (t *Tokenizer) CountMessages(model string, messages []Message) int {
	enc, err := t.getEncoder(model)
	if err != nil {
		return 0
	}

	total := 0
	for _, msg := range messages {
		// Every message has a 4-token overhead: <im_start>{role}\n ... <im_end>\n
		total += 4
		total += len(enc.Encode(msg.Role, nil, nil))
		total += len(enc.Encode(msg.Content, nil, nil))
		if msg.Name != "" {
			total += len(enc.Encode(msg.Name, nil, nil))
			// Name replaces role in the token count, but we already counted role,
			// so the name field costs an additional -1 in some formats. For
			// simplicity and consistency with the OpenAI reference implementation,
			// we just add the name tokens here.
		}
	}

	// 3 tokens for reply priming (<im_start>assistant<im_sep>)
	total += 3

	return total
}
