package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openCoreTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestOpen_Close(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if st.Path() != path {
		t.Errorf("Path: got %q, want %q", st.Path(), path)
	}
	if st.Writer() == nil {
		t.Error("Writer is nil")
	}
	if st.Reader() == nil {
		t.Error("Reader is nil")
	}

	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestOpen_CreatesDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open with nested dir: %v", err)
	}
	st.Close()
}

func TestPing(t *testing.T) {
	st := openCoreTestStore(t)
	if err := st.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestInsertRequest_GetRequest(t *testing.T) {
	st := openCoreTestStore(t)

	req := &Request{
		ID:          "req-001",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Method:      "POST",
		Path:        "/v1/messages",
		Format:      "anthropic",
		Model:       "claude-sonnet-4-20250514",
		TokensIn:    100,
		TokensOut:   200,
		TokensSaved: 50,
		CostUSD:     0.01,
		SavingsUSD:  0.005,
		LatencyMs:   150,
		StatusCode:  200,
		CacheHit:    false,
		RequestType: "normal",
		Provider:    "anthropic",
		RequestBody: `{"model":"claude-sonnet-4-20250514"}`,
		Project:     "test-project",
	}

	if err := st.InsertRequest(req); err != nil {
		t.Fatalf("InsertRequest: %v", err)
	}

	got, err := st.GetRequest("req-001")
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}

	if got.ID != req.ID {
		t.Errorf("ID: got %q, want %q", got.ID, req.ID)
	}
	if got.Model != req.Model {
		t.Errorf("Model: got %q, want %q", got.Model, req.Model)
	}
	if got.TokensIn != req.TokensIn {
		t.Errorf("TokensIn: got %d, want %d", got.TokensIn, req.TokensIn)
	}
	if got.TokensOut != req.TokensOut {
		t.Errorf("TokensOut: got %d, want %d", got.TokensOut, req.TokensOut)
	}
	if got.CacheHit != req.CacheHit {
		t.Errorf("CacheHit: got %v, want %v", got.CacheHit, req.CacheHit)
	}
	if got.Project != req.Project {
		t.Errorf("Project: got %q, want %q", got.Project, req.Project)
	}
}

func TestGetRequest_NotFound(t *testing.T) {
	st := openCoreTestStore(t)

	_, err := st.GetRequest("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent request")
	}
}

func TestListRequests(t *testing.T) {
	st := openCoreTestStore(t)

	for i := 0; i < 5; i++ {
		req := &Request{
			ID:          "list-" + time.Now().Format("150405.000000") + string(rune('0'+i)),
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Method:      "POST",
			Path:        "/v1/messages",
			Format:      "anthropic",
			Model:       "claude-sonnet-4-20250514",
			StatusCode:  200,
			RequestType: "normal",
		}
		if err := st.InsertRequest(req); err != nil {
			t.Fatalf("InsertRequest %d: %v", i, err)
		}
	}

	results, err := st.ListRequests(3, 0)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("ListRequests(3, 0): got %d results, want 3", len(results))
	}

	results, err = st.ListRequests(10, 3)
	if err != nil {
		t.Fatalf("ListRequests offset: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("ListRequests(10, 3): got %d results, want 2", len(results))
	}
}

func TestGetRequestStats(t *testing.T) {
	st := openCoreTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		req := &Request{
			ID:         "stats-" + string(rune('a'+i)),
			Timestamp:  now.Format(time.RFC3339),
			Method:     "POST",
			Path:       "/v1/messages",
			Format:     "anthropic",
			Model:      "claude-sonnet-4-20250514",
			TokensIn:   100,
			TokensOut:  200,
			CostUSD:    0.01,
			StatusCode: 200,
			CacheHit:   i == 0, // first one is a cache hit
			RequestType: "normal",
		}
		if err := st.InsertRequest(req); err != nil {
			t.Fatalf("InsertRequest: %v", err)
		}
	}

	stats, err := st.GetRequestStats(now.Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("GetRequestStats: %v", err)
	}

	if stats.TotalRequests != 3 {
		t.Errorf("TotalRequests: got %d, want 3", stats.TotalRequests)
	}
	if stats.CacheHits != 1 {
		t.Errorf("CacheHits: got %d, want 1", stats.CacheHits)
	}
	if stats.CacheMisses != 2 {
		t.Errorf("CacheMisses: got %d, want 2", stats.CacheMisses)
	}
}

func TestPrune(t *testing.T) {
	st := openCoreTestStore(t)

	oldTime := time.Now().UTC().AddDate(0, 0, -60).Format(time.RFC3339)
	newTime := time.Now().UTC().Format(time.RFC3339)

	// Insert old and new requests.
	for i, ts := range []string{oldTime, oldTime, newTime} {
		req := &Request{
			ID:          "prune-" + string(rune('a'+i)),
			Timestamp:   ts,
			Method:      "POST",
			Path:        "/v1/messages",
			Format:      "anthropic",
			Model:       "claude-sonnet-4-20250514",
			StatusCode:  200,
			RequestType: "normal",
		}
		if err := st.InsertRequest(req); err != nil {
			t.Fatalf("InsertRequest: %v", err)
		}
	}

	pruned, err := st.Prune(30) // retain 30 days
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if pruned < 2 {
		t.Errorf("Prune: got %d rows deleted, want at least 2", pruned)
	}

	// Verify the new request still exists.
	remaining, err := st.ListRequests(100, 0)
	if err != nil {
		t.Fatalf("ListRequests after prune: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("after prune: got %d requests, want 1", len(remaining))
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	st := openCoreTestStore(t)

	var wg sync.WaitGroup

	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			req := &Request{
				ID:          "conc-" + string(rune('a'+n)),
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				Method:      "POST",
				Path:        "/v1/messages",
				Format:      "anthropic",
				Model:       "claude-sonnet-4-20250514",
				StatusCode:  200,
				RequestType: "normal",
			}
			if err := st.InsertRequest(req); err != nil {
				t.Errorf("concurrent InsertRequest %d: %v", n, err)
			}
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = st.ListRequests(10, 0)
		}()
	}

	wg.Wait()
}

func TestWALMode(t *testing.T) {
	st := openCoreTestStore(t)

	var mode string
	err := st.Writer().QueryRow("PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode: got %q, want %q", mode, "wal")
	}
}

func TestMigrations(t *testing.T) {
	st := openCoreTestStore(t)

	var version int
	err := st.Writer().QueryRow("SELECT MAX(version) FROM migrations").Scan(&version)
	if err != nil {
		t.Fatalf("query migration version: %v", err)
	}

	expected := len(migrations)
	if version != expected {
		t.Errorf("migration version: got %d, want %d", version, expected)
	}
}

func TestInsertRequest_CacheHitFlag(t *testing.T) {
	st := openCoreTestStore(t)

	req := &Request{
		ID:          "cache-flag-test",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Method:      "POST",
		Path:        "/v1/messages",
		Format:      "anthropic",
		Model:       "claude-sonnet-4-20250514",
		StatusCode:  200,
		CacheHit:    true,
		RequestType: "cache_hit",
	}
	if err := st.InsertRequest(req); err != nil {
		t.Fatalf("InsertRequest: %v", err)
	}

	got, err := st.GetRequest("cache-flag-test")
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if !got.CacheHit {
		t.Error("CacheHit: got false, want true")
	}
}
