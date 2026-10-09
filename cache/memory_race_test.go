package cache_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

// Exists drops expired entries. A concurrent Set of the same key must never lose its fresh value.
func TestMemoryCacheExistsDoesNotDropFreshValue(t *testing.T) {
	mc := cache.NewMemoryCache()
	defer func() { _ = mc.Close() }()
	ctx := context.Background()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = mc.Exists(ctx, "k")
			}
		}
	}()

	for i := 0; i < 100000; i++ {
		// A short TTL entry that expires, immediately followed by a long-lived one
		if err := mc.Set(ctx, "k", i, time.Nanosecond); err != nil {
			t.Fatal(err)
		}
		if err := mc.Set(ctx, "k", i, time.Hour); err != nil {
			t.Fatal(err)
		}
		ok, err := mc.Exists(ctx, "k")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("iteration %d: a fresh value was removed by a concurrent Exists", i)
		}
	}
	close(stop)
	wg.Wait()
}

// A key that was read is protected from eviction, as in a strict LRU.
func TestMemoryCacheEvictsLeastRecentlyRead(t *testing.T) {
	cfg := cache.DefaultConfig()
	cfg.MaxSize = 3
	cfg.EnableLRU = true
	mc := cache.NewMemoryCacheWithConfig(cfg)
	defer func() { _ = mc.Close() }()
	ctx := context.Background()

	for _, k := range []string{"a", "b", "c"} {
		if err := mc.Set(ctx, k, k, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	var v string
	if ok, _ := mc.Get(ctx, "a", &v); !ok {
		t.Fatal("a should be cached")
	}
	if err := mc.Set(ctx, "d", "d", time.Hour); err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]bool{"a": true, "b": false, "c": true, "d": true} {
		if ok, _ := mc.Exists(ctx, key); ok != want {
			t.Errorf("key %q cached = %v, want %v", key, ok, want)
		}
	}
}

// Reads, writes, expiry and eviction run together; the race detector checks the locking.
func TestMemoryCacheConcurrentMixed(t *testing.T) {
	cfg := cache.DefaultConfig()
	cfg.MaxSize = 50
	mc := cache.NewMemoryCacheWithConfig(cfg)
	defer func() { _ = mc.Close() }()
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				k := "k" + string(rune('a'+(i+g)%26))
				switch i % 4 {
				case 0:
					_ = mc.Set(ctx, k, i, time.Duration(i%3)*time.Millisecond)
				case 3:
					_ = mc.Delete(ctx, k)
				default:
					var v int
					_, _ = mc.Get(ctx, k, &v)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := mc.GetSize(); got > 60 {
		t.Errorf("cache holds %d items, limit is 50", got)
	}
}

func TestMemoryCacheStatsCountHitsAndMisses(t *testing.T) {
	mc := cache.NewMemoryCache()
	defer func() { _ = mc.Close() }()
	ctx := context.Background()
	_ = mc.Set(ctx, "k", 1, time.Hour)

	var v int
	for i := 0; i < 3; i++ {
		_, _ = mc.Get(ctx, "k", &v)
	}
	_, _ = mc.Get(ctx, "missing", &v)

	s := mc.GetStats()
	if s.Hits != 3 || s.Misses != 1 {
		t.Fatalf("hits=%d misses=%d, want 3 and 1", s.Hits, s.Misses)
	}
	if s.HitRatio != 0.75 {
		t.Fatalf("hit ratio %v, want 0.75", s.HitRatio)
	}
}
