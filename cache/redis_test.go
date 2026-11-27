//go:build integration

package cache_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
	"github.com/valentin-kaiser/go-core/internal/await"
)

func setupRedisTest(t *testing.T) *cache.RedisCache {
	t.Helper()

	client := connectRedis(t)

	config := cache.DefaultConfig()
	config.Namespace = fmt.Sprintf("test:%s:%d:%d", t.Name(), time.Now().UnixNano(), rand.Int63()) //nolint:gosec // unique test namespace, not security relevant

	c := cache.NewRedisCacheWithConfig(client, config)
	closeOnCleanup(t, "Redis cache", c)

	err := c.Clear(t.Context())
	if err != nil {
		t.Fatalf("clearing test data failed: %v", err)
	}

	return c
}

func TestRedisCache_BasicOperations(t *testing.T) { // Run tests in parallel
	c := setupRedisTest(t)
	ctx := t.Context()

	// Test Set and Get
	user := TestUser{ID: 1, Name: "John Doe", Email: "john@example.com"}
	err := c.Set(ctx, "user:1", user, time.Minute)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	var retrievedUser TestUser
	found, err := c.Get(ctx, "user:1", &retrievedUser)
	if err != nil {
		t.Fatalf("Failed to get value: %v", err)
	}

	if !found {
		t.Fatal("Expected to find value")
	}

	if retrievedUser.ID != user.ID || retrievedUser.Name != user.Name || retrievedUser.Email != user.Email {
		t.Errorf("Retrieved user doesn't match original: %+v != %+v", retrievedUser, user)
	}

	// Test Exists
	exists, err := c.Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check exists: %v", err)
	}
	if !exists {
		t.Error("Expected key to exist")
	}

	// Test Delete
	err = c.Delete(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to delete value: %v", err)
	}

	found, err = c.Get(ctx, "user:1", &retrievedUser)
	if err != nil {
		t.Fatalf("Failed to get value after delete: %v", err)
	}
	if found {
		t.Error("Expected value to be deleted")
	}
}

func TestRedisCache_TTL(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Set value with TTL
	err := c.Set(ctx, "test", "value", redisShortTTL)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Should exist immediately
	exists, err := c.Exists(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to check exists: %v", err)
	}
	if !exists {
		t.Error("Expected key to exist")
	}

	// Get TTL
	ttl, err := c.GetTTL(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to get TTL: %v", err)
	}

	if ttl <= 0 || ttl > redisShortTTL {
		t.Errorf("Expected TTL to be between 0 and %v, got %v", redisShortTTL, ttl)
	}

	// Wait for expiration
	await.Until(t, redisExpiryWait, func() bool {
		var value string
		found, err := c.Get(ctx, "test", &value)
		if err != nil {
			t.Errorf("Failed to get expired value: %v", err)
			return true
		}
		return !found
	}, "the key to expire")
}

func TestRedisCache_MultiOperations(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Test SetMulti
	items := map[string]any{
		"user:1": TestUser{ID: 1, Name: "Alice", Email: "alice@example.com"},
		"user:2": TestUser{ID: 2, Name: "Bob", Email: "bob@example.com"},
		"user:3": TestUser{ID: 3, Name: "Charlie", Email: "charlie@example.com"},
	}

	err := c.SetMulti(ctx, items, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set multiple items: %v", err)
	}

	// Test GetMulti
	keys := []string{"user:1", "user:2", "user:3", "user:4"} // user:4 doesn't exist
	results, err := c.GetMulti(ctx, keys)
	if err != nil {
		t.Fatalf("Failed to get multiple items: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Verify results
	for key, data := range results {
		// For JSON serializer, data comes back as map[string]interface{}
		userMap, ok := data.(map[string]any)
		if !ok {
			t.Fatalf("Expected data to be map[string]interface{} for key %s, got %T", key, data)
		}

		switch key {
		case "user:1":
			if name, ok := userMap["name"].(string); !ok || name != "Alice" {
				t.Errorf("Expected user:1 name to be Alice, got %v", userMap["name"])
			}
		case "user:2":
			if name, ok := userMap["name"].(string); !ok || name != "Bob" {
				t.Errorf("Expected user:2 name to be Bob, got %v", userMap["name"])
			}
		case "user:3":
			if name, ok := userMap["name"].(string); !ok || name != "Charlie" {
				t.Errorf("Expected user:3 name to be Charlie, got %v", userMap["name"])
			}
		default:
			t.Errorf("Unexpected key in results: %s", key)
		}
	}

	// Test DeleteMulti
	deleteKeys := []string{"user:1", "user:2"}
	err = c.DeleteMulti(ctx, deleteKeys)
	if err != nil {
		t.Fatalf("Failed to delete multiple items: %v", err)
	}

	// Verify deletions
	for _, key := range deleteKeys {
		exists, err := c.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check exists for %s: %v", key, err)
		}
		if exists {
			t.Errorf("Expected %s to be deleted", key)
		}
	}

	// user:3 should still exist
	exists, err := c.Exists(ctx, "user:3")
	if err != nil {
		t.Fatalf("Failed to check exists for user:3: %v", err)
	}
	if !exists {
		t.Error("Expected user:3 to still exist")
	}
}

func TestRedisCache_SetNX(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Test SetNX (set if not exists)
	success, err := c.SetNX(ctx, "atomic:test", "value1", time.Hour)
	if err != nil {
		t.Fatalf("Failed SetNX: %v", err)
	}
	if !success {
		t.Error("Expected SetNX to succeed for new key")
	}

	// SetNX should fail for existing key
	success, err = c.SetNX(ctx, "atomic:test", "value2", time.Hour)
	if err != nil {
		t.Fatalf("Failed second SetNX: %v", err)
	}
	if success {
		t.Error("Expected SetNX to fail for existing key")
	}

	// Verify original value is unchanged
	var value string
	found, err := c.Get(ctx, "atomic:test", &value)
	if err != nil {
		t.Fatalf("Failed to get value: %v", err)
	}
	if !found || value != "value1" {
		t.Errorf("Expected value1, got %s (found: %v)", value, found)
	}
}

func TestRedisCache_GetSet(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	err := c.Set(ctx, "atomic:test", "value1", time.Hour)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Test GetSet
	oldValue, found, err := c.GetSet(ctx, "atomic:test", "value2")
	if err != nil {
		t.Fatalf("Failed GetSet: %v", err)
	}
	if !found {
		t.Error("Expected GetSet to find existing value")
	}

	oldValueStr, ok := oldValue.(string)
	if !ok {
		t.Fatalf("Expected old value to be string, got %T", oldValue)
	}
	if oldValueStr != "value1" {
		t.Errorf("Expected old value to be value1, got %s", oldValueStr)
	}

	// Verify new value is set
	var value string
	found, err = c.Get(ctx, "atomic:test", &value)
	if err != nil {
		t.Fatalf("Failed to get new value: %v", err)
	}
	if !found || value != "value2" {
		t.Errorf("Expected value2, got %s (found: %v)", value, found)
	}
}

func TestRedisCache_Increment(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Test Increment on non-existing key
	newValue, err := c.Increment(ctx, "counter", 1)
	if err != nil {
		t.Fatalf("Failed to increment non-existing key: %v", err)
	}
	if newValue != 1 {
		t.Errorf("Expected counter to be 1, got %d", newValue)
	}

	// Test Increment on existing key
	newValue, err = c.Increment(ctx, "counter", 5)
	if err != nil {
		t.Fatalf("Failed to increment existing key: %v", err)
	}
	if newValue != 6 {
		t.Errorf("Expected counter to be 6, got %d", newValue)
	}

	// Test Decrement
	newValue, err = c.Increment(ctx, "counter", -2)
	if err != nil {
		t.Fatalf("Failed to decrement: %v", err)
	}
	if newValue != 4 {
		t.Errorf("Expected counter to be 4, got %d", newValue)
	}
}

func TestRedisCache_Keys(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Set some test keys
	testKeys := []string{"test:1", "test:2", "other:1"}
	for _, key := range testKeys {
		err := c.Set(ctx, key, "value", time.Hour)
		if err != nil {
			t.Fatalf("Failed to set %s: %v", key, err)
		}
	}

	// Verify keys exist individually since GetKeys is not available
	for _, testKey := range testKeys {
		exists, err := c.Exists(ctx, testKey)
		if err != nil {
			t.Fatalf("Failed to check exists for %s: %v", testKey, err)
		}
		if !exists {
			t.Errorf("Expected to find key %s", testKey)
		}
	}
}

func TestRedisCache_Clear(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Set multiple values
	testKeys := make([]string, 5)
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("key%d", i)
		testKeys[i-1] = key
		err := c.Set(ctx, key, fmt.Sprintf("value%d", i), time.Hour)
		if err != nil {
			t.Fatalf("Failed to set %s: %v", key, err)
		}
	}

	// Verify they exist
	for _, key := range testKeys {
		exists, err := c.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check exists for %s: %v", key, err)
		}
		if !exists {
			t.Errorf("Expected %s to exist before clear", key)
		}
	}

	// Clear cache
	err := c.Clear(ctx)
	if err != nil {
		t.Fatalf("Failed to clear cache: %v", err)
	}

	// Verify no keys exist
	for _, key := range testKeys {
		exists, err := c.Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check exists for %s: %v", key, err)
		}
		if exists {
			t.Errorf("Expected %s to not exist after clear", key)
		}
	}
}

func TestRedisCache_Pipeline(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Test pipeline operations
	items := map[string]any{
		"pipe:1": "value1",
		"pipe:2": "value2",
		"pipe:3": "value3",
	}

	err := c.SetMulti(ctx, items, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set multiple items via pipeline: %v", err)
	}

	// Verify all items were set
	for key, expectedValue := range items {
		var value string
		found, err := c.Get(ctx, key, &value)
		if err != nil {
			t.Fatalf("Failed to get %s: %v", key, err)
		}
		if !found {
			t.Errorf("Expected to find %s", key)
		}
		if value != expectedValue {
			t.Errorf("Expected %s to be %s, got %s", key, expectedValue, value)
		}
	}
}

func TestRedisCache_Events(t *testing.T) {
	eventsChan := make(chan cache.Event, eventBufferSize)

	c := setupRedisTest(t)
	c = c.WithEventHandler(func(event cache.Event) {
		eventsChan <- event
	})
	ctx := t.Context()

	// Set value
	err := c.Set(ctx, "test", "value", time.Hour)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Get value
	var value string
	_, err = c.Get(ctx, "test", &value)
	if err != nil {
		t.Fatalf("Failed to get value: %v", err)
	}

	// Delete value
	err = c.Delete(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to delete value: %v", err)
	}

	// Check events
	timeout := time.After(time.Second)
	var events []cache.Event

	for len(events) < 3 {
		select {
		case event := <-eventsChan:
			events = append(events, event)
		case <-timeout:
			t.Fatal("Timeout waiting for events")
		}
	}

	expectedEventTypes := []cache.EventType{cache.EventSet, cache.EventGet, cache.EventDelete}
	for i, expectedType := range expectedEventTypes {
		if events[i].Type != expectedType {
			t.Errorf("Expected event %d to be %s, got %s", i, expectedType, events[i].Type)
		}
		if events[i].Key != "test" {
			t.Errorf("Expected event %d key to be 'test', got '%s'", i, events[i].Key)
		}
	}
}

func TestRedisCache_TTLOperations(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Set value with TTL
	err := c.Set(ctx, "test", "value", time.Hour)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Get TTL
	ttl, err := c.GetTTL(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to get TTL: %v", err)
	}

	if ttl <= 0 || ttl > time.Hour {
		t.Errorf("Expected TTL to be between 0 and 1 hour, got %v", ttl)
	}

	// Update TTL
	err = c.SetTTL(ctx, "test", time.Minute)
	if err != nil {
		t.Fatalf("Failed to set TTL: %v", err)
	}

	// Verify updated TTL
	newTTL, err := c.GetTTL(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to get updated TTL: %v", err)
	}

	if newTTL <= 0 || newTTL > time.Minute {
		t.Errorf("Expected updated TTL to be between 0 and 1 minute, got %v", newTTL)
	}

	// Test TTL for non-existent key
	_, err = c.GetTTL(ctx, "nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent key TTL")
	}
}

func TestRedisCache_Stats(t *testing.T) {
	c := setupRedisTest(t)
	ctx := t.Context()

	// Initial stats
	stats := c.GetStats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Sets != 0 {
		t.Errorf("Expected initial stats to be zero: %+v", stats)
	}

	// Set some values
	for i := 1; i <= 3; i++ {
		err := c.Set(ctx, fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i), time.Hour)
		if err != nil {
			t.Fatalf("Failed to set key%d: %v", i, err)
		}
	}

	// Get existing value (hit)
	var value string
	found, err := c.Get(ctx, "key1", &value)
	if err != nil {
		t.Fatalf("Failed to get key1: %v", err)
	}
	if !found {
		t.Error("Expected to find key1")
	}

	// Get non-existing value (miss)
	found, err = c.Get(ctx, "nonexistent", &value)
	if err != nil {
		t.Fatalf("Failed to get nonexistent key: %v", err)
	}
	if found {
		t.Error("Expected not to find nonexistent key")
	}

	// Check stats
	stats = c.GetStats()
	if stats.Sets != 3 {
		t.Errorf("Expected 3 sets, got %d", stats.Sets)
	}
	if stats.Hits != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}

	expectedHitRatio := float64(1) / float64(2) // 1 hit out of 2 total operations
	if stats.HitRatio != expectedHitRatio {
		t.Errorf("Expected hit ratio %f, got %f", expectedHitRatio, stats.HitRatio)
	}
}
