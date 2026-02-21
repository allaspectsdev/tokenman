package compress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// ---------------------------------------------------------------------------
// Mock FingerprintStore
// ---------------------------------------------------------------------------

type mockFingerprintEntry struct {
	hitCount int
	lastSeen time.Time
}

type mockFingerprintStore struct {
	fingerprints map[string]*mockFingerprintEntry
}

func newMockFingerprintStore() *mockFingerprintStore {
	return &mockFingerprintStore{
		fingerprints: make(map[string]*mockFingerprintEntry),
	}
}

func (m *mockFingerprintStore) UpsertFingerprint(hash, contentType string, tokenCount int) error {
	if entry, ok := m.fingerprints[hash]; ok {
		entry.hitCount++
		entry.lastSeen = time.Now()
	} else {
		m.fingerprints[hash] = &mockFingerprintEntry{
			hitCount: 1,
			lastSeen: time.Now(),
		}
	}
	return nil
}

func (m *mockFingerprintStore) GetFingerprint(hash string) (hitCount int, lastSeen time.Time, err error) {
	if entry, ok := m.fingerprints[hash]; ok {
		return entry.hitCount, entry.lastSeen, nil
	}
	return 0, time.Time{}, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHashContentConsistent(t *testing.T) {
	content := "You are a helpful assistant."
	h1 := HashContent(content)
	h2 := HashContent(content)

	if h1 != h2 {
		t.Fatalf("HashContent not consistent: %q vs %q", h1, h2)
	}

	// Verify it is a valid SHA-256 hex digest.
	raw := sha256.Sum256([]byte(content))
	expected := hex.EncodeToString(raw[:])
	if h1 != expected {
		t.Fatalf("expected SHA-256 %q, got %q", expected, h1)
	}

	// Different input produces different hash.
	h3 := HashContent("different content")
	if h1 == h3 {
		t.Fatal("different content should produce different hashes")
	}
}

func TestDedupMiddleware_UpsertsFingerprints(t *testing.T) {
	store := newMockFingerprintStore()
	mw := NewDedupMiddleware(store, 300, true)

	req := &pipeline.Request{
		Format: pipeline.FormatAnthropic,
		System: "You are a helpful assistant.",
	}

	_, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	hash := HashContent(req.System)
	entry, ok := store.fingerprints[hash]
	if !ok {
		t.Fatal("expected fingerprint to be stored")
	}
	if entry.hitCount != 1 {
		t.Fatalf("expected hitCount 1, got %d", entry.hitCount)
	}
}

func TestDedupMiddleware_AnnotatesCacheControl_Anthropic(t *testing.T) {
	store := newMockFingerprintStore()
	mw := NewDedupMiddleware(store, 300, true)

	system := "You are a helpful assistant used for testing dedup."

	// First request -- upserts with hitCount=1, seenWithinTTL returns false.
	req1 := &pipeline.Request{
		Format: pipeline.FormatAnthropic,
		System: system,
	}
	_, err := mw.ProcessRequest(context.Background(), req1)
	if err != nil {
		t.Fatalf("first ProcessRequest error: %v", err)
	}

	// Second request -- hitCount becomes 2, seenWithinTTL returns true.
	req2 := &pipeline.Request{
		Format: pipeline.FormatAnthropic,
		System: system,
	}
	req2, err = mw.ProcessRequest(context.Background(), req2)
	if err != nil {
		t.Fatalf("second ProcessRequest error: %v", err)
	}

	// After the second request, system blocks should be annotated with cache_control.
	if len(req2.SystemBlocks) == 0 {
		t.Fatal("expected SystemBlocks to be populated with cache_control annotation")
	}

	found := false
	for _, block := range req2.SystemBlocks {
		if block.CacheControl != nil && block.CacheControl["type"] == "ephemeral" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cache_control ephemeral annotation on system blocks")
	}
}

func TestDedupMiddleware_OpenAI_ReordersSystemToFront(t *testing.T) {
	store := newMockFingerprintStore()
	mw := NewDedupMiddleware(store, 300, true)

	req := &pipeline.Request{
		Format: pipeline.FormatOpenAI,
		Messages: []pipeline.Message{
			{Role: "user", Content: "Hello"},
			{Role: "system", Content: "Be helpful."},
			{Role: "assistant", Content: "Hi there!"},
			{Role: "system", Content: "Be concise."},
		},
	}

	req, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest error: %v", err)
	}

	// System messages should now be at the front.
	if req.Messages[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %q", req.Messages[0].Role)
	}
	if req.Messages[1].Role != "system" {
		t.Fatalf("expected second message to be system, got %q", req.Messages[1].Role)
	}
	if req.Messages[2].Role != "user" {
		t.Fatalf("expected third message to be user, got %q", req.Messages[2].Role)
	}
	if req.Messages[3].Role != "assistant" {
		t.Fatalf("expected fourth message to be assistant, got %q", req.Messages[3].Role)
	}
}

func TestDedupMiddleware_Disabled_IsNoOp(t *testing.T) {
	store := newMockFingerprintStore()
	mw := NewDedupMiddleware(store, 300, false)

	if mw.Enabled() {
		t.Fatal("expected middleware to be disabled")
	}

	// Even though we can call ProcessRequest directly, confirm it's reported
	// as disabled so the chain would skip it.
	if mw.Name() != "dedup" {
		t.Fatalf("expected name 'dedup', got %q", mw.Name())
	}
}
