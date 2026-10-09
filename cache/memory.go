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

	// shards split the keys, each with its own lock and LRU list. With Config.Shards of 0 or 1
	// there is one shard and the cache behaves as one exact LRU.
	shards    []*memShard
	native    InMemorySerializer // set when the serializer keeps values without encoding them
	stopChan  chan struct{}
	cleanupWg sync.WaitGroup
}

// memShard is one slice of the key space
type memShard struct {
	items   map[string]*list.Element
	lruList *list.List
	mutex   sync.RWMutex
}

func newMemShard() *memShard {
	return &memShard{items: make(map[string]*list.Element), lruList: list.New()}
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
// Config.Shards splits the cache into that many independent parts, each with its own lock, so
// concurrent writers do not wait for each other. MaxSize is then divided between the shards, and
// eviction picks the least recently used item of the shard that is full, not of the whole cache.
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
	shards := max(config.Shards, 1)
	mc := &MemoryCache{
		BaseCache: NewBaseCache(config),
		shards:    make([]*memShard, shards),
		stopChan:  make(chan struct{}),
	}
	for i := range mc.shards {
		mc.shards[i] = newMemShard()
	}
	mc.native, _ = mc.config.Serializer.(InMemorySerializer)

	// Start cleanup goroutine if cleanup interval is set
	if config.CleanupInterval > 0 {
		mc.startCleanup()
	}

	return mc
}

// shardFor returns the shard that holds the key
func (mc *MemoryCache) shardFor(formattedKey string) *memShard {
	if len(mc.shards) == 1 {
		return mc.shards[0]
	}
	// FNV-1a
	h := uint32(2166136261)
	for i := 0; i < len(formattedKey); i++ {
		h ^= uint32(formattedKey[i])
		h *= 16777619
	}
	return mc.shards[h%uint32(len(mc.shards))]
}

// shardLimit is the number of items one shard may hold, or 0 for no limit
func (mc *MemoryCache) shardLimit() int64 {
	if mc.config.MaxSize <= 0 {
		return 0
	}
	n := int64(len(mc.shards))
	return (mc.config.MaxSize + n - 1) / n
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
	sh := mc.shardFor(formattedKey)

	sh.mutex.RLock()
	element, exists := sh.items[formattedKey]
	if !exists {
		sh.mutex.RUnlock()
		mc.recordMiss()
		mc.emitEvent(EventGet, key, nil, nil)
		return false, nil
	}

	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		sh.mutex.RUnlock()
		mc.recordMiss()
		mc.emitEvent(EventGet, key, nil, nil)
		return false, NewCacheError("get", key, errors.New("invalid cache item type"))
	}
	item := memItem.item
	now := time.Now()

	// Check if item has expired
	if !item.ExpiresAt.IsZero() && now.After(item.ExpiresAt) {
		sh.mutex.RUnlock()
		mc.removeIfExpired(sh, formattedKey)
		mc.recordMiss()
		mc.emitEvent(EventExpire, key, nil, nil)
		return false, nil
	}

	stored := item.Value
	sh.mutex.RUnlock()

	// Remember the access for the LRU order
	memItem.accessed.Store(now.UnixNano())

	var err error
	if mc.native != nil {
		err = mc.native.Restore(stored, dest)
	} else {
		data, isBytes := stored.([]byte)
		if !isBytes {
			mc.recordMiss()
			return false, NewCacheError("get", key, errors.New("invalid item value type"))
		}
		err = mc.config.Serializer.Deserialize(data, dest)
	}
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
	var stored interface{}
	var dataSize int64
	var err error
	if mc.native != nil {
		stored, err = mc.native.Keep(value)
	} else {
		var data []byte
		data, err = mc.config.Serializer.Serialize(value)
		stored, dataSize = data, int64(len(data))
	}
	if err != nil {
		mc.recordError(err)
		mc.emitEvent(EventSet, key, value, err)
		return NewCacheError("set", key, err)
	}

	now := time.Now()

	item := &Item{
		Key:       formattedKey,
		Value:     stored,
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

	sh := mc.shardFor(formattedKey)
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	// Check if key already exists
	element, exists := sh.items[formattedKey]
	if exists {
		// Update existing item
		oldMemItem, ok := element.Value.(*memoryItem)
		if !ok {
			return NewCacheError("set", key, errors.New("invalid existing item type"))
		}
		element.Value = memItem
		if mc.config.EnableLRU {
			sh.lruList.MoveToFront(element)
		}

		// Update memory usage and count the set
		mc.updateStats(func(s *Stats) {
			s.Memory = s.Memory - oldMemItem.dataSize + dataSize
			s.Sets++
		})

		mc.emitEvent(EventSet, key, value, nil)
		return nil
	}

	// Add new item
	element = sh.lruList.PushFront(memItem)
	sh.items[formattedKey] = element

	mc.updateStats(func(s *Stats) {
		s.Size++
		s.Memory += dataSize
		s.Sets++
	})

	// Check if we need to evict items
	if limit := mc.shardLimit(); limit > 0 && int64(len(sh.items)) > limit {
		mc.evictLRU(sh)
	}

	mc.emitEvent(EventSet, key, value, nil)
	return nil
}

// Delete removes a value from the cache
func (mc *MemoryCache) Delete(_ context.Context, key string) error {
	formattedKey := mc.formatKey(key)
	sh := mc.shardFor(formattedKey)

	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	element, exists := sh.items[formattedKey]
	if !exists {
		return nil // Key doesn't exist, consider it a successful deletion
	}

	mc.removeElement(sh, element, formattedKey)
	mc.updateStats(func(s *Stats) { s.Deletes++ })
	mc.emitEvent(EventDelete, key, nil, nil)
	return nil
}

// removeIfExpired removes the key if it is still expired. The caller must not hold the lock:
// the entry may have been removed or replaced by a fresh value since it was seen.
func (mc *MemoryCache) removeIfExpired(sh *memShard, formattedKey string) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	if element, exists := sh.items[formattedKey]; exists {
		if memItem, ok := element.Value.(*memoryItem); ok && memItem.item.IsExpired() {
			mc.removeElement(sh, element, formattedKey)
		}
	}
}

// Exists checks if a key exists in the cache
func (mc *MemoryCache) Exists(_ context.Context, key string) (bool, error) {
	formattedKey := mc.formatKey(key)
	sh := mc.shardFor(formattedKey)

	sh.mutex.RLock()
	element, exists := sh.items[formattedKey]
	if !exists {
		sh.mutex.RUnlock()
		return false, nil
	}

	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		sh.mutex.RUnlock()
		return false, NewCacheError("exists", key, errors.New("invalid item type"))
	}
	item := memItem.item

	// Check if item has expired
	if !item.IsExpired() {
		sh.mutex.RUnlock()
		return true, nil
	}

	sh.mutex.RUnlock()

	// Remove the expired item. The lock was released, so look the key up again: another
	// goroutine may have removed it or stored a fresh value under the same key.
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	current, exists := sh.items[formattedKey]
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
	mc.removeElement(sh, current, formattedKey)
	return false, nil
}

// Clear removes all entries from the cache
func (mc *MemoryCache) Clear(_ context.Context) error {
	for _, sh := range mc.shards {
		sh.mutex.Lock()
		sh.items = make(map[string]*list.Element)
		sh.lruList = list.New()
		sh.mutex.Unlock()
	}

	mc.updateStats(func(s *Stats) {
		s.Size = 0
		s.Memory = 0
	})

	mc.emitEvent(EventClear, "", nil, nil)
	return nil
}

// GetMulti retrieves multiple values from the cache
func (mc *MemoryCache) GetMulti(ctx context.Context, keys []string) (map[string]interface{}, error) {
	result := make(map[string]interface{}, len(keys))

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
	sh := mc.shardFor(formattedKey)

	sh.mutex.RLock()
	defer sh.mutex.RUnlock()

	element, exists := sh.items[formattedKey]
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
	sh := mc.shardFor(formattedKey)

	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	element, exists := sh.items[formattedKey]
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
	keys := make([]string, 0, mc.GetSize())
	for _, sh := range mc.shards {
		sh.mutex.RLock()
		for key := range sh.items {
			keys = append(keys, key)
		}
		sh.mutex.RUnlock()
	}
	return keys
}

// GetSize returns the current number of items in the cache
func (mc *MemoryCache) GetSize() int64 {
	var size int64
	for _, sh := range mc.shards {
		sh.mutex.RLock()
		size += int64(len(sh.items))
		sh.mutex.RUnlock()
	}
	return size
}

// GetMemoryUsage returns the current memory usage in bytes
func (mc *MemoryCache) GetMemoryUsage() int64 {
	mc.BaseCache.mutex.RLock()
	defer mc.BaseCache.mutex.RUnlock()
	return mc.stats.Memory
}

// removeElement removes an element from the shard (must be called with the shard lock held)
func (mc *MemoryCache) removeElement(sh *memShard, element *list.Element, key string) {
	memItem, ok := element.Value.(*memoryItem)
	if !ok {
		return // Skip if invalid type
	}
	delete(sh.items, key)
	sh.lruList.Remove(element)

	mc.updateStats(func(s *Stats) {
		s.Size--
		s.Memory -= memItem.dataSize
	})
}

// evictLRU evicts the least recently used item of the shard (must be called with the shard lock held)
func (mc *MemoryCache) evictLRU(sh *memShard) {
	if !mc.config.EnableLRU || sh.lruList.Len() == 0 {
		return
	}

	// Second chance: an item that was read since it was last placed in the list moves to
	// the front instead of being evicted. Each item is moved at most once per call.
	var memItem *memoryItem
	var element *list.Element
	for i, n := 0, sh.lruList.Len(); i < n; i++ {
		element = sh.lruList.Back()
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
		sh.lruList.MoveToFront(element)
		memItem, element = nil, nil
	}
	if element == nil {
		// Every item was read recently: evict the oldest one
		element = sh.lruList.Back()
		candidate, ok := element.Value.(*memoryItem)
		if !ok {
			return
		}
		memItem = candidate
	}

	key := memItem.item.Key
	mc.removeElement(sh, element, key)

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

// cleanupExpired removes expired items from the cache, one shard at a time
func (mc *MemoryCache) cleanupExpired() {
	for _, sh := range mc.shards {
		mc.cleanupShard(sh)
	}
}

func (mc *MemoryCache) cleanupShard(sh *memShard) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	now := time.Now()
	var expiredKeys []string

	// Find expired items
	for key, element := range sh.items {
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
		element, exists := sh.items[key]
		if !exists {
			continue
		}

		mc.removeElement(sh, element, key)
		mc.emitEvent(EventExpire, key, nil, nil)
	}
}
