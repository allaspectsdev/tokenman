package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Request represents a single proxied API request record.
type Request struct {
	ID           string
	Timestamp    string
	Method       string
	Path         string
	Format       string
	Model        string
	TokensIn     int64
	TokensOut    int64
	TokensCached int64
	TokensSaved  int64
	CostUSD      float64
	SavingsUSD   float64
	LatencyMs    int64
	StatusCode   int
	CacheHit     bool
	RequestType  string
	Provider     string
	ErrorMessage string
	RequestBody  string
	ResponseBody string
	Project      string
}

// RequestStats holds aggregate statistics for a range of requests.
type RequestStats struct {
	TotalRequests  int64
	TotalTokensIn  int64
	TotalTokensOut int64
	TotalTokensSaved int64
	TotalCost      float64
	TotalSavings   float64
	CacheHits      int64
	CacheMisses    int64
}

// InsertRequest stores a new request record. The caller is responsible
// for providing a unique ID (typically a UUID).
func (s *Store) InsertRequest(r *Request) error {
	cacheHitInt := 0
	if r.CacheHit {
		cacheHitInt = 1
	}

	_, err := s.writer.Exec(`
		INSERT INTO requests (
			id, timestamp, method, path, format, model,
			tokens_in, tokens_out, tokens_cached, tokens_saved,
			cost_usd, savings_usd, latency_ms, status_code,
			cache_hit, request_type, provider, error_message,
			request_body, response_body, project
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Timestamp, r.Method, r.Path, r.Format, r.Model,
		r.TokensIn, r.TokensOut, r.TokensCached, r.TokensSaved,
		r.CostUSD, r.SavingsUSD, r.LatencyMs, r.StatusCode,
		cacheHitInt, r.RequestType, r.Provider, r.ErrorMessage,
		r.RequestBody, r.ResponseBody, r.Project,
	)
	if err != nil {
		return fmt.Errorf("store: insert request: %w", err)
	}
	return nil
}

// GetRequest retrieves a single request by its ID.
// Returns sql.ErrNoRows if the request does not exist.
func (s *Store) GetRequest(id string) (*Request, error) {
	r := &Request{}
	var cacheHitInt int

	err := s.reader.QueryRow(`
		SELECT id, timestamp, method, path, format, model,
		       tokens_in, tokens_out, tokens_cached, tokens_saved,
		       cost_usd, savings_usd, latency_ms, status_code,
		       cache_hit, request_type, provider, error_message,
		       request_body, response_body, project
		FROM requests WHERE id = ?`, id,
	).Scan(
		&r.ID, &r.Timestamp, &r.Method, &r.Path, &r.Format, &r.Model,
		&r.TokensIn, &r.TokensOut, &r.TokensCached, &r.TokensSaved,
		&r.CostUSD, &r.SavingsUSD, &r.LatencyMs, &r.StatusCode,
		&cacheHitInt, &r.RequestType, &r.Provider, &r.ErrorMessage,
		&r.RequestBody, &r.ResponseBody, &r.Project,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get request %s: %w", id, err)
	}

	r.CacheHit = cacheHitInt != 0
	return r, nil
}

// ListRequests returns a page of requests ordered by timestamp descending.
func (s *Store) ListRequests(limit, offset int) ([]*Request, error) {
	rows, err := s.reader.Query(`
		SELECT id, timestamp, method, path, format, model,
		       tokens_in, tokens_out, tokens_cached, tokens_saved,
		       cost_usd, savings_usd, latency_ms, status_code,
		       cache_hit, request_type, provider, error_message
		FROM requests
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list requests: %w", err)
	}
	defer rows.Close()

	var results []*Request
	for rows.Next() {
		r := &Request{}
		var cacheHitInt int
		if err := rows.Scan(
			&r.ID, &r.Timestamp, &r.Method, &r.Path, &r.Format, &r.Model,
			&r.TokensIn, &r.TokensOut, &r.TokensCached, &r.TokensSaved,
			&r.CostUSD, &r.SavingsUSD, &r.LatencyMs, &r.StatusCode,
			&cacheHitInt, &r.RequestType, &r.Provider, &r.ErrorMessage,
		); err != nil {
			return nil, fmt.Errorf("store: scan request row: %w", err)
		}
		r.CacheHit = cacheHitInt != 0
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list requests iteration: %w", err)
	}
	return results, nil
}

// GetRequestStats computes aggregate statistics for all requests whose
// timestamp is >= since.
func (s *Store) GetRequestStats(since time.Time) (*RequestStats, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	stats := &RequestStats{}

	err := s.reader.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(tokens_in), 0),
			COALESCE(SUM(tokens_out), 0),
			COALESCE(SUM(tokens_saved), 0),
			COALESCE(SUM(cost_usd), 0.0),
			COALESCE(SUM(savings_usd), 0.0),
			COALESCE(SUM(CASE WHEN cache_hit = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cache_hit = 0 THEN 1 ELSE 0 END), 0)
		FROM requests
		WHERE timestamp >= ?`, sinceStr,
	).Scan(
		&stats.TotalRequests,
		&stats.TotalTokensIn,
		&stats.TotalTokensOut,
		&stats.TotalTokensSaved,
		&stats.TotalCost,
		&stats.TotalSavings,
		&stats.CacheHits,
		&stats.CacheMisses,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return stats, nil
		}
		return nil, fmt.Errorf("store: get request stats: %w", err)
	}

	return stats, nil
}
