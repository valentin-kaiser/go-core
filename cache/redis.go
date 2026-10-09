package cache

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/config"
)

// RedisCache implements a Redis-backed cache
type RedisCache struct {
	*BaseCache

	client *redis.Client
}

// RedisConfig holds configuration for Redis cache
type RedisConfig struct {
	Addr         string        `json:"addr"`
	Password     string        `json:"password"`
	DB           int           `json:"db"`
	PoolSize     int           `json:"pool_size"`
	MinIdleConns int           `json:"min_idle_conns"`
	MaxRetries   int           `json:"max_retries"`
	DialTimeout  time.Duration `json:"dial_timeout"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`
}

// DefaultRedisConfig returns a default Redis configuration
func DefaultRedisConfig() RedisConfig {
	return RedisConfig{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 1,
		MaxRetries:   3,
		DialTimeout:  time.Second * 5,
		ReadTimeout:  time.Second * 3,
		WriteTimeout: time.Second * 3,
		IdleTimeout:  time.Minute * 5,
	}
}

// Changed checks if the Redis configuration has changed compared to another configuration.
func (rc *RedisConfig) Changed(n *RedisConfig) bool {
	return config.Changed(rc, n)
}

// NewRedisCache creates a new Redis-backed cache with default cache configuration.
// It establishes a new Redis client connection using the provided Redis configuration
// and applies default cache settings (LRU eviction, 1 hour TTL, etc.).
//
// Example usage:
//
//	config := cache.RedisConfig{
//		Addr:     "localhost:6379",
//		Password: "",
//		DB:       0,
//	}
//	cache := cache.NewRedisCache(config)
func NewRedisCache(config RedisConfig) *RedisCache {
	return NewRedisCacheWithCacheConfig(config, DefaultConfig())
}

// NewRedisCacheWithClient creates a new Redis-backed cache using an existing Redis client.
// This is useful when you want to share a Redis client across multiple cache instances
// or when you need custom Redis client configuration that's not supported by RedisConfig.
func NewRedisCacheWithClient(client *redis.Client) *RedisCache {
	return &RedisCache{
		BaseCache: NewBaseCache(DefaultConfig()),
		client:    client,
	}
}

// NewRedisCacheWithConfig creates a new Redis-backed cache with an existing Redis client
// and custom cache configuration. This allows fine-tuning of cache behavior while reusing
// an existing Redis connection.
func NewRedisCacheWithConfig(client *redis.Client, config Config) *RedisCache {
	return &RedisCache{
		BaseCache: NewBaseCache(config),
		client:    client,
	}
}

// NewRedisCacheWithCacheConfig creates a new Redis-backed cache with both custom Redis
// connection settings and cache configuration. This provides the most flexibility for
// configuring both the Redis client and cache behavior.
func NewRedisCacheWithCacheConfig(redisConfig RedisConfig, cacheConfig Config) *RedisCache {
	client := redis.NewClient(&redis.Options{
		Addr:         redisConfig.Addr,
		Password:     redisConfig.Password,
		DB:           redisConfig.DB,
		PoolSize:     redisConfig.PoolSize,
		MinIdleConns: redisConfig.MinIdleConns,
		MaxRetries:   redisConfig.MaxRetries,
		DialTimeout:  redisConfig.DialTimeout,
		ReadTimeout:  redisConfig.ReadTimeout,
		WriteTimeout: redisConfig.WriteTimeout,
	})

	return &RedisCache{
		BaseCache: NewBaseCache(cacheConfig),
		client:    client,
	}
}

// WithNamespace sets the namespace for cache keys
func (rc *RedisCache) WithNamespace(namespace string) *RedisCache {
	rc.config.Namespace = namespace
	return rc
}

// WithDefaultTTL sets the default TTL for cache items
func (rc *RedisCache) WithDefaultTTL(ttl time.Duration) *RedisCache {
	rc.config.DefaultTTL = ttl
	return rc
}

// WithEventHandler sets the event handler for cache events
func (rc *RedisCache) WithEventHandler(handler EventHandler) *RedisCache {
	rc.config.EventHandler = handler
	rc.config.EnableEvents = true
	return rc
}

// Ping tests the Redis connection
func (rc *RedisCache) Ping(ctx context.Context) error {
	return rc.client.Ping(ctx).Err()
}

// Get retrieves a value from the cache
func (rc *RedisCache) Get(ctx context.Context, key string, dest interface{}) (bool, error) {
	formattedKey := rc.formatKey(key)

	data, err := rc.client.Get(ctx, formattedKey).Result()
	if err != nil {
		if err == redis.Nil {
			rc.recordMiss()
			rc.emitEvent(EventGet, key, nil, nil)
			return false, nil
		}
		rc.recordError(err)
		rc.emitEvent(EventGet, key, nil, err)
		return false, NewCacheError("get", key, err)
	}

	// Deserialize the value
	err = rc.config.Serializer.Deserialize([]byte(data), dest)
	if err != nil {
		rc.recordError(err)
		rc.emitEvent(EventGet, key, nil, err)
		return false, NewCacheError("get", key, err)
	}

	rc.recordHit()
	rc.emitEvent(EventGet, key, dest, nil)
	return true, nil
}

// Set stores a value in the cache
func (rc *RedisCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	formattedKey := rc.formatKey(key)
	effectiveTTL := rc.calculateTTL(ttl)

	// Serialize the value
	data, err := rc.config.Serializer.Serialize(value)
	if err != nil {
		rc.recordError(err)
		rc.emitEvent(EventSet, key, value, err)
		return NewCacheError("set", key, err)
	}

	err = rc.client.Set(ctx, formattedKey, data, effectiveTTL).Err()
	if err != nil {
		rc.recordError(err)
		rc.emitEvent(EventSet, key, value, err)
		return NewCacheError("set", key, err)
	}

	rc.updateStats(func(s *Stats) { s.Sets++ })
	rc.emitEvent(EventSet, key, value, nil)
	return nil
}

// Delete removes a value from the cache
func (rc *RedisCache) Delete(ctx context.Context, key string) error {
	formattedKey := rc.formatKey(key)

	err := rc.client.Del(ctx, formattedKey).Err()
	if err != nil {
		rc.recordError(err)
		rc.emitEvent(EventDelete, key, nil, err)
		return NewCacheError("delete", key, err)
	}

	rc.updateStats(func(s *Stats) { s.Deletes++ })
	rc.emitEvent(EventDelete, key, nil, nil)
	return nil
}

// Exists checks if a key exists in the cache
func (rc *RedisCache) Exists(ctx context.Context, key string) (bool, error) {
	formattedKey := rc.formatKey(key)

	count, err := rc.client.Exists(ctx, formattedKey).Result()
	if err != nil {
		rc.recordError(err)
		return false, NewCacheError("exists", key, err)
	}

	return count > 0, nil
}

// Clear removes all entries from the cache (only works with namespace)
func (rc *RedisCache) Clear(ctx context.Context) error {
	if rc.config.Namespace == "" {
		return apperror.NewError("clear operation requires a namespace to avoid deleting all Redis keys")
	}

	pattern := rc.config.Namespace + ":*"
	keys, err := rc.client.Keys(ctx, pattern).Result()
	if err != nil {
		rc.recordError(err)
		rc.emitEvent(EventClear, "", nil, err)
		return NewCacheError("clear", "", err)
	}

	if len(keys) > 0 {
		err = rc.client.Del(ctx, keys...).Err()
		if err != nil {
			rc.recordError(err)
			rc.emitEvent(EventClear, "", nil, err)
			return NewCacheError("clear", "", err)
		}
	}

	rc.emitEvent(EventClear, "", nil, nil)
	return nil
}

// GetMulti retrieves multiple values from the cache
func (rc *RedisCache) GetMulti(ctx context.Context, keys []string) (map[string]interface{}, error) {
	if len(keys) == 0 {
		return make(map[string]interface{}), nil
	}

	formattedKeys := make([]string, len(keys))
	for i, key := range keys {
		formattedKeys[i] = rc.formatKey(key)
	}

	values, err := rc.client.MGet(ctx, formattedKeys...).Result()
	if err != nil {
		rc.recordError(err)
		return nil, NewCacheError("getmulti", "", err)
	}

	result := make(map[string]interface{}, len(values))
	for i, value := range values {
		if value != nil {
			var dest interface{}
			if data, ok := value.(string); ok {
				err := rc.config.Serializer.Deserialize([]byte(data), &dest)
				if err != nil {
					rc.recordError(err)
					continue
				}
				result[keys[i]] = dest
				rc.recordHit()
				continue
			}

			rc.recordMiss()
		}
	}

	return result, nil
}

// SetMulti stores multiple values in the cache
func (rc *RedisCache) SetMulti(ctx context.Context, items map[string]interface{}, ttl time.Duration) error {
	effectiveTTL := rc.calculateTTL(ttl)

	pipe := rc.client.Pipeline()
	for key, value := range items {
		formattedKey := rc.formatKey(key)

		data, err := rc.config.Serializer.Serialize(value)
		if err != nil {
			rc.recordError(err)
			continue
		}

		pipe.Set(ctx, formattedKey, data, effectiveTTL)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		rc.recordError(err)
		return NewCacheError("setmulti", "", err)
	}

	rc.updateStats(func(s *Stats) { s.Sets += int64(len(items)) })
	return nil
}

// DeleteMulti removes multiple values from the cache
func (rc *RedisCache) DeleteMulti(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	formattedKeys := make([]string, len(keys))
	for i, key := range keys {
		formattedKeys[i] = rc.formatKey(key)
	}

	err := rc.client.Del(ctx, formattedKeys...).Err()
	if err != nil {
		rc.recordError(err)
		return NewCacheError("deletemulti", "", err)
	}

	rc.updateStats(func(s *Stats) { s.Deletes += int64(len(keys)) })
	return nil
}

// GetTTL returns the remaining TTL for a key
func (rc *RedisCache) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	formattedKey := rc.formatKey(key)

	ttl, err := rc.client.TTL(ctx, formattedKey).Result()
	if err != nil {
		rc.recordError(err)
		return 0, NewCacheError("getttl", key, err)
	}

	if ttl == -2 {
		return 0, apperror.NewError("key not found")
	}

	if ttl == -1 {
		return 0, nil // No expiration
	}

	return ttl, nil
}

// SetTTL updates the TTL for an existing key
func (rc *RedisCache) SetTTL(ctx context.Context, key string, ttl time.Duration) error {
	formattedKey := rc.formatKey(key)

	var err error
	if ttl > 0 {
		err = rc.client.Expire(ctx, formattedKey, ttl).Err()
	}
	if ttl <= 0 {
		err = rc.client.Persist(ctx, formattedKey).Err()
	}

	if err != nil {
		rc.recordError(err)
		return NewCacheError("setttl", key, err)
	}

	return nil
}

// Close closes the Redis client
func (rc *RedisCache) Close() error {
	return rc.client.Close()
}

// GetClient returns the underlying Redis client for advanced operations
func (rc *RedisCache) GetClient() *redis.Client {
	return rc.client
}

// GetInfo returns Redis server information
func (rc *RedisCache) GetInfo(ctx context.Context) (map[string]string, error) {
	info, err := rc.client.Info(ctx).Result()
	if err != nil {
		return nil, NewCacheError("info", "", err)
	}

	result := make(map[string]string)
	lines := strings.Split(info, "\r\n")
	for _, line := range lines {
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				result[parts[0]] = parts[1]
			}
		}
	}

	return result, nil
}

// FlushDB flushes the current Redis database
func (rc *RedisCache) FlushDB(ctx context.Context) error {
	err := rc.client.FlushDB(ctx).Err()
	if err != nil {
		rc.recordError(err)
		return NewCacheError("flushdb", "", err)
	}

	rc.emitEvent(EventClear, "", nil, nil)
	return nil
}

// Increment atomically increments a numeric value
func (rc *RedisCache) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	formattedKey := rc.formatKey(key)

	result, err := rc.client.IncrBy(ctx, formattedKey, delta).Result()
	if err != nil {
		rc.recordError(err)
		return 0, NewCacheError("increment", key, err)
	}

	return result, nil
}

// Decrement atomically decrements a numeric value
func (rc *RedisCache) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	formattedKey := rc.formatKey(key)

	result, err := rc.client.DecrBy(ctx, formattedKey, delta).Result()
	if err != nil {
		rc.recordError(err)
		return 0, NewCacheError("decrement", key, err)
	}

	return result, nil
}

// SetNX sets a key only if it doesn't exist (atomic operation)
func (rc *RedisCache) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	formattedKey := rc.formatKey(key)
	effectiveTTL := rc.calculateTTL(ttl)

	data, err := rc.config.Serializer.Serialize(value)
	if err != nil {
		rc.recordError(err)
		return false, NewCacheError("setnx", key, err)
	}

	success, err := rc.client.SetNX(ctx, formattedKey, data, effectiveTTL).Result()
	if err != nil {
		rc.recordError(err)
		return false, NewCacheError("setnx", key, err)
	}

	if success {
		rc.updateStats(func(s *Stats) { s.Sets++ })
		rc.emitEvent(EventSet, key, value, nil)
	}

	return success, nil
}

// GetSet atomically sets a new value and returns the old value
func (rc *RedisCache) GetSet(ctx context.Context, key string, value interface{}) (interface{}, bool, error) {
	formattedKey := rc.formatKey(key)

	data, err := rc.config.Serializer.Serialize(value)
	if err != nil {
		rc.recordError(err)
		return nil, false, NewCacheError("getset", key, err)
	}

	oldData, err := rc.client.GetSet(ctx, formattedKey, data).Result()
	if err != nil {
		if err == redis.Nil {
			rc.updateStats(func(s *Stats) { s.Sets++ })
			rc.emitEvent(EventSet, key, value, nil)
			return nil, false, nil
		}
		rc.recordError(err)
		return nil, false, NewCacheError("getset", key, err)
	}

	var oldValue interface{}
	err = rc.config.Serializer.Deserialize([]byte(oldData), &oldValue)
	if err != nil {
		rc.recordError(err)
		return nil, false, NewCacheError("getset", key, err)
	}

	rc.updateStats(func(s *Stats) { s.Sets++; s.Hits++ })
	rc.emitEvent(EventSet, key, value, nil)
	return oldValue, true, nil
}
