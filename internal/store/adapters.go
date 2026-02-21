package store

import (
	"database/sql"
	"errors"
	"time"

	cachepkg "github.com/allaspects/tokenman/internal/cache"
)

// FingerprintAdapter adapts Store to compress.FingerprintStore interface.
type FingerprintAdapter struct {
	store *Store
}

// NewFingerprintAdapter creates a new FingerprintAdapter wrapping the given Store.
func NewFingerprintAdapter(s *Store) *FingerprintAdapter {
	return &FingerprintAdapter{store: s}
}

// UpsertFingerprint inserts or updates a fingerprint record.
func (a *FingerprintAdapter) UpsertFingerprint(hash, contentType string, tokenCount int) error {
	return a.store.UpsertFingerprint(&Fingerprint{
		Hash:        hash,
		ContentType: contentType,
		TokenCount:  int64(tokenCount),
	})
}

// GetFingerprint retrieves the hit count and last seen time for a fingerprint.
// Returns zero values if the fingerprint does not exist.
func (a *FingerprintAdapter) GetFingerprint(hash string) (hitCount int, lastSeen time.Time, err error) {
	f, err := a.store.GetFingerprint(hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339, f.LastSeen)
	return int(f.HitCount), t, nil
}

// CacheAdapter adapts Store to cache.CacheStore interface.
type CacheAdapter struct {
	store *Store
}

// NewCacheAdapter creates a new CacheAdapter wrapping the given Store.
func NewCacheAdapter(s *Store) *CacheAdapter {
	return &CacheAdapter{store: s}
}

// GetCache retrieves a cache entry by key, converting from store.CacheEntry
// to cache.CacheEntry.
func (a *CacheAdapter) GetCache(key string) (*cachepkg.CacheEntry, error) {
	sc, err := a.store.GetCache(key)
	if err != nil {
		return nil, err
	}
	createdAt, _ := time.Parse(time.RFC3339, sc.CreatedAt)
	expiresAt, _ := time.Parse(time.RFC3339, sc.ExpiresAt)
	return &cachepkg.CacheEntry{
		Body:        sc.ResponseBody,
		StatusCode:  200,
		ContentType: "application/json",
		Model:       sc.Model,
		CreatedAt:   createdAt,
		ExpiresAt:   expiresAt,
		TokensSaved: int(sc.TokensSaved),
	}, nil
}

// SetCache stores a cache entry, converting from cache.CacheEntry to
// store.CacheEntry.
func (a *CacheAdapter) SetCache(key string, entry *cachepkg.CacheEntry) error {
	return a.store.SetCache(&CacheEntry{
		Key:          key,
		Model:        entry.Model,
		RequestHash:  key,
		ResponseBody: entry.Body,
		TokensSaved:  int64(entry.TokensSaved),
		CreatedAt:    entry.CreatedAt.Format(time.RFC3339),
		ExpiresAt:    entry.ExpiresAt.Format(time.RFC3339),
		HitCount:     0,
	})
}

// DeleteExpired removes all expired cache entries from the store.
func (a *CacheAdapter) DeleteExpired() error {
	_, err := a.store.DeleteExpired()
	return err
}

// BudgetAdapter adapts Store to security.BudgetStore interface.
type BudgetAdapter struct {
	store *Store
}

// NewBudgetAdapter creates a new BudgetAdapter wrapping the given Store.
func NewBudgetAdapter(s *Store) *BudgetAdapter {
	return &BudgetAdapter{store: s}
}

// GetBudget retrieves the current spending amount and limit for a budget period.
// Returns zero values if no budget record exists.
func (a *BudgetAdapter) GetBudget(period, periodStart string) (amount, limit float64, err error) {
	b, err := a.store.GetBudget(period, periodStart)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	return b.AmountUSD, b.LimitUSD, nil
}

// AddSpending records spending against a budget period.
func (a *BudgetAdapter) AddSpending(period, periodStart string, amount, limit float64) error {
	return a.store.AddSpending(period, periodStart, amount, limit)
}

// PIIAdapter wraps the Store for PII logging.
type PIIAdapter struct {
	store *Store
}

// NewPIIAdapter creates a new PIIAdapter wrapping the given Store.
func NewPIIAdapter(s *Store) *PIIAdapter {
	return &PIIAdapter{store: s}
}

// LogPII records a PII detection event.
func (a *PIIAdapter) LogPII(requestID, piiType, action, fieldPath, context string) error {
	return a.store.LogPII(&PIILogEntry{
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		PIIType:   piiType,
		Action:    action,
		FieldPath: fieldPath,
		Context:   context,
	})
}
