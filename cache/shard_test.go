package cache_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

const (
	basicShards              = 8
	itemCount                = 200
	fillCount                = 2000
	limitShards              = 4
	limitMaxSize             = 100
	limitMinSize             = 80
	limitFillCount           = 1000
	concurrentShards         = 16
	shardedConcurrentMaxSize = 500
	shardedOpsPerWorker      = 3000
	shardedKeySpace          = 700
	scalarValue              = 42
)

func shardedConfig(shards int, maxSize int64) cache.Config {
	cfg := cache.DefaultConfig()
	cfg.Shards = shards
	cfg.MaxSize = maxSize
	cfg.CleanupInterval = 0
	return cfg
}

func TestShardedCacheBasicOperations(t *testing.T) {
	mc := cache.NewMemoryCacheWithConfig(shardedConfig(basicShards, 0))
	ctx := context.Background()

	for i := range itemCount {
		err := mc.Set(ctx, fmt.Sprintf("k%d", i), i, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := mc.GetSize(); got != itemCount {
		t.Fatalf("size %d, want 200", got)
	}
	for i := range itemCount {
		var v int
		ok, err := mc.Get(ctx, fmt.Sprintf("k%d", i), &v)
		if err != nil || !ok || v != i {
			t.Fatalf("k%d: ok=%v v=%d err=%v", i, ok, v, err)
		}
	}
	if keys := mc.GetKeys(); len(keys) != itemCount {
		t.Fatalf("%d keys, want 200", len(keys))
	}

	err := mc.Delete(ctx, "k5")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := mc.Exists(ctx, "k5")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("k5 exists after Delete")
	}
	err = mc.Clear(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mc.GetSize() != 0 || mc.GetStats().Size != 0 {
		t.Fatalf("not empty after Clear: size=%d stats=%d", mc.GetSize(), mc.GetStats().Size)
	}
}

// MaxSize is a hard maximum, also when it is not a multiple of the shard count or is smaller than it.
func TestShardedCacheNeverExceedsMaxSize(t *testing.T) {
	tests := []struct {
		name   string
		shards int
		max    int64
	}{
		{name: "max below shard count", shards: 16, max: 1},
		{name: "max below shard count by a few", shards: 16, max: 5},
		{name: "max not a multiple of shards", shards: 4, max: 10},
		{name: "max above shard count", shards: 16, max: 100},
		{name: "odd shard count", shards: 7, max: 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mc := cache.NewMemoryCacheWithConfig(shardedConfig(tc.shards, tc.max))
			ctx := context.Background()
			for i := range fillCount {
				err := mc.Set(ctx, fmt.Sprintf("k%d", i), i, time.Hour)
				if err != nil {
					t.Error(err)
				}
				if got := mc.GetSize(); got > tc.max {
					t.Fatalf("shards=%d max=%d: holds %d items", tc.shards, tc.max, got)
				}
			}
			if got := mc.GetStats().Size; got > tc.max {
				t.Fatalf("shards=%d max=%d: stats size %d", tc.shards, tc.max, got)
			}
		})
	}
}

// The total never exceeds MaxSize.
func TestShardedCacheRespectsMaxSize(t *testing.T) {
	mc := cache.NewMemoryCacheWithConfig(shardedConfig(limitShards, limitMaxSize))
	ctx := context.Background()
	for i := range limitFillCount {
		err := mc.Set(ctx, fmt.Sprintf("k%d", i), i, time.Hour)
		if err != nil {
			t.Error(err)
		}
	}
	if got := mc.GetSize(); got > limitMaxSize {
		t.Fatalf("holds %d items, limit is 100", got)
	}
	if got := mc.GetSize(); got < limitMinSize {
		t.Fatalf("holds only %d items of 100; the shards evict too much", got)
	}
	if stats := mc.GetStats(); stats.Size != mc.GetSize() || stats.Evictions == 0 {
		t.Fatalf("stats do not match: %+v", stats)
	}
}

func TestShardedCacheConcurrent(t *testing.T) {
	mc := cache.NewMemoryCacheWithConfig(shardedConfig(concurrentShards, shardedConcurrentMaxSize))
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := range workerCount {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range shardedOpsPerWorker {
				k := fmt.Sprintf("k%d", (i*7+g)%shardedKeySpace)
				switch i % 5 {
				case 0:
					err := mc.Set(ctx, k, i, time.Duration(i%3)*time.Millisecond)
					if err != nil {
						t.Error(err)
					}
				case 4:
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
	if got := mc.GetSize(); got > shardedConcurrentMaxSize {
		t.Fatalf("holds %d items, limit is 500", got)
	}
}

type nativeValue struct {
	ID   int
	Name string
	Tags []string
}

func newNativeCache(t *testing.T) *cache.MemoryCache {
	t.Helper()

	cfg := cache.DefaultConfig()
	cfg.Serializer = &cache.NativeSerializer{}
	mc := cache.NewMemoryCacheWithConfig(cfg)
	closeOnCleanup(t, "cache", mc)
	return mc
}

// A struct, set by value and by pointer, is read back by value and by pointer.
func TestNativeSerializerStructRoundTrip(t *testing.T) {
	mc := newNativeCache(t)
	ctx := context.Background()

	err := mc.Set(ctx, "v", nativeValue{ID: 1, Name: "a", Tags: []string{"x"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	err = mc.Set(ctx, "p", &nativeValue{ID: 2, Name: "b"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	var v nativeValue
	ok, err := mc.Get(ctx, "v", &v)
	if err != nil || !ok || v.ID != 1 || v.Tags[0] != "x" {
		t.Fatalf("v: ok=%v v=%+v err=%v", ok, v, err)
	}
	var p nativeValue
	ok, err = mc.Get(ctx, "p", &p)
	if err != nil || !ok || p.ID != 2 {
		t.Fatalf("p: ok=%v p=%+v err=%v", ok, p, err)
	}
}

// Into an interface, as GetMulti does.
func TestNativeSerializerGetMulti(t *testing.T) {
	mc := newNativeCache(t)
	ctx := context.Background()

	err := mc.Set(ctx, "v", nativeValue{ID: 1, Name: "a", Tags: []string{"x"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	multi, err := mc.GetMulti(ctx, []string{"v", "missing"})
	if err != nil || len(multi) != 1 {
		t.Fatalf("GetMulti: %v %v", multi, err)
	}
	if got, ok := multi["v"].(nativeValue); !ok || got.ID != 1 {
		t.Fatalf("GetMulti value: %#v", multi["v"])
	}
}

// A different destination type goes through JSON.
func TestNativeSerializerDifferentDestinationType(t *testing.T) {
	mc := newNativeCache(t)
	ctx := context.Background()

	err := mc.Set(ctx, "v", nativeValue{ID: 1, Name: "a", Tags: []string{"x"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	var asMap map[string]any
	ok, err := mc.Get(ctx, "v", &asMap)
	if err != nil || !ok || asMap["Name"] != "a" {
		t.Fatalf("map: ok=%v %v err=%v", ok, asMap, err)
	}
}

func TestNativeSerializerScalars(t *testing.T) {
	mc := newNativeCache(t)
	ctx := context.Background()

	err := mc.Set(ctx, "n", scalarValue, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	ok, err := mc.Get(ctx, "n", &n)
	if err != nil || !ok || n != scalarValue {
		t.Fatalf("n: %v %d %v", ok, n, err)
	}
	err = mc.Delete(ctx, "n")
	if err != nil {
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
	err = s.Deserialize(data, &v)
	if err != nil || v.ID != 3 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}
