package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Fingerprint represents a content fingerprint used for deduplication
// and token-saving analysis.
type Fingerprint struct {
	Hash        string
	ContentType string
	TokenCount  int64
	FirstSeen   string
	LastSeen    string
	HitCount    int64
}

// UpsertFingerprint inserts a new fingerprint or, if the hash already
// exists, increments its hit_count and updates last_seen.
func (s *Store) UpsertFingerprint(f *Fingerprint) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if f.FirstSeen == "" {
		f.FirstSeen = now
	}
	if f.LastSeen == "" {
		f.LastSeen = now
	}

	_, err := s.writer.Exec(`
		INSERT INTO fingerprints (hash, content_type, token_count, first_seen, last_seen, hit_count)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			last_seen = excluded.last_seen,
			hit_count = fingerprints.hit_count + 1`,
		f.Hash, f.ContentType, f.TokenCount, f.FirstSeen, f.LastSeen, f.HitCount,
	)
	if err != nil {
		return fmt.Errorf("store: upsert fingerprint: %w", err)
	}
	return nil
}

// GetFingerprint retrieves a fingerprint by its hash.
// Returns sql.ErrNoRows (wrapped) if not found.
func (s *Store) GetFingerprint(hash string) (*Fingerprint, error) {
	f := &Fingerprint{}
	err := s.reader.QueryRow(`
		SELECT hash, content_type, token_count, first_seen, last_seen, hit_count
		FROM fingerprints WHERE hash = ?`, hash,
	).Scan(
		&f.Hash, &f.ContentType, &f.TokenCount,
		&f.FirstSeen, &f.LastSeen, &f.HitCount,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("store: get fingerprint %s: %w", hash, err)
		}
		return nil, fmt.Errorf("store: get fingerprint %s: %w", hash, err)
	}
	return f, nil
}

// ListFingerprints returns all fingerprints ordered by hit_count descending.
func (s *Store) ListFingerprints() ([]*Fingerprint, error) {
	rows, err := s.reader.Query(`
		SELECT hash, content_type, token_count, first_seen, last_seen, hit_count
		FROM fingerprints
		ORDER BY hit_count DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: list fingerprints: %w", err)
	}
	defer rows.Close()

	var results []*Fingerprint
	for rows.Next() {
		f := &Fingerprint{}
		if err := rows.Scan(
			&f.Hash, &f.ContentType, &f.TokenCount,
			&f.FirstSeen, &f.LastSeen, &f.HitCount,
		); err != nil {
			return nil, fmt.Errorf("store: scan fingerprint row: %w", err)
		}
		results = append(results, f)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list fingerprints iteration: %w", err)
	}
	return results, nil
}
