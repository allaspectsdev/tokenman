package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/allaspectsdev/tokenman/internal/pipeline"
)

// ---------------------------------------------------------------------------
// Mock CacheStore
// ---------------------------------------------------------------------------

type mockCacheStore struct {
	entries map[string]*CacheEntry
}

func newMockCacheStore() *mockCacheStore {
	return &mockCacheStore{entries: make(map[string]*CacheEntry)}
}

func (m *mockCacheStore) GetCache(key string) (*CacheEntry, error) {
	if e, ok := m.entries[key]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockCacheStore) SetCache(key string, entry *CacheEntry) error {
	m.entries[key] = entry
	return nil
}

func (m *mockCacheStore) DeleteExpired() error {
	now := time.Now()
	for k, e := range m.entries {
		if now.After(e.ExpiresAt) {
			delete(m.entries, k)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// CacheKey tests
// ---------------------------------------------------------------------------

func TestCacheKey_SameInputsSameKey(t *testing.T) {
	msgs := []pipeline.Message{
		{Role: "user", Content: "hello"},
	}
	key1 := CacheKey("gpt-4", msgs, nil)
	key2 := CacheKey("gpt-4", msgs, nil)
	if key1 != key2 {
		t.Errorf("expected identical keys, got %q and %q", key1, key2)
	}
}

func TestCacheKey_DifferentModelDifferentKey(t *testing.T) {
	msgs := []pipeline.Message{
		{Role: "user", Content: "hello"},
	}
	key1 := CacheKey("gpt-4", msgs, nil)
	key2 := CacheKey("gpt-3.5", msgs, nil)
	if key1 == key2 {
		t.Errorf("expected different keys for different models, both got %q", key1)
	}
}

func TestCacheKey_DifferentMessagesDifferentKey(t *testing.T) {
	msgs1 := []pipeline.Message{{Role: "user", Content: "hello"}}
	msgs2 := []pipeline.Message{{Role: "user", Content: "goodbye"}}
	key1 := CacheKey("gpt-4", msgs1, nil)
	key2 := CacheKey("gpt-4", msgs2, nil)
	if key1 == key2 {
		t.Errorf("expected different keys for different messages, both got %q", key1)
	}
}

func TestCacheKey_DifferentToolsDifferentKey(t *testing.T) {
	msgs := []pipeline.Message{{Role: "user", Content: "hello"}}
	tools1 := []pipeline.Tool{{Name: "tool_a"}}
	tools2 := []pipeline.Tool{{Name: "tool_b"}}
	key1 := CacheKey("gpt-4", msgs, tools1)
	key2 := CacheKey("gpt-4", msgs, tools2)
	if key1 == key2 {
		t.Errorf("expected different keys for different tools, both got %q", key1)
	}
}

// ---------------------------------------------------------------------------
// IsCacheable tests
// ---------------------------------------------------------------------------

func TestIsCacheable_StreamingNotCacheable(t *testing.T) {
	req := &pipeline.Request{Stream: true}
	if IsCacheable(req) {
		t.Error("expected streaming request to not be cacheable")
	}
}

func TestIsCacheable_NonZeroTemperatureNotCacheable(t *testing.T) {
	temp := 0.7
	req := &pipeline.Request{Temperature: &temp}
	if IsCacheable(req) {
		t.Error("expected non-zero temperature request to not be cacheable")
	}
}

func TestIsCacheable_NilTemperatureCacheable(t *testing.T) {
	req := &pipeline.Request{}
	if !IsCacheable(req) {
		t.Error("expected nil temperature request to be cacheable")
	}
}

func TestIsCacheable_ZeroTemperatureCacheable(t *testing.T) {
	temp := 0.0
	req := &pipeline.Request{Temperature: &temp}
	if !IsCacheable(req) {
		t.Error("expected zero temperature request to be cacheable")
	}
}

// ---------------------------------------------------------------------------
// CacheMiddleware.ProcessRequest tests
// ---------------------------------------------------------------------------

func newTestMiddleware(t *testing.T, store CacheStore, maxEntries int) *CacheMiddleware {
	t.Helper()
	mw, err := NewCacheMiddleware(store, 3600, maxEntries, true)
	if err != nil {
		t.Fatalf("NewCacheMiddleware: %v", err)
	}
	return mw
}

func TestProcessRequest_CacheMiss(t *testing.T) {
	store := newMockCacheStore()
	mw := newTestMiddleware(t, store, 100)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	// On a cache miss, no cached_response should be in metadata.
	if out.Metadata != nil {
		if _, ok := out.Metadata["cached_response"]; ok {
			t.Error("expected no cached_response on cache miss")
		}
	}
}

func TestProcessRequest_CacheHit_Memory(t *testing.T) {
	store := newMockCacheStore()
	mw := newTestMiddleware(t, store, 100)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}
	key := CacheKey(req.Model, req.Messages, req.Tools)

	// Pre-populate the in-memory LRU cache.
	entry := &CacheEntry{
		Body:        []byte(`{"result":"cached"}`),
		StatusCode:  200,
		ContentType: "application/json",
		Model:       "gpt-4",
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		TokensSaved: 42,
	}
	mw.memory.Add(key, entry)

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	if out.Flags == nil || !out.Flags["cache_hit"] {
		t.Error("expected cache_hit flag to be true")
	}
	cr, ok := out.Metadata["cached_response"].(*pipeline.CachedResponse)
	if !ok || cr == nil {
		t.Fatal("expected cached_response in metadata")
	}
	if cr.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", cr.StatusCode)
	}
	if string(cr.Body) != `{"result":"cached"}` {
		t.Errorf("unexpected cached body: %s", cr.Body)
	}
}

func TestProcessRequest_CacheHit_PersistentStore(t *testing.T) {
	store := newMockCacheStore()
	mw := newTestMiddleware(t, store, 100)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}
	key := CacheKey(req.Model, req.Messages, req.Tools)

	// Put entry only in persistent store (not in memory).
	entry := &CacheEntry{
		Body:        []byte(`{"result":"from_store"}`),
		StatusCode:  200,
		ContentType: "application/json",
		Model:       "gpt-4",
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		TokensSaved: 10,
	}
	store.entries[key] = entry

	out, err := mw.ProcessRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}
	if !out.Flags["cache_hit"] {
		t.Error("expected cache_hit flag from persistent store")
	}

	// After a persistent hit, the entry should be promoted to memory.
	if _, ok := mw.memory.Get(key); !ok {
		t.Error("expected entry to be promoted to in-memory cache")
	}
}

// ---------------------------------------------------------------------------
// CacheMiddleware.ProcessResponse tests
// ---------------------------------------------------------------------------

func TestProcessResponse_StoresInCache(t *testing.T) {
	store := newMockCacheStore()
	mw := newTestMiddleware(t, store, 100)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
		TokensIn: 10,
	}
	resp := &pipeline.Response{
		StatusCode: 200,
		Body:       []byte(`{"result":"ok"}`),
		TokensOut:  20,
	}

	// Run ProcessRequest first to generate the cache key.
	req, _ = mw.ProcessRequest(context.Background(), req)

	_, err := mw.ProcessResponse(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	// Verify the entry is in the persistent store.
	key := req.Metadata["cache_key"].(string)
	cached, err := store.GetCache(key)
	if err != nil {
		t.Fatalf("expected entry in store: %v", err)
	}
	if cached.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", cached.StatusCode)
	}
	if cached.TokensSaved != 30 { // 10 in + 20 out
		t.Errorf("expected TokensSaved=30, got %d", cached.TokensSaved)
	}
}

func TestProcessResponse_DoesNotCacheErrors(t *testing.T) {
	store := newMockCacheStore()
	mw := newTestMiddleware(t, store, 100)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
	}
	resp := &pipeline.Response{
		StatusCode: 500,
		Body:       []byte(`{"error":"internal"}`),
	}

	req, _ = mw.ProcessRequest(context.Background(), req)
	_, err := mw.ProcessResponse(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	// Nothing should be in the store.
	if len(store.entries) != 0 {
		t.Errorf("expected no entries in store, got %d", len(store.entries))
	}
}

func TestProcessResponse_DoesNotReCacheOnHit(t *testing.T) {
	store := newMockCacheStore()
	mw := newTestMiddleware(t, store, 100)

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "hello"}},
		Flags:    map[string]bool{"cache_hit": true},
		Metadata: map[string]interface{}{"cache_key": "test-key"},
	}
	resp := &pipeline.Response{
		StatusCode: 200,
		Body:       []byte(`{"result":"ok"}`),
	}

	_, err := mw.ProcessResponse(context.Background(), req, resp)
	if err != nil {
		t.Fatalf("ProcessResponse: %v", err)
	}

	// Should not write to store on a cache hit.
	if len(store.entries) != 0 {
		t.Errorf("expected no new entries in store after cache hit, got %d", len(store.entries))
	}
}

// ---------------------------------------------------------------------------
// LRU eviction test
// ---------------------------------------------------------------------------

func TestLRUEviction(t *testing.T) {
	store := newMockCacheStore()
	// Max 2 entries in LRU.
	mw := newTestMiddleware(t, store, 2)

	makeReq := func(content string) *pipeline.Request {
		return &pipeline.Request{
			Model:    "gpt-4",
			Messages: []pipeline.Message{{Role: "user", Content: content}},
			TokensIn: 5,
		}
	}
	resp := &pipeline.Response{StatusCode: 200, Body: []byte(`{}`), TokensOut: 5}

	// Add 3 entries; the first should be evicted.
	for _, content := range []string{"first", "second", "third"} {
		req := makeReq(content)
		req, _ = mw.ProcessRequest(context.Background(), req)
		mw.ProcessResponse(context.Background(), req, resp)
	}

	// The LRU should only hold 2 entries.
	if mw.memory.Len() != 2 {
		t.Errorf("expected 2 entries in LRU, got %d", mw.memory.Len())
	}

	// The first key should have been evicted.
	firstKey := CacheKey("gpt-4", []pipeline.Message{{Role: "user", Content: "first"}}, nil)
	if _, ok := mw.memory.Get(firstKey); ok {
		t.Error("expected 'first' to be evicted from LRU")
	}

	// The second and third should still be present.
	secondKey := CacheKey("gpt-4", []pipeline.Message{{Role: "user", Content: "second"}}, nil)
	thirdKey := CacheKey("gpt-4", []pipeline.Message{{Role: "user", Content: "third"}}, nil)
	if _, ok := mw.memory.Get(secondKey); !ok {
		t.Error("expected 'second' to still be in LRU")
	}
	if _, ok := mw.memory.Get(thirdKey); !ok {
		t.Error("expected 'third' to still be in LRU")
	}
}

// ---------------------------------------------------------------------------
// TTL expiry test
// ---------------------------------------------------------------------------

func TestTTLExpiry(t *testing.T) {
	store := newMockCacheStore()
	// TTL of 1 second.
	mw, err := NewCacheMiddleware(store, 1, 100, true)
	if err != nil {
		t.Fatalf("NewCacheMiddleware: %v", err)
	}

	req := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "ttl-test"}},
		TokensIn: 5,
	}
	resp := &pipeline.Response{StatusCode: 200, Body: []byte(`{"ok":true}`), TokensOut: 5}

	// Store the response.
	req, _ = mw.ProcessRequest(context.Background(), req)
	mw.ProcessResponse(context.Background(), req, resp)

	// Immediately the entry should be a hit.
	req2 := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "ttl-test"}},
	}
	out, _ := mw.ProcessRequest(context.Background(), req2)
	if out.Flags == nil || !out.Flags["cache_hit"] {
		t.Error("expected cache hit before TTL expiry")
	}

	// Wait for TTL to expire.
	time.Sleep(1100 * time.Millisecond)

	// Now the entry should be expired and treated as a miss.
	req3 := &pipeline.Request{
		Model:    "gpt-4",
		Messages: []pipeline.Message{{Role: "user", Content: "ttl-test"}},
	}
	out, _ = mw.ProcessRequest(context.Background(), req3)
	if out.Flags != nil && out.Flags["cache_hit"] {
		t.Error("expected cache miss after TTL expiry")
	}
}
