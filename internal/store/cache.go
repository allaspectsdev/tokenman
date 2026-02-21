package store

import (
	"database/sql"
	"fmt"
	"time"
)

// CacheEntry represents a cached response stored in the cache table.
type CacheEntry struct {
	Key          string
	Model        string
	RequestHash  string
	ResponseBody []byte
	TokensSaved  int64
	CreatedAt    string
	ExpiresAt    string
	HitCount     int64
	LastHit      sql.NullString
}

// GetCache retrieves a cache entry by its key.
// Returns sql.ErrNoRows (wrapped) if the key does not exist.
func (s *Store) GetCache(key string) (*CacheEntry, error) {
	c := &CacheEntry{}
	err := s.reader.QueryRow(`
		SELECT key, model, request_hash, response_body, tokens_saved,
		       created_at, expires_at, hit_count, last_hit
		FROM cache WHERE key = ?`, key,
	).Scan(
		&c.Key, &c.Model, &c.RequestHash, &c.ResponseBody, &c.TokensSaved,
		&c.CreatedAt, &c.ExpiresAt, &c.HitCount, &c.LastHit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get cache %s: %w", key, err)
	}
	return c, nil
}

// SetCache inserts or replaces a cache entry. If an entry with the same
// key already exists it is overwritten.
func (s *Store) SetCache(c *CacheEntry) error {
	_, err := s.writer.Exec(`
		INSERT OR REPLACE INTO cache (
			key, model, request_hash, response_body, tokens_saved,
			created_at, expires_at, hit_count, last_hit
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Key, c.Model, c.RequestHash, c.ResponseBody, c.TokensSaved,
		c.CreatedAt, c.ExpiresAt, c.HitCount, c.LastHit,
	)
	if err != nil {
		return fmt.Errorf("store: set cache: %w", err)
	}
	return nil
}

// DeleteExpired removes all cache entries whose expires_at timestamp is
// in the past. It returns the number of rows deleted.
func (s *Store) DeleteExpired() (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.writer.Exec("DELETE FROM cache WHERE expires_at < ?", now)
	if err != nil {
		return 0, fmt.Errorf("store: delete expired cache: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete expired rows affected: %w", err)
	}
	return n, nil
}

// IncrementHitCount atomically increments the hit_count for a cache
// entry and updates last_hit to the current time.
func (s *Store) IncrementHitCount(key string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.writer.Exec(`
		UPDATE cache SET hit_count = hit_count + 1, last_hit = ?
		WHERE key = ?`, now, key,
	)
	if err != nil {
		return fmt.Errorf("store: increment hit count: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: increment hit count rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: increment hit count: %w", sql.ErrNoRows)
	}
	return nil
}
