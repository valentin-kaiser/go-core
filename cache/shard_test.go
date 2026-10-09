package cache_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

func shardedConfig(shards int, max int64) cache.Config {
	cfg := cache.DefaultConfig()
	cfg.Shards = shards
	cfg.MaxSize = max
	cfg.CleanupInterval = 0
	return cfg
}

func TestShardedCacheBasicOperations(t *testing.T) {
	mc := cache.NewMemoryCacheWithConfig(shardedConfig(8, 0))
	ctx := context.Background()

	for i := 0; i < 200; i++ {
		if err := mc.Set(ctx, fmt.Sprintf("k%d", i), i, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if got := mc.GetSize(); got != 200 {
		t.Fatalf("size %d, want 200", got)
	}
	for i := 0; i < 200; i++ {
		var v int
		ok, err := mc.Get(ctx, fmt.Sprintf("k%d", i), &v)
		if err != nil || !ok || v != i {
			t.Fatalf("k%d: ok=%v v=%d err=%v", i, ok, v, err)
		}
	}
	if keys := mc.GetKeys(); len(keys) != 200 {
		t.Fatalf("%d keys, want 200", len(keys))
	}

	if err := mc.Delete(ctx, "k5"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := mc.Exists(ctx, "k5"); ok {
		t.Error("k5 exists after Delete")
	}
	if err := mc.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if mc.GetSize() != 0 || mc.GetStats().Size != 0 {
		t.Fatalf("not empty after Clear: size=%d stats=%d", mc.GetSize(), mc.GetStats().Size)
	}
}

// MaxSize is a hard maximum, also when it is not a multiple of the shard count or is smaller than it.
func TestShardedCacheNeverExceedsMaxSize(t *testing.T) {
	for _, tc := range []struct {
		shards int
		max    int64
	}{{16, 1}, {16, 5}, {4, 10}, {16, 100}, {7, 50}} {
		mc := cache.NewMemoryCacheWithConfig(shardedConfig(tc.shards, tc.max))
		ctx := context.Background()
		for i := 0; i < 2000; i++ {
			_ = mc.Set(ctx, fmt.Sprintf("k%d", i), i, time.Hour)
			if got := mc.GetSize(); int64(got) > tc.max {
				t.Fatalf("shards=%d max=%d: holds %d items", tc.shards, tc.max, got)
			}
		}
		if got := mc.GetStats().Size; got > tc.max {
			t.Fatalf("shards=%d max=%d: stats size %d", tc.shards, tc.max, got)
		}
	}
}

// The total never exceeds MaxSize.
func TestShardedCacheRespectsMaxSize(t *testing.T) {
	mc := cache.NewMemoryCacheWithConfig(shardedConfig(4, 100))
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		_ = mc.Set(ctx, fmt.Sprintf("k%d", i), i, time.Hour)
	}
	if got := mc.GetSize(); got > 100 {
		t.Fatalf("holds %d items, limit is 100", got)
	}
	if got := mc.GetSize(); got < 80 {
		t.Fatalf("holds only %d items of 100; the shards evict too much", got)
	}
	if stats := mc.GetStats(); stats.Size != mc.GetSize() || stats.Evictions == 0 {
		t.Fatalf("stats do not match: %+v", stats)
	}
}

func TestShardedCacheConcurrent(t *testing.T) {
	mc := cache.NewMemoryCacheWithConfig(shardedConfig(16, 500))
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 3000; i++ {
				k := fmt.Sprintf("k%d", (i*7+g)%700)
				switch i % 5 {
				case 0:
					_ = mc.Set(ctx, k, i, time.Duration(i%3)*time.Millisecond)
				case 4:
					_ = mc.Delete(ctx, k)
				default:
					var v int
					_, _ = mc.Get(ctx, k, &v)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := mc.GetSize(); got > 500 {
		t.Fatalf("holds %d items, limit is 500", got)
	}
}

type nativeValue struct {
	ID   int
	Name string
	Tags []string
}

func TestNativeSerializer(t *testing.T) {
	cfg := cache.DefaultConfig()
	cfg.Serializer = &cache.NativeSerializer{}
	mc := cache.NewMemoryCacheWithConfig(cfg)
	ctx := context.Background()

	// A struct, set by value and by pointer, is read back by value and by pointer
	if err := mc.Set(ctx, "v", nativeValue{ID: 1, Name: "a", Tags: []string{"x"}}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := mc.Set(ctx, "p", &nativeValue{ID: 2, Name: "b"}, time.Hour); err != nil {
		t.Fatal(err)
	}

	var v nativeValue
	if ok, err := mc.Get(ctx, "v", &v); err != nil || !ok || v.ID != 1 || v.Tags[0] != "x" {
		t.Fatalf("v: ok=%v v=%+v err=%v", ok, v, err)
	}
	var p nativeValue
	if ok, err := mc.Get(ctx, "p", &p); err != nil || !ok || p.ID != 2 {
		t.Fatalf("p: ok=%v p=%+v err=%v", ok, p, err)
	}

	// Into an interface, as GetMulti does
	multi, err := mc.GetMulti(ctx, []string{"v", "missing"})
	if err != nil || len(multi) != 1 {
		t.Fatalf("GetMulti: %v %v", multi, err)
	}
	if got, ok := multi["v"].(nativeValue); !ok || got.ID != 1 {
		t.Fatalf("GetMulti value: %#v", multi["v"])
	}

	// A different destination type goes through JSON
	var asMap map[string]interface{}
	if ok, err := mc.Get(ctx, "v", &asMap); err != nil || !ok || asMap["Name"] != "a" {
		t.Fatalf("map: ok=%v %v err=%v", ok, asMap, err)
	}

	// Scalars
	_ = mc.Set(ctx, "n", 42, time.Hour)
	var n int
	if ok, err := mc.Get(ctx, "n", &n); err != nil || !ok || n != 42 {
		t.Fatalf("n: %v %d %v", ok, n, err)
	}
	if err := mc.Delete(ctx, "n"); err != nil {
		t.Fatal(err)
	}
}

// The same serializer still encodes JSON for the second level of a tiered cache.
func TestNativeSerializerJSONMethods(t *testing.T) {
	s := &cache.NativeSerializer{}
	data, err := s.Serialize(nativeValue{ID: 3})
	if err != nil {
		t.Fatal(err)
	}
	var v nativeValue
	if err := s.Deserialize(data, &v); err != nil || v.ID != 3 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}
