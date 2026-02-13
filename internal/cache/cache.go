// Licensed to Andrew Kroh under one or more agreements.
// Andrew Kroh licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package cache provides an in-memory token cache with TTL-based expiration.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/andrewkroh/traefik-github-auth/internal/validator"
)

// Entry represents a single cached entry with an expiration time.
type Entry struct {
	// Result is the cached validation result (zero value for negative entries).
	Result validator.ValidationResult

	// Err is non-nil for negative cache entries (e.g., unauthorized tokens).
	Err error

	// ExpiresAt is the time at which this entry should be considered expired.
	ExpiresAt time.Time
}

// Cache is an in-memory cache for token validation results.
type Cache struct {
	ttl     time.Duration
	maxSize int

	mu      sync.RWMutex
	entries map[string]Entry

	stop chan struct{}

	hits       metric.Int64Counter
	misses     metric.Int64Counter
	evictions  metric.Int64Counter
	entryGauge metric.Int64UpDownCounter
}

// hashToken returns the hex-encoded SHA-256 hash of the raw token.
// The raw token is never stored.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// New creates a new Cache with the specified TTL and maximum number of entries.
// A background goroutine is started to periodically remove expired entries.
// Call Stop to terminate the background goroutine.
//
// If ttl is 0, the cache is effectively disabled: Get always returns false
// and Set is a no-op. The maxSize parameter limits the number of entries;
// when the cache is full, the entry closest to expiry is evicted.
// A maxSize of 0 or less means no limit (not recommended for production).
func New(ttl time.Duration, maxSize int) *Cache {
	meter := otel.Meter("github_auth.cache")

	hits, _ := meter.Int64Counter("github_auth.cache.hits",
		metric.WithDescription("Number of cache hits"),
	)
	misses, _ := meter.Int64Counter("github_auth.cache.misses",
		metric.WithDescription("Number of cache misses"),
	)
	evictions, _ := meter.Int64Counter("github_auth.cache.evictions",
		metric.WithDescription("Number of cache evictions"),
	)
	entryGauge, _ := meter.Int64UpDownCounter("github_auth.cache.entries",
		metric.WithDescription("Current number of cache entries"),
	)

	c := &Cache{
		ttl:        ttl,
		maxSize:    maxSize,
		entries:    make(map[string]Entry),
		stop:       make(chan struct{}),
		hits:       hits,
		misses:     misses,
		evictions:  evictions,
		entryGauge: entryGauge,
	}

	if ttl > 0 {
		go c.cleanupLoop()
	}

	return c
}

// cleanupLoop periodically removes expired entries from the cache.
// It runs every TTL/2 or every 30 seconds, whichever is smaller.
func (c *Cache) cleanupLoop() {
	interval := c.ttl / 2
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	if interval <= 0 {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			c.removeExpired()
		}
	}
}

// removeExpired removes all entries that have passed their expiration time.
func (c *Cache) removeExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
			c.entryGauge.Add(nil, -1)
		}
	}
}

// Get retrieves a cached entry for the given token.
// Returns the result, an optional error (for negative cache entries),
// and whether the entry was found.
//
// If the cache was created with a zero TTL, Get always returns a miss.
func (c *Cache) Get(token string) (validator.ValidationResult, error, bool) {
	if c.ttl == 0 {
		c.misses.Add(nil, 1)
		return validator.ValidationResult{}, nil, false
	}

	key := hashToken(token)

	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(nil, 1)
		return validator.ValidationResult{}, nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		c.misses.Add(nil, 1)
		return validator.ValidationResult{}, nil, false
	}

	c.hits.Add(nil, 1)
	return entry.Result, entry.Err, true
}

// Set stores a validation result for the given token.
// Pass a non-nil err to cache a negative result (e.g., unauthorized).
// The entry expires after the cache's TTL has elapsed.
//
// If the cache is full (maxSize > 0 and len(entries) >= maxSize),
// the entry closest to expiry is evicted before inserting the new entry.
//
// If the cache was created with a zero TTL, Set is a no-op.
func (c *Cache) Set(token string, result validator.ValidationResult, err error) {
	if c.ttl == 0 {
		return
	}

	key := hashToken(token)

	c.mu.Lock()
	defer c.mu.Unlock()

	_, exists := c.entries[key]

	// Evict the entry closest to expiry if we're at capacity and this is a new key.
	if !exists && c.maxSize > 0 && len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[key] = Entry{
		Result:    result,
		Err:       err,
		ExpiresAt: time.Now().Add(c.ttl),
	}
	if !exists {
		c.entryGauge.Add(nil, 1)
	}
}

// evictOldest removes the entry with the earliest ExpiresAt time.
// Must be called with c.mu held.
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, entry := range c.entries {
		if first || entry.ExpiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.ExpiresAt
			first = false
		}
	}

	if !first {
		delete(c.entries, oldestKey)
		c.entryGauge.Add(nil, -1)
		c.evictions.Add(nil, 1)
	}
}

// Delete removes a cached entry for the given token.
// This is useful for cache invalidation on errors.
func (c *Cache) Delete(token string) {
	key := hashToken(token)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; exists {
		delete(c.entries, key)
		c.entryGauge.Add(nil, -1)
	}
}

// Stop terminates the background cleanup goroutine.
func (c *Cache) Stop() {
	select {
	case <-c.stop:
		// Already stopped.
	default:
		close(c.stop)
	}
}

// Len returns the number of entries currently in the cache.
// This includes entries that may have expired but have not yet been cleaned up.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
