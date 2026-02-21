package compress

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// HashContent returns the SHA-256 hex digest of the given content string.
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// HashMessages returns a single SHA-256 hex digest computed by concatenating
// the text content of every message separated by newlines.
func HashMessages(messages []pipeline.Message) string {
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(ExtractText(msg.Content))
	}
	return HashContent(b.String())
}

// ExtractText extracts the plain text from a message Content field.
// Content may be a plain string or a []ContentBlock (or []interface{} from
// JSON unmarshalling). For content blocks, only blocks with Type=="text"
// contribute their Text field. Unrecognised types are rendered with
// fmt.Sprintf as a fallback.
func ExtractText(content interface{}) string {
	if content == nil {
		return ""
	}

	switch v := content.(type) {
	case string:
		return v

	case []pipeline.ContentBlock:
		var b strings.Builder
		for _, block := range v {
			if block.Type == "text" || block.Type == "" {
				b.WriteString(block.Text)
			}
		}
		return b.String()

	case []interface{}:
		// Handle the common case where JSON unmarshalling produces
		// []interface{} instead of []ContentBlock.
		var b strings.Builder
		for _, item := range v {
			switch block := item.(type) {
			case map[string]interface{}:
				blockType, _ := block["type"].(string)
				if blockType == "text" || blockType == "" {
					if text, ok := block["text"].(string); ok {
						b.WriteString(text)
					}
				}
			case pipeline.ContentBlock:
				if block.Type == "text" || block.Type == "" {
					b.WriteString(block.Text)
				}
			default:
				b.WriteString(fmt.Sprintf("%v", block))
			}
		}
		return b.String()

	default:
		return fmt.Sprintf("%v", content)
	}
}
