package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// PIIDetection records a single detected PII instance.
type PIIDetection struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	FieldPath string `json:"field_path"`
}

// PIIMapping maps placeholders to their original values for bidirectional
// restore within a single request lifecycle.
type PIIMapping struct {
	mu       sync.Mutex
	forward  map[string]string // original → placeholder
	reverse  map[string]string // placeholder → original
	counters map[string]int    // type → next counter
}

// newPIIMapping creates a new PIIMapping.
func newPIIMapping() *PIIMapping {
	return &PIIMapping{
		forward:  make(map[string]string),
		reverse:  make(map[string]string),
		counters: make(map[string]int),
	}
}

// placeholder returns or creates a placeholder for the given original value
// and PII type. e.g., [EMAIL_1], [PHONE_2].
func (m *PIIMapping) placeholder(original, piiType string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ph, ok := m.forward[original]; ok {
		return ph
	}

	m.counters[piiType]++
	ph := fmt.Sprintf("[%s_%d]", piiType, m.counters[piiType])
	m.forward[original] = ph
	m.reverse[ph] = original
	return ph
}

// restore replaces all placeholders in text with their original values.
func (m *PIIMapping) restore(text string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	for ph, original := range m.reverse {
		text = strings.ReplaceAll(text, ph, original)
	}
	return text
}

// PIIMiddleware is a pipeline.Middleware that scans messages for PII and
// takes action based on the configured mode: "redact", "log", or "block".
type PIIMiddleware struct {
	patterns  []*PIIPattern
	action    string
	allowList map[string]bool
	enabled   bool
}

// Compile-time assertion that PIIMiddleware implements pipeline.Middleware.
var _ pipeline.Middleware = (*PIIMiddleware)(nil)

// NewPIIMiddleware creates a new PIIMiddleware.
//
//   - action is one of "redact", "hash", "log", or "block".
//   - allowList contains values that should be ignored during scanning.
//   - enabled controls whether the middleware is active.
func NewPIIMiddleware(action string, allowList []string, enabled bool) *PIIMiddleware {
	allow := make(map[string]bool, len(allowList))
	for _, v := range allowList {
		allow[v] = true
	}
	return &PIIMiddleware{
		patterns:  CompilePatterns(),
		action:    action,
		allowList: allow,
		enabled:   enabled,
	}
}

// Name returns the middleware name.
func (p *PIIMiddleware) Name() string {
	return "pii"
}

// Enabled reports whether this middleware is active.
func (p *PIIMiddleware) Enabled() bool {
	return p.enabled
}

// ProcessRequest scans all message content for PII patterns and takes the
// configured action.
func (p *PIIMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}

	mapping := newPIIMapping()
	var detections []PIIDetection

	for i := range req.Messages {
		msg := &req.Messages[i]
		fieldPath := fmt.Sprintf("messages[%d].content", i)

		replaces := p.action == "redact" || p.action == "hash"

		switch c := msg.Content.(type) {
		case string:
			newContent, dets := p.scanAndProcess(c, fieldPath, mapping)
			detections = append(detections, dets...)
			if replaces {
				msg.Content = newContent
			}

		case []interface{}:
			for j, block := range c {
				blockPath := fmt.Sprintf("messages[%d].content[%d]", i, j)
				blockMap, ok := block.(map[string]interface{})
				if !ok {
					continue
				}
				if text, ok := blockMap["text"].(string); ok {
					newText, dets := p.scanAndProcess(text, blockPath+".text", mapping)
					detections = append(detections, dets...)
					if replaces {
						blockMap["text"] = newText
						c[j] = blockMap
					}
				}
				if content, ok := blockMap["content"].(string); ok {
					newContent, dets := p.scanAndProcess(content, blockPath+".content", mapping)
					detections = append(detections, dets...)
					if replaces {
						blockMap["content"] = newContent
						c[j] = blockMap
					}
				}
			}
			if replaces {
				msg.Content = c
			}
		}
	}

	// Also scan the system prompt.
	if req.System != "" {
		newSystem, dets := p.scanAndProcess(req.System, "system", mapping)
		detections = append(detections, dets...)
		if p.action == "redact" || p.action == "hash" {
			req.System = newSystem
		}
	}

	// Store detections and mapping in metadata.
	if len(detections) > 0 {
		req.Metadata["pii_detections"] = detections
		req.Metadata["pii_mapping"] = mapping

		if p.action == "block" {
			types := make(map[string]bool)
			for _, d := range detections {
				types[d.Type] = true
			}
			typeList := make([]string, 0, len(types))
			for t := range types {
				typeList = append(typeList, t)
			}
			return nil, fmt.Errorf("pii detected: request contains %s", strings.Join(typeList, ", "))
		}
	}

	return req, nil
}

// ProcessResponse restores redacted placeholders in the response body if
// the action was "redact".
func (p *PIIMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	if p.action != "redact" {
		return resp, nil
	}

	if req.Metadata == nil {
		return resp, nil
	}

	mapping, ok := req.Metadata["pii_mapping"].(*PIIMapping)
	if !ok || mapping == nil {
		return resp, nil
	}

	// Restore placeholders in the response body.
	if len(resp.Body) > 0 {
		restored := mapping.restore(string(resp.Body))
		resp.Body = []byte(restored)
	}

	return resp, nil
}

// scanAndProcess scans text for PII and either redacts, logs, or records
// detections depending on the configured action.
func (p *PIIMiddleware) scanAndProcess(text, fieldPath string, mapping *PIIMapping) (string, []PIIDetection) {
	var detections []PIIDetection
	result := text

	for _, pattern := range p.patterns {
		matches := pattern.Regex.FindAllString(text, -1)
		for _, match := range matches {
			// Skip allow-listed values.
			if p.allowList[match] {
				continue
			}

			// Run validation if present.
			if pattern.Validate != nil && !pattern.Validate(match) {
				continue
			}

			detections = append(detections, PIIDetection{
				Type:      pattern.Name,
				Value:     maskValue(match),
				FieldPath: fieldPath,
			})

			if p.action == "redact" {
				ph := mapping.placeholder(match, pattern.Name)
				result = strings.ReplaceAll(result, match, ph)
			}
			if p.action == "hash" {
				h := sha256.Sum256([]byte(match))
				hashStr := hex.EncodeToString(h[:])[:8]
				placeholder := fmt.Sprintf("[%s_HASH_%s]", strings.ToUpper(pattern.Name), hashStr)
				result = strings.ReplaceAll(result, match, placeholder)
			}
		}
	}

	return result, detections
}

// maskValue masks the interior of a string, showing only the first 2 and last 2
// characters. Values of 4 characters or fewer are fully masked.
func maskValue(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

// MarshalDetections serializes PII detections from request metadata to JSON.
// This is a helper for logging and persistence.
func MarshalDetections(req *pipeline.Request) ([]byte, error) {
	if req.Metadata == nil {
		return nil, nil
	}
	dets, ok := req.Metadata["pii_detections"].([]PIIDetection)
	if !ok || len(dets) == 0 {
		return nil, nil
	}
	return json.Marshal(dets)
}
