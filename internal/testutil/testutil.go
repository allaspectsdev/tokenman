package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/allaspectsdev/tokenman/internal/config"
	"github.com/allaspectsdev/tokenman/internal/store"
)

// NewTestStore creates an in-memory SQLite store for testing.
// The store is automatically closed when the test completes.
func NewTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// NewTestConfig returns a minimal valid config for testing.
func NewTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = t.TempDir()
	return cfg
}

// TempDir creates a temporary directory for test data.
func TempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// WriteFile writes content to a file in the given directory.
func WriteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	return path
}
