package cache

import (
	"context"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// TieredCache implements a multi-tier cache system (e.g., L1: Memory, L2: Redis)
type TieredCache struct {
	*BaseCache

	l1Cache Cache // Fast cache (e.g., memory)
	l2Cache Cache // Slower but larger cache (e.g., Redis)
}

// NewTieredCache creates a new multi-tier cache with L1 and L2 cache implementations.
// The L1 cache is typically a fast, small cache (like memory) while L2 is a larger,
// persistent cache (like Redis). Reads check L1 first, then L2, with automatic promotion
// of hot data to L1. Writes go to both tiers for consistency.
//
// Example usage:
//
//	memCache := cache.NewMemoryCache()
//	redisCache := cache.NewRedisCache(redisConfig)
//	tiered := cache.NewTieredCache(memCache, redisCache)
func NewTieredCache(l1Cache, l2Cache Cache) *TieredCache {
	return NewTieredCacheWithConfig(l1Cache, l2Cache, DefaultConfig())
}

// NewTieredCacheWithConfig creates a new tiered cache with custom configuration.
// This allows fine-tuning of cache behavior across both tiers while maintaining
// the performance benefits of multi-tier caching architecture.
func NewTieredCacheWithConfig(l1Cache, l2Cache Cache, config Config) *TieredCache {
	return &TieredCache{
		BaseCache: NewBaseCache(config),
		l1Cache:   l1Cache,
		l2Cache:   l2Cache,
	}
}

// WithEventHandler sets the event handler for cache events
func (tc *TieredCache) WithEventHandler(handler EventHandler) *TieredCache {
	tc.config.EventHandler = handler
	tc.config.EnableEvents = true
	return tc
}

// Get retrieves a value from the cache, checking L1 first, then L2
func (tc *TieredCache) Get(ctx context.Context, key string, dest interface{}) (bool, error) {
	// Try L1 cache first
	found, err := tc.l1Cache.Get(ctx, key, dest)
	if err != nil {
		tc.recordError(err)
		tc.emitEvent(EventGet, key, nil, err)
		return false, err
	}

	if found {
		tc.recordHit()
		tc.emitEvent(EventGet, key, dest, nil)
		return true, nil
	}

	// Try L2 cache
	found, err = tc.l2Cache.Get(ctx, key, dest)
	if err != nil {
		tc.recordError(err)
		tc.emitEvent(EventGet, key, nil, err)
		return false, err
	}

	if found {
		// Backfill L1 cache with the value from L2
		// Use a shorter TTL for L1 to avoid memory bloat
		l1TTL := tc.config.DefaultTTL
		if l1TTL > time.Hour {
			l1TTL = time.Hour // Limit L1 TTL to 1 hour max
		}

		err = tc.l1Cache.Set(ctx, key, dest, l1TTL)
		if err != nil {
			// Log error but don't fail the operation
			tc.recordError(err)
		}

		tc.recordHit()
		tc.emitEvent(EventGet, key, dest, nil)
		return true, nil
	}

	tc.recordMiss()
	tc.emitEvent(EventGet, key, nil, nil)
	return false, nil
}

// Set stores a value in both L1 and L2 caches
func (tc *TieredCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	effectiveTTL := tc.calculateTTL(ttl)

	// Set in L2 cache first (source of truth)
	err := tc.l2Cache.Set(ctx, key, value, effectiveTTL)
	if err != nil {
		tc.recordError(err)
		tc.emitEvent(EventSet, key, value, err)
		return err
	}

	// Set in L1 cache with potentially shorter TTL
	l1TTL := effectiveTTL
	if l1TTL > time.Hour {
		l1TTL = time.Hour // Limit L1 TTL to prevent memory bloat
	}

	err = tc.l1Cache.Set(ctx, key, value, l1TTL)
	if err != nil {
		// Log error but don't fail the operation since L2 succeeded
		tc.recordError(err)
	}

	tc.updateStats(func(s *Stats) { s.Sets++ })
	tc.emitEvent(EventSet, key, value, nil)
	return nil
}

// Delete removes a value from both L1 and L2 caches
func (tc *TieredCache) Delete(ctx context.Context, key string) error {
	// Delete from both caches
	var l1Err, l2Err error

	l1Err = tc.l1Cache.Delete(ctx, key)
	l2Err = tc.l2Cache.Delete(ctx, key)

	// Return error if both failed
	if l1Err != nil && l2Err != nil {
		err := apperror.NewError("failed to delete from both L1 and L2 caches")
		tc.recordError(err)
		tc.emitEvent(EventDelete, key, nil, err)
		return err
	}

	tc.updateStats(func(s *Stats) { s.Deletes++ })
	tc.emitEvent(EventDelete, key, nil, nil)
	return nil
}

// Exists checks if a key exists in either L1 or L2 cache
func (tc *TieredCache) Exists(ctx context.Context, key string) (bool, error) {
	// Check L1 first
	exists, err := tc.l1Cache.Exists(ctx, key)
	if err != nil {
		tc.recordError(err)
		return false, err
	}

	if exists {
		return true, nil
	}

	// Check L2
	exists, err = tc.l2Cache.Exists(ctx, key)
	if err != nil {
		tc.recordError(err)
		return false, err
	}

	return exists, nil
}

// Clear removes all entries from both L1 and L2 caches
func (tc *TieredCache) Clear(ctx context.Context) error {
	var l1Err, l2Err error

	l1Err = tc.l1Cache.Clear(ctx)
	l2Err = tc.l2Cache.Clear(ctx)

	// Return error if both failed
	if l1Err != nil && l2Err != nil {
		err := apperror.NewError("failed to clear both L1 and L2 caches")
		tc.recordError(err)
		tc.emitEvent(EventClear, "", nil, err)
		return err
	}

	tc.emitEvent(EventClear, "", nil, nil)
	return nil
}

// GetMulti retrieves multiple values from the cache
func (tc *TieredCache) GetMulti(ctx context.Context, keys []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// Try L1 cache first
	l1Results, err := tc.l1Cache.GetMulti(ctx, keys)
	if err != nil {
		tc.recordError(err)
		// Continue to L2 even if L1 fails
	}
	if err == nil {
		for key, value := range l1Results {
			result[key] = value
		}
	}

	// Find keys not found in L1
	var missingKeys []string
	for _, key := range keys {
		if _, found := result[key]; !found {
			missingKeys = append(missingKeys, key)
		}
	}

	// Try L2 cache for missing keys
	if len(missingKeys) > 0 {
		l2Results, err := tc.l2Cache.GetMulti(ctx, missingKeys)
		if err != nil {
			tc.recordError(err)
			return result, err
		}

		// Backfill L1 cache with values from L2
		backfillItems := make(map[string]interface{})
		for key, value := range l2Results {
			result[key] = value
			backfillItems[key] = value
		}

		if len(backfillItems) > 0 {
			// Use shorter TTL for L1 backfill
			l1TTL := tc.config.DefaultTTL
			if l1TTL > time.Hour {
				l1TTL = time.Hour
			}

			err = tc.l1Cache.SetMulti(ctx, backfillItems, l1TTL)
			if err != nil {
				tc.recordError(err)
			}
		}
	}

	return result, nil
}

// SetMulti stores multiple values in both L1 and L2 caches
func (tc *TieredCache) SetMulti(ctx context.Context, items map[string]interface{}, ttl time.Duration) error {
	effectiveTTL := tc.calculateTTL(ttl)

	// Set in L2 cache first
	err := tc.l2Cache.SetMulti(ctx, items, effectiveTTL)
	if err != nil {
		tc.recordError(err)
		return err
	}

	// Set in L1 cache with potentially shorter TTL
	l1TTL := effectiveTTL
	if l1TTL > time.Hour {
		l1TTL = time.Hour
	}

	err = tc.l1Cache.SetMulti(ctx, items, l1TTL)
	if err != nil {
		tc.recordError(err)
	}

	tc.updateStats(func(s *Stats) { s.Sets += int64(len(items)) })
	return nil
}

// DeleteMulti removes multiple values from both L1 and L2 caches
func (tc *TieredCache) DeleteMulti(ctx context.Context, keys []string) error {
	var l1Err, l2Err error

	l1Err = tc.l1Cache.DeleteMulti(ctx, keys)
	l2Err = tc.l2Cache.DeleteMulti(ctx, keys)

	// Return error if both failed
	if l1Err != nil && l2Err != nil {
		err := apperror.NewError("failed to delete from both L1 and L2 caches")
		tc.recordError(err)
		return err
	}

	tc.updateStats(func(s *Stats) { s.Deletes += int64(len(keys)) })
	return nil
}

// GetTTL returns the remaining TTL for a key from L2 cache
func (tc *TieredCache) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	// Get TTL from L2 cache (source of truth)
	return tc.l2Cache.GetTTL(ctx, key)
}

// SetTTL updates the TTL for an existing key in both caches
func (tc *TieredCache) SetTTL(ctx context.Context, key string, ttl time.Duration) error {
	var l1Err, l2Err error

	// Update TTL in L2 cache
	l2Err = tc.l2Cache.SetTTL(ctx, key, ttl)

	// Update TTL in L1 cache (use shorter TTL if necessary)
	l1TTL := ttl
	if l1TTL > time.Hour {
		l1TTL = time.Hour
	}
	l1Err = tc.l1Cache.SetTTL(ctx, key, l1TTL)

	// Return error if both failed
	if l1Err != nil && l2Err != nil {
		err := apperror.NewError("failed to set TTL in both L1 and L2 caches")
		tc.recordError(err)
		return err
	}

	return nil
}

// GetStats returns combined statistics from both cache levels
func (tc *TieredCache) GetStats() Stats {
	l1Stats := tc.l1Cache.GetStats()
	l2Stats := tc.l2Cache.GetStats()

	// Combine stats from both levels
	combined := Stats{
		Hits:      l1Stats.Hits + l2Stats.Hits,
		Misses:    l1Stats.Misses + l2Stats.Misses,
		Sets:      l1Stats.Sets + l2Stats.Sets,
		Deletes:   l1Stats.Deletes + l2Stats.Deletes,
		Evictions: l1Stats.Evictions + l2Stats.Evictions,
		Size:      l1Stats.Size + l2Stats.Size,
		Memory:    l1Stats.Memory + l2Stats.Memory,
		Errors:    l1Stats.Errors + l2Stats.Errors,
	}

	// Calculate combined hit ratio
	total := combined.Hits + combined.Misses
	if total > 0 {
		combined.HitRatio = float64(combined.Hits) / float64(total)
	}

	return combined
}

// Close closes both cache implementations
func (tc *TieredCache) Close() error {
	var l1Err, l2Err error

	if tc.l1Cache != nil {
		l1Err = tc.l1Cache.Close()
	}

	if tc.l2Cache != nil {
		l2Err = tc.l2Cache.Close()
	}

	if l1Err != nil {
		return l1Err
	}

	return l2Err
}

// GetL1Cache returns the L1 (fast) cache instance
func (tc *TieredCache) GetL1Cache() Cache {
	return tc.l1Cache
}

// GetL2Cache returns the L2 (persistent) cache instance
func (tc *TieredCache) GetL2Cache() Cache {
	return tc.l2Cache
}

// InvalidateL1 removes a key from L1 cache while keeping it in L2
func (tc *TieredCache) InvalidateL1(ctx context.Context, key string) error {
	return tc.l1Cache.Delete(ctx, key)
}

// WarmupL1 preloads L1 cache with values from L2 cache
func (tc *TieredCache) WarmupL1(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// Get values from L2
	values, err := tc.l2Cache.GetMulti(ctx, keys)
	if err != nil {
		return err
	}

	if len(values) == 0 {
		return nil
	}

	// Set values in L1 with shorter TTL
	l1TTL := tc.config.DefaultTTL
	if l1TTL > time.Hour {
		l1TTL = time.Hour
	}

	return tc.l1Cache.SetMulti(ctx, values, l1TTL)
}

// GetTieredStats returns detailed statistics for each cache level
func (tc *TieredCache) GetTieredStats() map[string]Stats {
	return map[string]Stats{
		"L1": tc.l1Cache.GetStats(),
		"L2": tc.l2Cache.GetStats(),
	}
}
