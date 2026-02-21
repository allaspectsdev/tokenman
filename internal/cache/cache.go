package cache

import (
	"context"
	"fmt"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/rs/zerolog/log"

	"github.com/allaspects/tokenman/internal/pipeline"
)

// CacheEntry represents a cached response with metadata.
type CacheEntry struct {
	Body        []byte    `json:"body"`
	StatusCode  int       `json:"status_code"`
	ContentType string    `json:"content_type"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	TokensSaved int       `json:"tokens_saved"`
}

// Expired returns true if the entry has passed its expiration time.
func (e *CacheEntry) Expired() bool {
	return time.Now().After(e.ExpiresAt)
}

// CacheStore is the persistence interface for cached responses. Implementations
// may use SQLite, Redis, or other backends.
type CacheStore interface {
	GetCache(key string) (*CacheEntry, error)
	SetCache(key string, entry *CacheEntry) error
	DeleteExpired() error
}

// CacheMiddleware is a pipeline.Middleware that caches deterministic API
// responses in a two-tier cache (in-memory LRU + persistent store).
type CacheMiddleware struct {
	memory  *lru.Cache[string, *CacheEntry]
	store   CacheStore
	ttl     time.Duration
	enabled bool
}

// Compile-time assertion that CacheMiddleware implements pipeline.Middleware.
var _ pipeline.Middleware = (*CacheMiddleware)(nil)

// NewCacheMiddleware creates a new CacheMiddleware.
//
//   - store is the persistent cache backend (may be nil for memory-only).
//   - ttlSeconds is the time-to-live for cache entries in seconds.
//   - maxMemoryEntries is the maximum number of entries in the in-memory LRU cache.
//   - enabled controls whether the middleware is active.
func NewCacheMiddleware(store CacheStore, ttlSeconds int, maxMemoryEntries int, enabled bool) (*CacheMiddleware, error) {
	if maxMemoryEntries <= 0 {
		maxMemoryEntries = 1000
	}

	memCache, err := lru.New[string, *CacheEntry](maxMemoryEntries)
	if err != nil {
		return nil, fmt.Errorf("cache: creating LRU: %w", err)
	}

	return &CacheMiddleware{
		memory:  memCache,
		store:   store,
		ttl:     time.Duration(ttlSeconds) * time.Second,
		enabled: enabled,
	}, nil
}

// Name returns the middleware name.
func (c *CacheMiddleware) Name() string {
	return "cache"
}

// Enabled reports whether this middleware is active.
func (c *CacheMiddleware) Enabled() bool {
	return c.enabled
}

// ProcessRequest checks the cache for a matching entry. If a cache hit is
// found and the entry is not expired, the request is flagged as a cache hit
// and a CachedResponse is stored in the context for the pipeline to
// short-circuit.
func (c *CacheMiddleware) ProcessRequest(ctx context.Context, req *pipeline.Request) (*pipeline.Request, error) {
	if !IsCacheable(req) {
		return req, nil
	}

	key := CacheKey(req.Model, req.Messages, req.Tools)

	// Store the key in metadata for use in ProcessResponse.
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	req.Metadata["cache_key"] = key

	// Tier 1: check in-memory LRU.
	if entry, ok := c.memory.Get(key); ok {
		if !entry.Expired() {
			return c.buildCacheHit(ctx, req, entry)
		}
		// Expired: evict from memory.
		c.memory.Remove(key)
	}

	// Tier 2: check persistent store.
	if c.store != nil {
		entry, err := c.store.GetCache(key)
		if err == nil && entry != nil && !entry.Expired() {
			// Promote to memory cache.
			c.memory.Add(key, entry)
			return c.buildCacheHit(ctx, req, entry)
		}
	}

	return req, nil
}

// buildCacheHit flags the request as a cache hit and stores the CachedResponse
// in the context. The pipeline chain will detect the cache_hit flag and
// short-circuit.
func (c *CacheMiddleware) buildCacheHit(ctx context.Context, req *pipeline.Request, entry *CacheEntry) (*pipeline.Request, error) {
	if req.Flags == nil {
		req.Flags = make(map[string]bool)
	}
	req.Flags["cache_hit"] = true

	// Store cached response in metadata so caller can retrieve it.
	req.Metadata["cached_tokens_saved"] = entry.TokensSaved

	cached := &pipeline.CachedResponse{
		Body:        entry.Body,
		StatusCode:  entry.StatusCode,
		ContentType: entry.ContentType,
	}

	// Store in request metadata so the pipeline chain can retrieve it.
	// (Storing in context via WithCachedResponse would return a new context
	// that the caller never sees, so we use metadata instead.)
	req.Metadata["cached_response"] = cached

	return req, nil
}

// ProcessResponse stores a cacheable response in both the in-memory LRU
// and the persistent store.
func (c *CacheMiddleware) ProcessResponse(ctx context.Context, req *pipeline.Request, resp *pipeline.Response) (*pipeline.Response, error) {
	if !IsCacheable(req) {
		return resp, nil
	}

	// Don't cache error responses.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}

	// Don't re-cache if this was already a cache hit.
	if req.Flags != nil && req.Flags["cache_hit"] {
		return resp, nil
	}

	// Retrieve the cache key computed in ProcessRequest.
	key := ""
	if req.Metadata != nil {
		if k, ok := req.Metadata["cache_key"].(string); ok {
			key = k
		}
	}
	if key == "" {
		key = CacheKey(req.Model, req.Messages, req.Tools)
	}

	now := time.Now()
	entry := &CacheEntry{
		Body:        resp.Body,
		StatusCode:  resp.StatusCode,
		ContentType: "application/json",
		Model:       req.Model,
		CreatedAt:   now,
		ExpiresAt:   now.Add(c.ttl),
		TokensSaved: req.TokensIn + resp.TokensOut,
	}

	// Store in memory.
	c.memory.Add(key, entry)

	// Store in persistent backend.
	if c.store != nil {
		if err := c.store.SetCache(key, entry); err != nil {
			// Log but do not fail the response.
			_ = err
		}
	}

	return resp, nil
}

// StartPurger starts a background goroutine that periodically purges expired
// entries from the persistent store and evicts expired entries from the
// in-memory LRU. It runs every 5 minutes until the context is cancelled.
// The returned channel is closed when the goroutine exits, allowing callers
// to synchronize shutdown before closing the underlying store.
func (c *CacheMiddleware) StartPurger(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Error().Interface("panic", r).Msg("cache purger: recovered from panic")
						}
					}()
					c.purge()
				}()
			}
		}
	}()
	return done
}

// purge removes expired entries from both the persistent store and the
// in-memory LRU cache.
func (c *CacheMiddleware) purge() {
	// Purge persistent store.
	if c.store != nil {
		_ = c.store.DeleteExpired()
	}

	// Evict expired entries from the in-memory LRU.
	keys := c.memory.Keys()
	for _, key := range keys {
		if entry, ok := c.memory.Peek(key); ok {
			if entry.Expired() {
				c.memory.Remove(key)
			}
		}
	}
}
