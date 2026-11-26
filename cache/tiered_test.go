//go:build integration

package cache_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/cache"
	"github.com/valentin-kaiser/go-core/v2/internal/await"
)

func setupTieredTest(t *testing.T) *cache.TieredCache {
	t.Helper()

	l1Config := cache.DefaultConfig()
	l1Config.MaxSize = tieredL1MaxSize
	l1Cache := cache.NewMemoryCacheWithConfig(l1Config)

	client := connectRedis(t)

	l2Config := cache.DefaultConfig()
	l2Config.Namespace = fmt.Sprintf("tiered-test:%s:%d:%d", t.Name(), time.Now().UnixNano(), rand.Int63()) //nolint:gosec // unique test namespace, not security relevant
	l2Cache := cache.NewRedisCacheWithConfig(client, l2Config)

	tieredConfig := cache.DefaultConfig()
	tieredConfig.Namespace = fmt.Sprintf("tiered:%s:%d:%d", t.Name(), time.Now().UnixNano(), rand.Int63()) //nolint:gosec // unique test namespace, not security relevant

	c := cache.NewTieredCacheWithConfig(l1Cache, l2Cache, tieredConfig)
	closeOnCleanup(t, "tiered cache", c)

	err := l2Cache.Clear(t.Context())
	if err != nil {
		t.Fatalf("clearing test data failed: %v", err)
	}

	return c
}

func TestTieredCache_BasicOperations(t *testing.T) {
	c := setupTieredTest(t)
	ctx := t.Context()

	// Test Set and Get
	user := TestUser{ID: 1, Name: "John Doe", Email: "john@example.com"}
	err := c.Set(ctx, "user:1", user, time.Hour)
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

	// Value should exist in both L1 and L2
	l1Found, err := c.L1().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L1 exists: %v", err)
	}
	if !l1Found {
		t.Error("Expected value to exist in L1 cache")
	}

	l2Found, err := c.L2().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L2 exists: %v", err)
	}
	if !l2Found {
		t.Error("Expected value to exist in L2 cache")
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

	// Value should be deleted from both L1 and L2
	l1Found, err = c.L1().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L1 exists after delete: %v", err)
	}
	if l1Found {
		t.Error("Expected value to be deleted from L1 cache")
	}

	l2Found, err = c.L2().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L2 exists after delete: %v", err)
	}
	if l2Found {
		t.Error("Expected value to be deleted from L2 cache")
	}
}

func TestTieredCache_L1Eviction(t *testing.T) {
	c := setupTieredTest(t)
	ctx := t.Context()

	// Fill L1 cache beyond its capacity (L1 has maxSize of 5)
	for i := 1; i <= 7; i++ {
		user := TestUser{ID: i, Name: fmt.Sprintf("User%d", i), Email: fmt.Sprintf("user%d@example.com", i)}
		err := c.Set(ctx, fmt.Sprintf("user:%d", i), user, time.Hour)
		if err != nil {
			t.Fatalf("Failed to set user:%d: %v", i, err)
		}
	}

	// All items should be in L2
	for i := 1; i <= 7; i++ {
		l2Found, err := c.L2().Exists(ctx, fmt.Sprintf("user:%d", i))
		if err != nil {
			t.Fatalf("Failed to check L2 exists for user:%d: %v", i, err)
		}
		if !l2Found {
			t.Errorf("Expected user:%d to exist in L2 cache", i)
		}
	}

	// Only the last 5 items should be in L1 due to LRU eviction
	for i := 1; i <= 2; i++ {
		l1Found, err := c.L1().Exists(ctx, fmt.Sprintf("user:%d", i))
		if err != nil {
			t.Fatalf("Failed to check L1 exists for user:%d: %v", i, err)
		}
		if l1Found {
			t.Errorf("Expected user:%d to be evicted from L1 cache", i)
		}
	}

	for i := 3; i <= 7; i++ {
		l1Found, err := c.L1().Exists(ctx, fmt.Sprintf("user:%d", i))
		if err != nil {
			t.Fatalf("Failed to check L1 exists for user:%d: %v", i, err)
		}
		if !l1Found {
			t.Errorf("Expected user:%d to exist in L1 cache", i)
		}
	}
}

func TestTieredCache_L2Backfill(t *testing.T) {
	c := setupTieredTest(t)
	ctx := t.Context()

	// Set value in L2 directly (simulating L1 eviction)
	user := TestUser{ID: 1, Name: "John Doe", Email: "john@example.com"}
	err := c.L2().Set(ctx, "user:1", user, time.Hour)
	if err != nil {
		t.Fatalf("Failed to set value in L2: %v", err)
	}

	// Value should not be in L1
	l1Found, err := c.L1().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L1 exists: %v", err)
	}
	if l1Found {
		t.Error("Expected value to not exist in L1 initially")
	}

	// Get value through tiered cache (should trigger L2 -> L1 backfill)
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

	// Value should now be in both L1 and L2 due to backfill
	l1Found, err = c.L1().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L1 exists after backfill: %v", err)
	}
	if !l1Found {
		t.Error("Expected value to be backfilled to L1 cache")
	}

	l2Found, err := c.L2().Exists(ctx, "user:1")
	if err != nil {
		t.Fatalf("Failed to check L2 exists after backfill: %v", err)
	}
	if !l2Found {
		t.Error("Expected value to still exist in L2 cache")
	}
}

func TestTieredCache_TTL(t *testing.T) {
	c := setupTieredTest(t)
	ctx := t.Context()

	// Test TTL operations
	err := c.Set(ctx, "test", "value", time.Hour)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Check TTL
	ttl, err := c.TTL(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to get TTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Hour {
		t.Errorf("Expected TTL between 0 and 1 hour, got %v", ttl)
	}
}

func TestTieredCache_TTLPropagation(t *testing.T) {
	c := setupTieredTest(t)
	ctx := t.Context()

	// Set value with short TTL
	err := c.Set(ctx, "test", "value", redisShortTTL)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Check TTL in both caches
	l1TTL, err := c.L1().TTL(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to get L1 TTL: %v", err)
	}

	l2TTL, err := c.L2().TTL(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to get L2 TTL: %v", err)
	}

	// TTLs should be similar (within a reasonable margin)
	if abs(l1TTL-l2TTL) > ttlTolerance {
		t.Errorf("TTL difference too large: L1=%v, L2=%v", l1TTL, l2TTL)
	}

	// Value should be expired in both caches
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

func TestTieredCache_MultiOperations(t *testing.T) {
	c := setupTieredTest(t)
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

	// Test Fetch
	keys := []string{"user:1", "user:2", "user:3", "user:4"} // user:4 doesn't exist
	results, err := c.Fetch(ctx, keys)
	if err != nil {
		t.Fatalf("Failed to get multiple items: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Test DeleteMulti
	deleteKeys := []string{"user:1", "user:2"}
	err = c.DeleteMulti(ctx, deleteKeys)
	if err != nil {
		t.Fatalf("Failed to delete multiple items: %v", err)
	}

	// Verify deletions in both caches
	for _, key := range deleteKeys {
		l1Found, err := c.L1().Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check L1 exists for %s: %v", key, err)
		}
		if l1Found {
			t.Errorf("Expected %s to be deleted from L1", key)
		}

		l2Found, err := c.L2().Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check L2 exists for %s: %v", key, err)
		}
		if l2Found {
			t.Errorf("Expected %s to be deleted from L2", key)
		}
	}

	// user:3 should still exist in both caches
	l1Found, err := c.L1().Exists(ctx, "user:3")
	if err != nil {
		t.Fatalf("Failed to check L1 exists for user:3: %v", err)
	}
	if !l1Found {
		t.Error("Expected user:3 to still exist in L1")
	}

	l2Found, err := c.L2().Exists(ctx, "user:3")
	if err != nil {
		t.Fatalf("Failed to check L2 exists for user:3: %v", err)
	}
	if !l2Found {
		t.Error("Expected user:3 to still exist in L2")
	}
}

func TestTieredCache_Clear(t *testing.T) {
	c := setupTieredTest(t)
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

	// Clear cache
	err := c.Clear(ctx)
	if err != nil {
		t.Fatalf("Failed to clear cache: %v", err)
	}

	// Verify all keys are deleted from both caches
	for _, key := range testKeys {
		l1Found, err := c.L1().Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check L1 exists for %s: %v", key, err)
		}
		if l1Found {
			t.Errorf("Expected %s to be cleared from L1", key)
		}

		l2Found, err := c.L2().Exists(ctx, key)
		if err != nil {
			t.Fatalf("Failed to check L2 exists for %s: %v", key, err)
		}
		if l2Found {
			t.Errorf("Expected %s to be cleared from L2", key)
		}
	}
}

func TestTieredCache_Events(t *testing.T) {
	eventsChan := make(chan cache.Event, tieredEventBufferSize)

	c := setupTieredTest(t)
	c = c.WithEventHandler(func(event cache.Event) {
		eventsChan <- event
	})
	ctx := t.Context()

	// Set value (should generate events from both L1 and L2)
	err := c.Set(ctx, "test", "value", time.Hour)
	if err != nil {
		t.Fatalf("Failed to set value: %v", err)
	}

	// Get value (should generate events)
	var value string
	_, err = c.Get(ctx, "test", &value)
	if err != nil {
		t.Fatalf("Failed to get value: %v", err)
	}

	// Delete value (should generate events from both L1 and L2)
	err = c.Delete(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to delete value: %v", err)
	}

	// Wait for events
	timeout := time.After(time.Second * 2)
	var events []cache.Event

eventLoop:
	for len(events) < 3 {
		select {
		case event := <-eventsChan:
			events = append(events, event)
		case <-timeout:
			break eventLoop
		}
	}

	if len(events) < 3 {
		t.Errorf("Expected at least 3 events, got %d", len(events))
	}

	// Should have set, get, and delete events
	eventTypes := make(map[cache.EventType]bool)
	for _, event := range events {
		if event.Key == "test" {
			eventTypes[event.Type] = true
		}
	}

	expectedTypes := []cache.EventType{cache.EventSet, cache.EventGet, cache.EventDelete}
	for _, expectedType := range expectedTypes {
		if !eventTypes[expectedType] {
			t.Errorf("Expected event type %s not found", expectedType)
		}
	}
}

func TestTieredCache_Stats(t *testing.T) {
	c := setupTieredTest(t)
	ctx := t.Context()

	// Set some values
	for i := 1; i <= 3; i++ {
		err := c.Set(ctx, fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i), time.Hour)
		if err != nil {
			t.Fatalf("Failed to set key%d: %v", i, err)
		}
	}

	// Get existing value (hit in L1)
	var value string
	found, err := c.Get(ctx, "key1", &value)
	if err != nil {
		t.Fatalf("Failed to get key1: %v", err)
	}
	if !found {
		t.Error("Expected to find key1")
	}

	// Remove from L1 to test L2 hit
	err = c.L1().Delete(ctx, "key2")
	if err != nil {
		t.Fatalf("Failed to delete key2 from L1: %v", err)
	}

	// Get value that's only in L2 (should trigger backfill)
	found, err = c.Get(ctx, "key2", &value)
	if err != nil {
		t.Fatalf("Failed to get key2: %v", err)
	}
	if !found {
		t.Error("Expected to find key2")
	}

	// Get non-existing value (miss)
	found, err = c.Get(ctx, "nonexistent", &value)
	if err != nil {
		t.Fatalf("Failed to get nonexistent key: %v", err)
	}
	if found {
		t.Error("Expected not to find nonexistent key")
	}

	// Check combined stats
	stats := c.Stats()
	// 3 initial sets to both L1 and L2 (6 sets) + 1 backfill set to L1 (1 set) = 7 sets
	if stats.Sets != 7 {
		t.Errorf("Expected 7 sets, got %d", stats.Sets)
	}
	if stats.Hits < 2 {
		t.Errorf("Expected at least 2 hits, got %d", stats.Hits)
	}
	if stats.Misses < 1 {
		t.Errorf("Expected at least 1 miss, got %d", stats.Misses)
	}
}

// Helper function to calculate absolute difference between durations
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
