package cache

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// MemoryCache implements an in-memory cache with LRU eviction support
type MemoryCache struct {
	*BaseCache

	items     map[string]*list.Element
	lruList   *list.List
	mutex     sync.RWMutex
	stopChan  chan struct{}
	cleanupWg sync.WaitGroup
}

// memoryItem represents an item stored in memory cache
type memoryItem struct {
	item     *Item
	dataSize int64
	// accessed is the time of the last read in unix nanoseconds. Readers only set this
	// atomic and hold the shared lock; the LRU list is reordered lazily when an item is
	// about to be evicted (see evictLRU), so reads never need the exclusive lock.
	accessed atomic.Int64
	// promoted is the value of accessed the last time it was applied to the LRU list.
	// Guarded by the exclusive lock.
	promoted int64
}

// NewMemoryCache creates a new in-memory cache with default configuration.
// The default settings include LRU eviction, 1000 item max size, 1 hour default TTL,
// and 5-minute cleanup intervals. For custom settings, use NewMemoryCacheWithConfig.
//
// Example usage:
//
//	cache := cache.NewMemoryCache()
//	err := cache.Set(ctx, "key", "value", time.Hour)
func NewMemoryCache() *MemoryCache {
	return NewMemoryCacheWithConfig(DefaultConfig())
}

// NewMemoryCacheWithConfig creates a new in-memory cache with custom configuration.
// This allows fine-tuning of cache behavior including size limits, TTL settings,
// LRU eviction policies, cleanup intervals, and serialization options.
//
// Example usage:
//
//	config := cache.Config{
//		MaxSize:         5000,
//		DefaultTTL:      time.Minute * 30,
//		CleanupInterval: time.Minute,
//		EnableLRU:       true,
//	}
//	cache := cache.NewMemoryCacheWithConfig(config)
func NewMemoryCacheWithConfig(config Config) *MemoryCache {
	mc := &MemoryCache{
		BaseCache: NewBaseCache(config),
		items:     make(map[string]*list.Element),
		lruList:   list.New(),
		stopChan:  make(chan struct{}),
	}

	// Start cleanup goroutine if cleanup interval is set
	if config.CleanupInterval > 0 {
		mc.startCleanup()
	}

	return mc
}

// WithMaxSize sets the maximum number of items in the cache
func (mc *MemoryCache) WithMaxSize(maxSize int64) *MemoryCache {
	mc.config.MaxSize = maxSize
	mc.stats.MaxSize = maxSize
	return mc
}

// WithDefaultTTL sets the default TTL for cache items
func (mc *MemoryCache) WithDefaultTTL(ttl time.Duration) *MemoryCache {
	mc.config.DefaultTTL = ttl
	return mc
}

// WithLRUEviction enables or disables LRU eviction
func (mc *MemoryCache) WithLRUEviction(enabled bool) *MemoryCache {
	mc.config.EnableLRU = enabled
	return mc
}

// WithCleanupInterval sets the interval for cleaning up expired items
func (mc *MemoryCache) WithCleanupInterval(interval time.Duration) *MemoryCache {
	mc.config.CleanupInterval = interval
	return mc
}

// WithEventHandler sets the event handler for cache events
func (mc *MemoryCache) WithEventHandler(handler EventHandler) *MemoryCache {
	mc.config.EventHandler = handler
	mc.config.EnableEvents = true
	return mc
}

// Get retrieves a value from the cache
func (mc *MemoryCache) Get(_ context.Context, key string, dest interface{}) (bool, error) {
	formattedKey := mc.formatKey(key)

	mc.mutex.RLock()
	element, exists := mc.items[formattedKey]
	if !exists {
		mc.mutex.RUnlock()
		mc.recordMiss()
		mc.emitEvent(EventGet, key, nil, nil)
		return false, nil
	}

	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		mc.mutex.RUnlock()
		mc.recordMiss()
		mc.emitEvent(EventGet, key, nil, nil)
		return false, NewCacheError("get", key, errors.New("invalid cache item type"))
	}
	item := memItem.item
	now := time.Now()

	// Check if item has expired
	if !item.ExpiresAt.IsZero() && now.After(item.ExpiresAt) {
		mc.mutex.RUnlock()
		mc.removeIfExpired(formattedKey)
		mc.recordMiss()
		mc.emitEvent(EventExpire, key, nil, nil)
		return false, nil
	}

	data, ok := item.Value.([]byte)
	mc.mutex.RUnlock()

	// Remember the access for the LRU order
	memItem.accessed.Store(now.UnixNano())

	// Deserialize the value
	if !ok {
		mc.recordMiss()
		return false, NewCacheError("get", key, errors.New("invalid item value type"))
	}
	err := mc.config.Serializer.Deserialize(data, dest)
	if err != nil {
		mc.recordError(err)
		mc.emitEvent(EventGet, key, nil, err)
		return false, NewCacheError("get", key, err)
	}

	mc.recordHit()
	mc.emitEvent(EventGet, key, dest, nil)
	return true, nil
}

// Set stores a value in the cache
func (mc *MemoryCache) Set(_ context.Context, key string, value interface{}, ttl time.Duration) error {
	formattedKey := mc.formatKey(key)
	effectiveTTL := mc.calculateTTL(ttl)

	// Serialize the value
	data, err := mc.config.Serializer.Serialize(value)
	if err != nil {
		mc.recordError(err)
		mc.emitEvent(EventSet, key, value, err)
		return NewCacheError("set", key, err)
	}

	dataSize := int64(len(data))
	now := time.Now()

	item := &Item{
		Key:       formattedKey,
		Value:     data,
		CreatedAt: now,
		UpdatedAt: now,
		AccessAt:  now,
		TTL:       effectiveTTL,
		Size:      dataSize,
		Namespace: mc.config.Namespace,
		ExpiresAt: time.Time{}, // Default to no expiration
	}

	if effectiveTTL > 0 {
		item.ExpiresAt = now.Add(effectiveTTL)
	}

	memItem := &memoryItem{
		item:     item,
		dataSize: dataSize,
	}

	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	// Check if key already exists
	element, exists := mc.items[formattedKey]
	if exists {
		// Update existing item
		oldMemItem, ok := element.Value.(*memoryItem)
		if !ok {
			return NewCacheError("set", key, errors.New("invalid existing item type"))
		}
		element.Value = memItem
		if mc.config.EnableLRU {
			mc.lruList.MoveToFront(element)
		}

		// Update memory usage
		mc.updateStats(func(s *Stats) {
			s.Memory = s.Memory - oldMemItem.dataSize + dataSize
		})

		mc.updateStats(func(s *Stats) { s.Sets++ })
		mc.emitEvent(EventSet, key, value, nil)
		return nil
	}

	// Add new item
	element = mc.lruList.PushFront(memItem)
	mc.items[formattedKey] = element

	mc.updateStats(func(s *Stats) {
		s.Size++
		s.Memory += dataSize
	})

	// Check if we need to evict items
	if mc.config.MaxSize > 0 && mc.stats.Size > mc.config.MaxSize {
		mc.evictLRU()
	}

	mc.updateStats(func(s *Stats) { s.Sets++ })
	mc.emitEvent(EventSet, key, value, nil)
	return nil
}

// Delete removes a value from the cache
func (mc *MemoryCache) Delete(_ context.Context, key string) error {
	formattedKey := mc.formatKey(key)

	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	element, exists := mc.items[formattedKey]
	if !exists {
		return nil // Key doesn't exist, consider it a successful deletion
	}

	mc.removeElement(element, formattedKey)
	mc.updateStats(func(s *Stats) { s.Deletes++ })
	mc.emitEvent(EventDelete, key, nil, nil)
	return nil
}

// removeIfExpired removes the key if it is still expired. The caller must not hold the lock:
// the entry may have been removed or replaced by a fresh value since it was seen.
func (mc *MemoryCache) removeIfExpired(formattedKey string) {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	if element, exists := mc.items[formattedKey]; exists {
		if memItem, ok := element.Value.(*memoryItem); ok && memItem.item.IsExpired() {
			mc.removeElement(element, formattedKey)
		}
	}
}

// Exists checks if a key exists in the cache
func (mc *MemoryCache) Exists(_ context.Context, key string) (bool, error) {
	formattedKey := mc.formatKey(key)

	mc.mutex.RLock()
	element, exists := mc.items[formattedKey]
	if !exists {
		mc.mutex.RUnlock()
		return false, nil
	}

	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		mc.mutex.RUnlock()
		return false, NewCacheError("exists", key, errors.New("invalid item type"))
	}
	item := memItem.item

	// Check if item has expired
	if !item.IsExpired() {
		mc.mutex.RUnlock()
		return true, nil
	}

	mc.mutex.RUnlock()

	// Remove the expired item. The lock was released, so look the key up again: another
	// goroutine may have removed it or stored a fresh value under the same key.
	mc.mutex.Lock()
	defer mc.mutex.Unlock()
	current, exists := mc.items[formattedKey]
	if !exists {
		return false, nil
	}
	currentItem, ok := current.Value.(*memoryItem)
	if !ok {
		return false, NewCacheError("exists", key, errors.New("invalid item type"))
	}
	if !currentItem.item.IsExpired() {
		return true, nil
	}
	mc.removeElement(current, formattedKey)
	return false, nil
}

// Clear removes all entries from the cache
func (mc *MemoryCache) Clear(_ context.Context) error {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	mc.items = make(map[string]*list.Element)
	mc.lruList = list.New()

	mc.updateStats(func(s *Stats) {
		s.Size = 0
		s.Memory = 0
	})

	mc.emitEvent(EventClear, "", nil, nil)
	return nil
}

// GetMulti retrieves multiple values from the cache
func (mc *MemoryCache) GetMulti(ctx context.Context, keys []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for _, key := range keys {
		var value interface{}
		found, err := mc.Get(ctx, key, &value)
		if err != nil {
			return nil, err
		}
		if found {
			result[key] = value
		}
	}

	return result, nil
}

// SetMulti stores multiple values in the cache
func (mc *MemoryCache) SetMulti(ctx context.Context, items map[string]interface{}, ttl time.Duration) error {
	for key, value := range items {
		err := mc.Set(ctx, key, value, ttl)
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteMulti removes multiple values from the cache
func (mc *MemoryCache) DeleteMulti(ctx context.Context, keys []string) error {
	for _, key := range keys {
		err := mc.Delete(ctx, key)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetTTL returns the remaining TTL for a key
func (mc *MemoryCache) GetTTL(_ context.Context, key string) (time.Duration, error) {
	formattedKey := mc.formatKey(key)

	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	element, exists := mc.items[formattedKey]
	if !exists {
		return 0, apperror.NewError("key not found")
	}

	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		return 0, NewCacheError("gettl", key, errors.New("invalid item type"))
	}
	item := memItem.item

	if item.ExpiresAt.IsZero() {
		return 0, nil // No expiration
	}

	if item.IsExpired() {
		return 0, apperror.NewError("key has expired")
	}

	return time.Until(item.ExpiresAt), nil
}

// SetTTL updates the TTL for an existing key
func (mc *MemoryCache) SetTTL(_ context.Context, key string, ttl time.Duration) error {
	formattedKey := mc.formatKey(key)

	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	element, exists := mc.items[formattedKey]
	if !exists {
		return apperror.NewError("key not found")
	}

	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		return NewCacheError("setttl", key, errors.New("invalid item type"))
	}
	item := memItem.item
	item.ExpiresAt = time.Time{}
	item.TTL = 0
	if ttl > 0 {
		item.ExpiresAt = time.Now().Add(ttl)
		item.TTL = ttl
	}

	item.UpdatedAt = time.Now()
	return nil
}

// Close closes the cache and stops the cleanup goroutine
func (mc *MemoryCache) Close() error {
	close(mc.stopChan)
	mc.cleanupWg.Wait()
	return nil
}

// GetKeys returns all keys in the cache (useful for debugging)
func (mc *MemoryCache) GetKeys() []string {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()

	keys := make([]string, 0, len(mc.items))
	for key := range mc.items {
		keys = append(keys, key)
	}
	return keys
}

// GetSize returns the current number of items in the cache
func (mc *MemoryCache) GetSize() int64 {
	mc.mutex.RLock()
	defer mc.mutex.RUnlock()
	return int64(len(mc.items))
}

// GetMemoryUsage returns the current memory usage in bytes
func (mc *MemoryCache) GetMemoryUsage() int64 {
	return mc.stats.Memory
}

// removeElement removes an element from the cache (must be called with lock held)
func (mc *MemoryCache) removeElement(element *list.Element, key string) {
	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		return // Skip if invalid type
	}
	delete(mc.items, key)
	mc.lruList.Remove(element)

	mc.updateStats(func(s *Stats) {
		s.Size--
		s.Memory -= memItem.dataSize
	})
}

// evictLRU evicts the least recently used item (must be called with lock held)
func (mc *MemoryCache) evictLRU() {
	if !mc.config.EnableLRU || mc.lruList.Len() == 0 {
		return
	}

	// Second chance: an item that was read since it was last placed in the list moves to
	// the front instead of being evicted. Each item is moved at most once per call.
	var memItem *memoryItem
	var element *list.Element
	for i, n := 0, mc.lruList.Len(); i < n; i++ {
		element = mc.lruList.Back()
		candidate, ok := element.Value.(*memoryItem)
		if !ok {
			return // Skip if invalid type
		}
		memItem = candidate
		accessed := memItem.accessed.Load()
		if accessed <= memItem.promoted {
			break
		}
		memItem.promoted = accessed
		memItem.item.AccessAt = time.Unix(0, accessed)
		mc.lruList.MoveToFront(element)
		memItem, element = nil, nil
	}
	if element == nil {
		// Every item was read recently: evict the oldest one
		element = mc.lruList.Back()
		candidate, ok := element.Value.(*memoryItem)
		if !ok {
			return
		}
		memItem = candidate
	}

	key := memItem.item.Key
	mc.removeElement(element, key)

	mc.updateStats(func(s *Stats) { s.Evictions++ })
	mc.emitEvent(EventEvict, key, nil, nil)
}

// startCleanup starts the background cleanup goroutine
func (mc *MemoryCache) startCleanup() {
	mc.cleanupWg.Add(1)
	go func() {
		defer mc.cleanupWg.Done()
		ticker := time.NewTicker(mc.config.CleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-mc.stopChan:
				return
			case <-ticker.C:
				mc.cleanupExpired()
			}
		}
	}()
}

// cleanupExpired removes expired items from the cache
func (mc *MemoryCache) cleanupExpired() {
	mc.mutex.Lock()
	defer mc.mutex.Unlock()

	now := time.Now()
	var expiredKeys []string

	// Find expired items
	for key, element := range mc.items {
		memItem, ok := element.Value.(*memoryItem)
		if !ok {
			continue
		}
		item := memItem.item

		if item.ExpiresAt.IsZero() || !now.After(item.ExpiresAt) {
			continue
		}

		expiredKeys = append(expiredKeys, key)
	}

	// Remove expired items
	for _, key := range expiredKeys {
		element, exists := mc.items[key]
		if !exists {
			continue
		}

		mc.removeElement(element, key)
		mc.emitEvent(EventExpire, key, nil, nil)
	}
}
