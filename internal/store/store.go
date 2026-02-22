package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

// Store provides a SQLite-backed persistence layer for TokenMan.
// It uses a two-connection pattern: a single writer connection with
// MaxOpenConns=1 for serialised writes, and a separate reader pool
// for concurrent reads.
type Store struct {
	writer    *sql.DB
	reader    *sql.DB
	path      string
	closeOnce sync.Once
}

// Open creates a new Store backed by the SQLite database at path.
// It creates the parent directory if it does not exist, opens a writer
// connection (single-conn) and a reader pool, enables WAL mode, and
// runs all pending migrations.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create directory %s: %w", dir, err)
	}

	// Writer connection: exactly one connection, serialises all writes.
	writerDSN := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	writer, err := sql.Open("sqlite", writerDSN)
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)

	// Verify the writer connection is usable.
	if err := writer.Ping(); err != nil {
		writer.Close()
		return nil, fmt.Errorf("store: ping writer: %w", err)
	}

	// Reader pool: multiple connections for concurrent reads.
	// Use query_only pragma to enforce read-only behaviour at the connection level.
	readerDSN := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=query_only(ON)"
	reader, err := sql.Open("sqlite", readerDSN)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	reader.SetMaxOpenConns(4)
	reader.SetMaxIdleConns(4)
	reader.SetConnMaxLifetime(0)

	if err := reader.Ping(); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("store: ping reader: %w", err)
	}

	s := &Store{
		writer: writer,
		reader: reader,
		path:   path,
	}

	// Run pending migrations.
	if err := s.Migrate(); err != nil {
		s.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	return s, nil
}

// Close closes both the writer and reader database connections.
// It is safe to call Close multiple times.
func (s *Store) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		if s.writer != nil {
			if err := s.writer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if s.reader != nil {
			if err := s.reader.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// Writer returns the writer database handle. Exported for advanced usage;
// prefer the typed methods on Store for regular operations.
func (s *Store) Writer() *sql.DB {
	return s.writer
}

// Reader returns the reader database handle.
func (s *Store) Reader() *sql.DB {
	return s.reader
}

// Path returns the filesystem path of the database.
func (s *Store) Path() string {
	return s.path
}

// Ping verifies that both the writer and reader database connections are alive
// by executing a simple SELECT 1 query on each.
func (s *Store) Ping() error {
	if err := s.writer.Ping(); err != nil {
		return fmt.Errorf("store: writer ping: %w", err)
	}
	if err := s.reader.Ping(); err != nil {
		return fmt.Errorf("store: reader ping: %w", err)
	}
	return nil
}

// Prune removes data older than retentionDays from requests, cache,
// and pii_log tables. It returns the total number of rows deleted.
func (s *Store) Prune(retentionDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339)
	var total int64

	queries := []string{
		"DELETE FROM requests WHERE timestamp < ?",
		"DELETE FROM cache WHERE expires_at < ?",
		"DELETE FROM pii_log WHERE timestamp < ?",
	}

	for _, q := range queries {
		result, err := s.writer.Exec(q, cutoff)
		if err != nil {
			return total, fmt.Errorf("store: prune: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("store: prune rows affected: %w", err)
		}
		total += n
	}

	return total, nil
}
