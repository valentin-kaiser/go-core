package cache_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

const (
	existsIterations    = 100000
	concurrentMaxSize   = 50
	concurrentSizeSlack = 10
	workerCount         = 8
	opsPerWorker        = 2000
)

// Exists drops expired entries. A concurrent Set of the same key must never lose its fresh value.
func TestMemoryCacheExistsDoesNotDropFreshValue(t *testing.T) {
	mc := cache.NewMemoryCache()
	closeOnCleanup(t, "cache", mc)
	ctx := context.Background()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_, err := mc.Exists(ctx, "k")
				if err != nil {
					t.Error(err)
				}
			}
		}
	})

	for i := range existsIterations {
		// A short TTL entry that expires, immediately followed by a long-lived one
		err := mc.Set(ctx, "k", i, time.Nanosecond)
		if err != nil {
			t.Fatal(err)
		}
		err = mc.Set(ctx, "k", i, time.Hour)
		if err != nil {
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
	cfg.MaxSize = lruMaxSize
	cfg.EnableLRU = true
	mc := cache.NewMemoryCacheWithConfig(cfg)
	closeOnCleanup(t, "cache", mc)
	ctx := context.Background()

	for _, k := range []string{"a", "b", "c"} {
		err := mc.Set(ctx, k, k, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	var v string
	ok, err := mc.Get(ctx, "a", &v)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a should be cached")
	}
	err = mc.Set(ctx, "d", "d", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for key, want := range map[string]bool{"a": true, "b": false, "c": true, "d": true} {
		ok, err := mc.Exists(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if ok != want {
			t.Errorf("key %q cached = %v, want %v", key, ok, want)
		}
	}
}

// Reads, writes, expiry and eviction run together; the race detector checks the locking.
func TestMemoryCacheConcurrentMixed(t *testing.T) {
	cfg := cache.DefaultConfig()
	cfg.MaxSize = concurrentMaxSize
	mc := cache.NewMemoryCacheWithConfig(cfg)
	closeOnCleanup(t, "cache", mc)
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := range workerCount {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range opsPerWorker {
				k := "k" + string(rune('a'+(i+g)%26))
				switch i % 4 {
				case 0:
					err := mc.Set(ctx, k, i, time.Duration(i%3)*time.Millisecond)
					if err != nil {
						t.Error(err)
					}
				case 3:
					err := mc.Delete(ctx, k)
					if err != nil {
						t.Error(err)
					}
				default:
					var v int
					_, err := mc.Get(ctx, k, &v)
					if err != nil {
						t.Error(err)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if got := mc.GetSize(); got > concurrentMaxSize+concurrentSizeSlack {
		t.Errorf("cache holds %d items, limit is 50", got)
	}
}

func TestMemoryCacheStatsCountHitsAndMisses(t *testing.T) {
	mc := cache.NewMemoryCache()
	closeOnCleanup(t, "cache", mc)
	ctx := context.Background()
	err := mc.Set(ctx, "k", 1, time.Hour)
	if err != nil {
		t.Error(err)
	}

	var v int
	for range 3 {
		_, err := mc.Get(ctx, "k", &v)
		if err != nil {
			t.Error(err)
		}
	}
	_, err = mc.Get(ctx, "missing", &v)
	if err != nil {
		t.Error(err)
	}

	s := mc.GetStats()
	if s.Hits != 3 || s.Misses != 1 {
		t.Fatalf("hits=%d misses=%d, want 3 and 1", s.Hits, s.Misses)
	}
	if s.HitRatio != 0.75 {
		t.Fatalf("hit ratio %v, want 0.75", s.HitRatio)
	}
}
