package security

import (
	"context"
	"strings"
	"testing"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// ---------------------------------------------------------------------------
// Email detection
// ---------------------------------------------------------------------------

func TestPII_EmailDetection(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Contact me at user@example.com please"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "user@example.com") {
		t.Error("expected email to be redacted")
	}
	if !strings.Contains(content, "[EMAIL_1]") {
		t.Errorf("expected [EMAIL_1] placeholder, got: %s", content)
	}
}

func TestPII_MultipleEmails(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Send to alice@test.com and bob@test.com"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "alice@test.com") || strings.Contains(content, "bob@test.com") {
		t.Error("expected both emails to be redacted")
	}
}

// ---------------------------------------------------------------------------
// Phone number detection
// ---------------------------------------------------------------------------

func TestPII_PhoneDetection(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)

	tests := []struct {
		name  string
		input string
	}{
		{"US format", "Call me at (555) 123-4567"},
		{"dashes", "Call me at 555-123-4567"},
		{"dots", "Call me at 555.123.4567"},
		{"international", "Call me at +14155551234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &pipeline.Request{
				Messages: []pipeline.Message{
					{Role: "user", Content: tt.input},
				},
			}
			out, err := mw.ProcessRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("ProcessRequest: %v", err)
			}
			content := out.Messages[0].Content.(string)
			if !strings.Contains(content, "[PHONE_") {
				t.Errorf("expected phone to be redacted in %q, got: %s", tt.name, content)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SSN detection with validation
// ---------------------------------------------------------------------------

func TestPII_SSNDetection(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "123-45-6789") {
		t.Error("expected SSN to be redacted")
	}
	if !strings.Contains(content, "[SSN_1]") {
		t.Errorf("expected [SSN_1] placeholder, got: %s", content)
	}
}

func TestPII_InvalidSSNsNotDetected(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)

	invalidSSNs := []struct {
		name string
		ssn  string
	}{
		{"area 000", "000-12-3456"},
		{"area 666", "666-12-3456"},
		{"area 9xx", "900-12-3456"},
		{"group 00", "123-00-6789"},
		{"serial 0000", "123-45-0000"},
	}

	for _, tt := range invalidSSNs {
		t.Run(tt.name, func(t *testing.T) {
			req := &pipeline.Request{
				Messages: []pipeline.Message{
					{Role: "user", Content: "Number: " + tt.ssn},
				},
			}
			out, err := mw.ProcessRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("ProcessRequest: %v", err)
			}
			content := out.Messages[0].Content.(string)
			// The invalid SSN should NOT be redacted.
			if strings.Contains(content, "[SSN_") {
				t.Errorf("expected invalid SSN %s to not be detected, got: %s", tt.ssn, content)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Credit card detection with Luhn validation
// ---------------------------------------------------------------------------

func TestPII_CreditCardDetection(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)
	// 4111 1111 1111 1111 is a well-known test Visa number that passes Luhn.
	// Using spaced format to avoid phone-pattern collision.
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Card: 4111 1111 1111 1111"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "4111 1111 1111 1111") {
		t.Error("expected credit card to be redacted")
	}
	if !strings.Contains(content, "[CREDIT_CARD_") {
		t.Errorf("expected [CREDIT_CARD_*] placeholder, got: %s", content)
	}
}

func TestPII_InvalidCreditCardNotDetected(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)
	// 4111 1111 1111 1112 does NOT pass Luhn.
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Card: 4111 1111 1111 1112"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "[CREDIT_CARD_") {
		t.Errorf("expected invalid credit card to not be detected, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// API key detection
// ---------------------------------------------------------------------------

func TestPII_APIKeyDetection(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)

	tests := []struct {
		name  string
		input string
	}{
		{"OpenAI key", "Key: sk-abcdefghijklmnopqrstuvwxyz12345"},
		{"AWS key", "Key: AKIAIOSFODNN7EXAMPLE"},
		{"GitHub PAT", "Key: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &pipeline.Request{
				Messages: []pipeline.Message{
					{Role: "user", Content: tt.input},
				},
			}
			out, err := mw.ProcessRequest(context.Background(), req)
			if err != nil {
				t.Fatalf("ProcessRequest: %v", err)
			}
			content := out.Messages[0].Content.(string)
			if !strings.Contains(content, "[API_KEY_") {
				t.Errorf("expected API key to be redacted in %q, got: %s", tt.name, content)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Bidirectional PII mapping (redact on request, restore on response)
// ---------------------------------------------------------------------------

func TestPII_BidirectionalMapping(t *testing.T) {
	mw := NewPIIMiddleware("redact", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Email me at secret@example.com with the info"},
		},
	}

	// ProcessRequest redacts.
	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "secret@example.com") {
		t.Fatal("expected email to be redacted")
	}

	// Simulate a response that echoes the placeholder.
	resp := &pipeline.Response{
		StatusCode: 200,
		Body:       []byte(`The email is [EMAIL_1]`),
	}

	// ProcessResponse should restore.
	outResp, err := mw.ProcessResponse(context.Background(), out, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	restored := string(outResp.Body)
	if !strings.Contains(restored, "secret@example.com") {
		t.Errorf("expected email to be restored in response, got: %s", restored)
	}
	if strings.Contains(restored, "[EMAIL_1]") {
		t.Errorf("expected placeholder to be replaced, got: %s", restored)
	}
}

// ---------------------------------------------------------------------------
// Allow-list bypasses detection
// ---------------------------------------------------------------------------

func TestPII_AllowListBypassesDetection(t *testing.T) {
	allowList := []string{"allowed@example.com"}
	mw := NewPIIMiddleware("redact", allowList, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Contact allowed@example.com or private@example.com"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	// allowed@example.com should NOT be redacted.
	if !strings.Contains(content, "allowed@example.com") {
		t.Error("expected allow-listed email to remain unredacted")
	}
	// private@example.com should be redacted.
	if strings.Contains(content, "private@example.com") {
		t.Error("expected non-allow-listed email to be redacted")
	}
}

// ---------------------------------------------------------------------------
// Block action returns error
// ---------------------------------------------------------------------------

func TestPII_BlockActionReturnsError(t *testing.T) {
	mw := NewPIIMiddleware("block", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "My SSN is 123-45-6789"},
		},
	}

	_, err := mw.ProcessRequest(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for block action with PII detected")
	}
	if !strings.Contains(err.Error(), "pii detected") {
		t.Errorf("expected 'pii detected' in error, got: %v", err)
	}
}

func TestPII_BlockActionNoPII(t *testing.T) {
	mw := NewPIIMiddleware("block", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Just a normal message with no PII"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error without PII, got: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil request")
	}
}

// ---------------------------------------------------------------------------
// Hash action replaces PII with hashed placeholders
// ---------------------------------------------------------------------------

func TestPII_HashActionReplacesPII(t *testing.T) {
	mw := NewPIIMiddleware("hash", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Contact me at user@example.com please"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "user@example.com") {
		t.Error("expected email to be hashed")
	}
	if !strings.Contains(content, "[EMAIL_HASH_") {
		t.Errorf("expected [EMAIL_HASH_*] placeholder, got: %s", content)
	}
}

func TestPII_HashActionDoesNotRestoreOnResponse(t *testing.T) {
	mw := NewPIIMiddleware("hash", nil, true)
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: "Email me at secret@example.com"},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	content := out.Messages[0].Content.(string)
	if strings.Contains(content, "secret@example.com") {
		t.Fatal("expected email to be hashed")
	}

	// Simulate a response containing the hash placeholder.
	resp := &pipeline.Response{
		StatusCode: 200,
		Body:       []byte(content),
	}

	outResp, err := mw.ProcessResponse(context.Background(), out, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	// Hash is one-way — the original value should NOT be restored.
	restored := string(outResp.Body)
	if strings.Contains(restored, "secret@example.com") {
		t.Error("hash action should not restore original PII in response")
	}
}

// ---------------------------------------------------------------------------
// Log action doesn't modify content but records detections
// ---------------------------------------------------------------------------

func TestPII_LogActionPreservesContent(t *testing.T) {
	mw := NewPIIMiddleware("log", nil, true)
	original := "My email is user@example.com"
	req := &pipeline.Request{
		Messages: []pipeline.Message{
			{Role: "user", Content: original},
		},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	// Content should be unchanged.
	content := out.Messages[0].Content.(string)
	if content != original {
		t.Errorf("expected content to be unchanged, got: %s", content)
	}

	// But detections should be recorded in metadata.
	dets, ok := out.Metadata["pii_detections"].([]PIIDetection)
	if !ok || len(dets) == 0 {
		t.Fatal("expected PII detections in metadata")
	}

	found := false
	for _, d := range dets {
		if d.Type == "EMAIL" && d.Value == maskValue("user@example.com") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected EMAIL detection with masked value %q", maskValue("user@example.com"))
	}
}
