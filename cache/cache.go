// Package cache provides a comprehensive caching solution with multiple backends
// and advanced features like TTL, LRU eviction, serialization, and distributed caching.
//
// It supports in-memory caching with LRU eviction, Redis-backed distributed caching,
// and provides features like:
//   - TTL (Time To Live) support
//   - LRU (Least Recently Used) eviction
//   - Automatic serialization/deserialization
//   - Cache statistics and monitoring
//   - Namespace support for multi-tenant applications
//   - Bulk operations (GetMulti, SetMulti, DeleteMulti)
//   - Cache warming and preloading
//   - Event callbacks (OnSet, OnGet, OnDelete, OnEvict)
//   - Compression support for large values
//   - Circuit breaker pattern for external cache failures
//
// Example usage:
//
//	package main
//
//	import (
//		"context"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/cache"
//	)
//
//	func main() {
//		// Create an in-memory cache with LRU eviction
//		memCache := cache.NewMemoryCache().
//			WithMaxSize(1000).
//			WithDefaultTTL(time.Hour).
//			WithLRUEviction(true)
//
//		// Create a Redis-backed cache
//		redisCache := cache.NewRedisCache(cache.RedisConfig{
//			Addr:     "localhost:6379",
//			Password: "",
//			DB:       0,
//		})
//
//		// Create a multi-tier cache (L1: memory, L2: Redis)
//		tieredCache := cache.NewTieredCache(memCache, redisCache)
//
//		// Use the cache
//		ctx := context.Background()
//
//		// Set a value with TTL
//		err := tieredCache.Set(ctx, "user:123", user, time.Minute*30)
//		if err != nil {
//			panic(err)
//		}
//
//		// Get a value
//		var cachedUser User
//		found, err := tieredCache.Get(ctx, "user:123", &cachedUser)
//		if err != nil {
//			panic(err)
//		}
//
//		if found {
//			fmt.Printf("Found user: %+v\n", cachedUser)
//		}
//	}
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/config"
)

// Cache defines the interface for cache implementations
type Cache interface {
	// Get retrieves a value from the cache and deserializes it into the provided destination
	Get(ctx context.Context, key string, dest interface{}) (bool, error)

	// Set stores a value in the cache with the specified TTL
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error

	// Delete removes a value from the cache
	Delete(ctx context.Context, key string) error

	// Exists checks if a key exists in the cache
	Exists(ctx context.Context, key string) (bool, error)

	// Clear removes all entries from the cache
	Clear(ctx context.Context) error

	// GetMulti retrieves multiple values from the cache
	GetMulti(ctx context.Context, keys []string) (map[string]interface{}, error)

	// SetMulti stores multiple values in the cache
	SetMulti(ctx context.Context, items map[string]interface{}, ttl time.Duration) error

	// DeleteMulti removes multiple values from the cache
	DeleteMulti(ctx context.Context, keys []string) error

	// GetTTL returns the remaining TTL for a key
	GetTTL(ctx context.Context, key string) (time.Duration, error)

	// SetTTL updates the TTL for an existing key
	SetTTL(ctx context.Context, key string, ttl time.Duration) error

	// GetStats returns cache statistics
	GetStats() Stats

	// Close closes the cache and releases resources
	Close() error
}

// Stats represents cache statistics
type Stats struct {
	Hits        int64     `json:"hits"`
	Misses      int64     `json:"misses"`
	Sets        int64     `json:"sets"`
	Deletes     int64     `json:"deletes"`
	Evictions   int64     `json:"evictions"`
	Size        int64     `json:"size"`
	MaxSize     int64     `json:"max_size"`
	HitRatio    float64   `json:"hit_ratio"`
	Memory      int64     `json:"memory_bytes"`
	Errors      int64     `json:"errors"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// Item represents a cache item with metadata
type Item struct {
	Key       string        `json:"key"`
	Value     interface{}   `json:"value"`
	ExpiresAt time.Time     `json:"expires_at"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	AccessAt  time.Time     `json:"access_at"`
	TTL       time.Duration `json:"ttl"`
	Size      int64         `json:"size"`
	Namespace string        `json:"namespace,omitempty"`
}

// IsExpired checks if the item has expired
func (i *Item) IsExpired() bool {
	return !i.ExpiresAt.IsZero() && time.Now().After(i.ExpiresAt)
}

// EventType represents cache event types
type EventType int

const (
	// EventSet represents a cache set event
	EventSet EventType = iota
	// EventGet represents a cache get event
	EventGet
	// EventDelete represents a cache delete event
	EventDelete
	// EventEvict represents a cache eviction event
	EventEvict
	// EventExpire represents a cache expiration event
	EventExpire
	// EventClear represents a cache clear event
	EventClear
)

// String returns the string representation of the event type
func (e EventType) String() string {
	switch e {
	case EventSet:
		return "set"
	case EventGet:
		return "get"
	case EventDelete:
		return "delete"
	case EventEvict:
		return "evict"
	case EventExpire:
		return "expire"
	case EventClear:
		return "clear"
	default:
		return "unknown"
	}
}

// Event represents a cache event
type Event struct {
	Type      EventType   `json:"type"`
	Key       string      `json:"key"`
	Value     interface{} `json:"value,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
	Namespace string      `json:"namespace,omitempty"`
	Error     error       `json:"error,omitempty"`
}

// EventHandler is a function that handles cache events
type EventHandler func(event Event)

// Config holds common configuration for cache implementations
type Config struct {
	MaxSize         int64         `json:"max_size"`
	DefaultTTL      time.Duration `json:"default_ttl"`
	CleanupInterval time.Duration `json:"cleanup_interval"`
	EnableLRU       bool          `json:"enable_lru"`
	EnableStats     bool          `json:"enable_stats"`
	EnableEvents    bool          `json:"enable_events"`
	Namespace       string        `json:"namespace"`
	// Shards splits a MemoryCache into this many parts with their own locks. 0 or 1 keeps one
	// part, which is an exact LRU; more parts let concurrent writers run in parallel but make
	// eviction approximate: MaxSize is divided exactly between the parts (so the cache never holds
	// more than MaxSize items) and a full part evicts its own least recently used item. With a
	// MaxSize below Shards, some parts hold nothing.
	Shards int `json:"shards,omitempty"`
	Serializer      Serializer    `json:"-"`
	EventHandler    EventHandler  `json:"-"`
}

// Changed checks if the cache configuration has changed compared to another configuration.
func (c *Config) Changed(n *Config) bool {
	return config.Changed(c, n)
}

// Serializer defines the interface for value serialization
type Serializer interface {
	Serialize(value interface{}) ([]byte, error)
	Deserialize(data []byte, dest interface{}) error
}

// JSONSerializer implements JSON serialization
type JSONSerializer struct{}

// Serialize serializes a value to JSON
func (s *JSONSerializer) Serialize(value interface{}) ([]byte, error) {
	return json.Marshal(value)
}

// Deserialize deserializes JSON data into the destination
func (s *JSONSerializer) Deserialize(data []byte, dest interface{}) error {
	return json.Unmarshal(data, dest)
}

// InMemorySerializer is implemented by serializers that let an in-process cache keep values as
// they are, without encoding them. MemoryCache uses it when its serializer implements it.
type InMemorySerializer interface {
	Serializer
	// Keep returns what the cache stores for the value
	Keep(value interface{}) (interface{}, error)
	// Restore copies a stored value into dest, which is a pointer
	Restore(kept interface{}, dest interface{}) error
}

// NativeSerializer keeps values in memory as Go values instead of JSON, which makes reads of a
// MemoryCache several times faster. Serialize and Deserialize are still JSON, so the same
// serializer works for Redis or the second level of a TieredCache.
//
// Values are not copied deeply: a struct is copied by value, but maps, slices and pointers inside
// it are shared with the caller and with every reader. Treat cached values as read-only, or
// use the JSON serializer, whose reads each get their own copy. The size of an item is not
// tracked, so GetMemoryUsage reports 0 for a cache that uses this serializer.
type NativeSerializer struct{ JSONSerializer }

// Keep stores the value; a pointer is stored as the value it points to
func (s *NativeSerializer) Keep(value interface{}) (interface{}, error) {
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, nil
		}
		return rv.Elem().Interface(), nil
	}
	return value, nil
}

// Restore assigns the stored value to dest. When the types do not match it falls back to a JSON round trip.
func (s *NativeSerializer) Restore(kept interface{}, dest interface{}) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return errors.New("destination must be a non-nil pointer")
	}
	dv = dv.Elem()
	if kept == nil {
		dv.Set(reflect.Zero(dv.Type()))
		return nil
	}
	if kv := reflect.ValueOf(kept); kv.Type().AssignableTo(dv.Type()) {
		dv.Set(kv)
		return nil
	}

	data, err := s.Serialize(kept)
	if err != nil {
		return err
	}
	return s.Deserialize(data, dest)
}

// NoOpSerializer implements no serialization (for already serialized data)
type NoOpSerializer struct{}

// Serialize returns the value as-is if it's []byte, otherwise returns an error
func (s *NoOpSerializer) Serialize(value interface{}) ([]byte, error) {
	if data, ok := value.([]byte); ok {
		return data, nil
	}
	if str, ok := value.(string); ok {
		return []byte(str), nil
	}
	return nil, apperror.NewError("NoOpSerializer can only handle []byte or string values")
}

// Deserialize copies the data to the destination if it's *[]byte or *string
func (s *NoOpSerializer) Deserialize(data []byte, dest interface{}) error {
	switch v := dest.(type) {
	case *[]byte:
		*v = make([]byte, len(data))
		copy(*v, data)
		return nil
	case *string:
		*v = string(data)
		return nil
	default:
		return apperror.NewError("NoOpSerializer can only deserialize to *[]byte or *string")
	}
}

// DefaultConfig returns a default cache configuration
func DefaultConfig() Config {
	return Config{
		MaxSize:         1000,
		DefaultTTL:      time.Hour,
		CleanupInterval: time.Minute * 5,
		EnableLRU:       true,
		EnableStats:     true,
		EnableEvents:    false,
		Serializer:      &JSONSerializer{},
	}
}

// BaseCache provides common functionality for cache implementations
type BaseCache struct {
	config Config
	stats  Stats
	mutex  sync.RWMutex
	// hits and misses are counted on every read, so they are atomic instead of
	// going through the stats mutex. GetStats merges them into the snapshot.
	hits   atomic.Int64
	misses atomic.Int64
}

// NewBaseCache creates a new base cache with the given configuration
func NewBaseCache(config Config) *BaseCache {
	if config.Serializer == nil {
		config.Serializer = &JSONSerializer{}
	}

	return &BaseCache{
		config: config,
		stats:  Stats{MaxSize: config.MaxSize},
	}
}

// GetConfig returns the cache configuration
func (bc *BaseCache) GetConfig() Config {
	return bc.config
}

// GetStats returns a copy of the current cache statistics
func (bc *BaseCache) GetStats() Stats {
	bc.mutex.RLock()
	stats := bc.stats
	bc.mutex.RUnlock()

	stats.Hits += bc.hits.Load()
	stats.Misses += bc.misses.Load()
	if total := stats.Hits + stats.Misses; total > 0 {
		stats.HitRatio = float64(stats.Hits) / float64(total)
	}
	return stats
}

// recordHit counts a cache hit
func (bc *BaseCache) recordHit() {
	if bc.config.EnableStats {
		bc.hits.Add(1)
	}
}

// recordMiss counts a cache miss
func (bc *BaseCache) recordMiss() {
	if bc.config.EnableStats {
		bc.misses.Add(1)
	}
}

// updateStats updates cache statistics safely
func (bc *BaseCache) updateStats(fn func(*Stats)) {
	if !bc.config.EnableStats {
		return
	}

	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	fn(&bc.stats)

	// Calculate hit ratio
	total := bc.stats.Hits + bc.stats.Misses
	if total > 0 {
		bc.stats.HitRatio = float64(bc.stats.Hits) / float64(total)
	}
}

// emitEvent emits a cache event if events are enabled
func (bc *BaseCache) emitEvent(eventType EventType, key string, value interface{}, err error) {
	if !bc.config.EnableEvents || bc.config.EventHandler == nil {
		return
	}

	event := Event{
		Type:      eventType,
		Key:       key,
		Value:     value,
		Timestamp: time.Now(),
		Namespace: bc.config.Namespace,
		Error:     err,
	}

	// Run event handler in a goroutine to avoid blocking
	go bc.config.EventHandler(event)
}

// recordError records an error in the statistics
func (bc *BaseCache) recordError(err error) {
	bc.updateStats(func(s *Stats) {
		s.Errors++
		s.LastError = err.Error()
		s.LastErrorAt = time.Now()
	})
}

// formatKey formats a cache key with namespace if configured
func (bc *BaseCache) formatKey(key string) string {
	if bc.config.Namespace == "" {
		return key
	}
	return bc.config.Namespace + ":" + key
}

// calculateTTL calculates the effective TTL for a cache entry
func (bc *BaseCache) calculateTTL(ttl time.Duration) time.Duration {
	if ttl == 0 {
		return bc.config.DefaultTTL
	}
	return ttl
}

// Error represents a cache-specific error
type Error struct {
	Op  string
	Key string
	Err error
}

// NewCacheError creates a new cache error
func NewCacheError(op, key string, err error) *Error {
	return &Error{
		Op:  op,
		Key: key,
		Err: err,
	}
}

// Error implements the error interface
func (e *Error) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("cache %s failed for key '%s': %v", e.Op, e.Key, e.Err)
	}
	return fmt.Sprintf("cache %s failed: %v", e.Op, e.Err)
}

// Unwrap returns the underlying error
func (e *Error) Unwrap() error {
	return e.Err
}
