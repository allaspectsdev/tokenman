package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/allaspects/tokenman/internal/cache"
)

// openTestStore creates a temporary SQLite-backed Store for testing.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%s): %v", dbPath, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ---------------------------------------------------------------------------
// FingerprintAdapter
// ---------------------------------------------------------------------------

func TestFingerprintAdapter_UpsertAndGet(t *testing.T) {
	s := openTestStore(t)
	fa := NewFingerprintAdapter(s)

	hash := "abc123"
	contentType := "text/plain"
	tokenCount := 42

	// Upsert should succeed.
	if err := fa.UpsertFingerprint(hash, contentType, tokenCount); err != nil {
		t.Fatalf("UpsertFingerprint: %v", err)
	}

	// Verify the record was persisted via the underlying store.
	raw, err := s.GetFingerprint(hash)
	if err != nil {
		t.Fatalf("store.GetFingerprint: %v", err)
	}
	if raw.Hash != hash {
		t.Errorf("Hash = %q, want %q", raw.Hash, hash)
	}
	if raw.ContentType != contentType {
		t.Errorf("ContentType = %q, want %q", raw.ContentType, contentType)
	}
	if raw.TokenCount != int64(tokenCount) {
		t.Errorf("TokenCount = %d, want %d", raw.TokenCount, tokenCount)
	}
}

func TestFingerprintAdapter_GetNonExistent(t *testing.T) {
	s := openTestStore(t)
	fa := NewFingerprintAdapter(s)

	hitCount, lastSeen, err := fa.GetFingerprint("does-not-exist")
	if err != nil {
		t.Fatalf("GetFingerprint: unexpected error: %v", err)
	}
	if hitCount != 0 {
		t.Errorf("hitCount = %d, want 0", hitCount)
	}
	if !lastSeen.IsZero() {
		t.Errorf("lastSeen = %v, want zero time", lastSeen)
	}
}

func TestFingerprintAdapter_GetAfterUpsert(t *testing.T) {
	s := openTestStore(t)
	fa := NewFingerprintAdapter(s)

	hash := "hash-1"
	if err := fa.UpsertFingerprint(hash, "application/json", 100); err != nil {
		t.Fatalf("UpsertFingerprint: %v", err)
	}

	hitCount, lastSeen, err := fa.GetFingerprint(hash)
	if err != nil {
		t.Fatalf("GetFingerprint: %v", err)
	}

	// The initial insert sets hit_count to the schema default of 1
	// (see schemaFingerprints: hit_count INTEGER NOT NULL DEFAULT 1).
	// The Fingerprint struct is created with HitCount = 0 by the adapter,
	// so the INSERT uses 0. The schema default only applies when the column
	// is omitted. The INSERT explicitly provides 0, so the initial hit_count
	// is 0.
	if hitCount != 0 {
		t.Errorf("hitCount = %d, want 0", hitCount)
	}
	if lastSeen.IsZero() {
		t.Error("lastSeen should not be zero after upsert")
	}
}

func TestFingerprintAdapter_MultipleUpsertsIncrementHitCount(t *testing.T) {
	s := openTestStore(t)
	fa := NewFingerprintAdapter(s)

	hash := "dup-hash"

	// First insert.
	if err := fa.UpsertFingerprint(hash, "text/html", 10); err != nil {
		t.Fatalf("UpsertFingerprint #1: %v", err)
	}

	// Second upsert should increment hit_count.
	if err := fa.UpsertFingerprint(hash, "text/html", 10); err != nil {
		t.Fatalf("UpsertFingerprint #2: %v", err)
	}

	// Third upsert.
	if err := fa.UpsertFingerprint(hash, "text/html", 10); err != nil {
		t.Fatalf("UpsertFingerprint #3: %v", err)
	}

	hitCount, _, err := fa.GetFingerprint(hash)
	if err != nil {
		t.Fatalf("GetFingerprint: %v", err)
	}

	// Initial insert stores 0; two subsequent upserts each increment by 1.
	if hitCount != 2 {
		t.Errorf("hitCount = %d, want 2 after two additional upserts", hitCount)
	}
}

// ---------------------------------------------------------------------------
// CacheAdapter
// ---------------------------------------------------------------------------

func TestCacheAdapter_SetAndGet(t *testing.T) {
	s := openTestStore(t)
	ca := NewCacheAdapter(s)

	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(1 * time.Hour).Truncate(time.Second)

	entry := &cache.CacheEntry{
		Body:        []byte(`{"result":"ok"}`),
		StatusCode:  200,
		ContentType: "application/json",
		Model:       "gpt-4",
		CreatedAt:   now,
		ExpiresAt:   expires,
		TokensSaved: 500,
	}

	key := "cache-key-1"

	if err := ca.SetCache(key, entry); err != nil {
		t.Fatalf("SetCache: %v", err)
	}

	got, err := ca.GetCache(key)
	if err != nil {
		t.Fatalf("GetCache: %v", err)
	}

	if string(got.Body) != string(entry.Body) {
		t.Errorf("Body = %q, want %q", got.Body, entry.Body)
	}
	if got.Model != entry.Model {
		t.Errorf("Model = %q, want %q", got.Model, entry.Model)
	}
	if got.TokensSaved != entry.TokensSaved {
		t.Errorf("TokensSaved = %d, want %d", got.TokensSaved, entry.TokensSaved)
	}
	// The adapter hardcodes StatusCode=200 and ContentType="application/json".
	if got.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", got.StatusCode)
	}
	if got.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want %q", got.ContentType, "application/json")
	}

	// Time comparison: the adapter round-trips through RFC3339 formatting,
	// so compare at second precision.
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
}

func TestCacheAdapter_GetNonExistent(t *testing.T) {
	s := openTestStore(t)
	ca := NewCacheAdapter(s)

	_, err := ca.GetCache("no-such-key")
	if err == nil {
		t.Fatal("GetCache: expected error for non-existent key, got nil")
	}
}

func TestCacheAdapter_DeleteExpired(t *testing.T) {
	s := openTestStore(t)
	ca := NewCacheAdapter(s)

	past := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	future := time.Now().UTC().Add(1 * time.Hour).Truncate(time.Second)

	// Insert an expired entry.
	expiredEntry := &cache.CacheEntry{
		Body:        []byte(`expired`),
		Model:       "gpt-4",
		CreatedAt:   past.Add(-1 * time.Hour),
		ExpiresAt:   past,
		TokensSaved: 10,
	}
	if err := ca.SetCache("expired-key", expiredEntry); err != nil {
		t.Fatalf("SetCache (expired): %v", err)
	}

	// Insert a valid (non-expired) entry.
	validEntry := &cache.CacheEntry{
		Body:        []byte(`valid`),
		Model:       "gpt-4",
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
		ExpiresAt:   future,
		TokensSaved: 20,
	}
	if err := ca.SetCache("valid-key", validEntry); err != nil {
		t.Fatalf("SetCache (valid): %v", err)
	}

	// Delete expired entries.
	if err := ca.DeleteExpired(); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}

	// The expired entry should be gone.
	_, err := ca.GetCache("expired-key")
	if err == nil {
		t.Error("GetCache(expired-key): expected error after DeleteExpired, got nil")
	}

	// The valid entry should still exist.
	got, err := ca.GetCache("valid-key")
	if err != nil {
		t.Fatalf("GetCache(valid-key): %v", err)
	}
	if string(got.Body) != "valid" {
		t.Errorf("Body = %q, want %q", got.Body, "valid")
	}
}

// ---------------------------------------------------------------------------
// BudgetAdapter
// ---------------------------------------------------------------------------

func TestBudgetAdapter_GetNonExistent(t *testing.T) {
	s := openTestStore(t)
	ba := NewBudgetAdapter(s)

	amount, limit, err := ba.GetBudget("daily", "2025-01-01")
	if err != nil {
		t.Fatalf("GetBudget: unexpected error: %v", err)
	}
	if amount != 0 {
		t.Errorf("amount = %f, want 0", amount)
	}
	if limit != 0 {
		t.Errorf("limit = %f, want 0", limit)
	}
}

func TestBudgetAdapter_AddSpendingCreatesRecord(t *testing.T) {
	s := openTestStore(t)
	ba := NewBudgetAdapter(s)

	period := "daily"
	periodStart := "2025-06-01"
	spendAmount := 1.50
	spendLimit := 10.00

	if err := ba.AddSpending(period, periodStart, spendAmount, spendLimit); err != nil {
		t.Fatalf("AddSpending: %v", err)
	}

	amount, limit, err := ba.GetBudget(period, periodStart)
	if err != nil {
		t.Fatalf("GetBudget: %v", err)
	}
	if amount != spendAmount {
		t.Errorf("amount = %f, want %f", amount, spendAmount)
	}
	if limit != spendLimit {
		t.Errorf("limit = %f, want %f", limit, spendLimit)
	}
}

func TestBudgetAdapter_GetBudgetAfterAddSpending(t *testing.T) {
	s := openTestStore(t)
	ba := NewBudgetAdapter(s)

	period := "monthly"
	periodStart := "2025-06-01"

	if err := ba.AddSpending(period, periodStart, 5.00, 100.00); err != nil {
		t.Fatalf("AddSpending: %v", err)
	}

	amount, limit, err := ba.GetBudget(period, periodStart)
	if err != nil {
		t.Fatalf("GetBudget: %v", err)
	}
	if amount != 5.00 {
		t.Errorf("amount = %f, want 5.00", amount)
	}
	if limit != 100.00 {
		t.Errorf("limit = %f, want 100.00", limit)
	}
}

func TestBudgetAdapter_MultipleAddSpendingAccumulate(t *testing.T) {
	s := openTestStore(t)
	ba := NewBudgetAdapter(s)

	period := "weekly"
	periodStart := "2025-06-02"

	if err := ba.AddSpending(period, periodStart, 2.50, 50.00); err != nil {
		t.Fatalf("AddSpending #1: %v", err)
	}
	if err := ba.AddSpending(period, periodStart, 3.25, 50.00); err != nil {
		t.Fatalf("AddSpending #2: %v", err)
	}
	if err := ba.AddSpending(period, periodStart, 1.00, 50.00); err != nil {
		t.Fatalf("AddSpending #3: %v", err)
	}

	amount, limit, err := ba.GetBudget(period, periodStart)
	if err != nil {
		t.Fatalf("GetBudget: %v", err)
	}

	expectedAmount := 2.50 + 3.25 + 1.00
	if amount != expectedAmount {
		t.Errorf("amount = %f, want %f", amount, expectedAmount)
	}
	if limit != 50.00 {
		t.Errorf("limit = %f, want 50.00", limit)
	}
}

// ---------------------------------------------------------------------------
// PIIAdapter
// ---------------------------------------------------------------------------

func TestPIIAdapter_LogPII(t *testing.T) {
	s := openTestStore(t)
	pa := NewPIIAdapter(s)

	requestID := "req-001"
	piiType := "email"
	action := "redacted"
	fieldPath := "messages[0].content"
	ctx := "user input"

	if err := pa.LogPII(requestID, piiType, action, fieldPath, ctx); err != nil {
		t.Fatalf("LogPII: %v", err)
	}

	// Verify the record via the underlying store.
	entries, err := s.GetPIILog(requestID)
	if err != nil {
		t.Fatalf("GetPIILog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	e := entries[0]
	if e.RequestID != requestID {
		t.Errorf("RequestID = %q, want %q", e.RequestID, requestID)
	}
	if e.PIIType != piiType {
		t.Errorf("PIIType = %q, want %q", e.PIIType, piiType)
	}
	if e.Action != action {
		t.Errorf("Action = %q, want %q", e.Action, action)
	}
	if e.FieldPath != fieldPath {
		t.Errorf("FieldPath = %q, want %q", e.FieldPath, fieldPath)
	}
	if e.Context != ctx {
		t.Errorf("Context = %q, want %q", e.Context, ctx)
	}
	if e.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
	if e.ID == 0 {
		t.Error("ID should be assigned by the database (non-zero)")
	}
}

func TestPIIAdapter_LogPIIMultipleEntries(t *testing.T) {
	s := openTestStore(t)
	pa := NewPIIAdapter(s)

	requestID := "req-002"

	if err := pa.LogPII(requestID, "email", "redacted", "field1", "ctx1"); err != nil {
		t.Fatalf("LogPII #1: %v", err)
	}
	if err := pa.LogPII(requestID, "phone", "masked", "field2", "ctx2"); err != nil {
		t.Fatalf("LogPII #2: %v", err)
	}

	entries, err := s.GetPIILog(requestID)
	if err != nil {
		t.Fatalf("GetPIILog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	// Entries are ordered by timestamp ascending.
	if entries[0].PIIType != "email" {
		t.Errorf("entries[0].PIIType = %q, want %q", entries[0].PIIType, "email")
	}
	if entries[1].PIIType != "phone" {
		t.Errorf("entries[1].PIIType = %q, want %q", entries[1].PIIType, "phone")
	}
}
