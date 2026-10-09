package cache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

type benchValue struct {
	ID   int
	Name string
	Tags []string
}

func newBenchCache(b *testing.B, keys int) *cache.MemoryCache {
	b.Helper()
	mc := cache.NewMemoryCache()
	b.Cleanup(func() { _ = mc.Close() })
	ctx := context.Background()
	for i := 0; i < keys; i++ {
		if err := mc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name", Tags: []string{"a", "b"}}, time.Hour); err != nil {
			b.Fatal(err)
		}
	}
	return mc
}

func BenchmarkMemoryCacheGetHit(b *testing.B) {
	mc := newBenchCache(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, _ = mc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v)
	}
}

// Every hit takes the exclusive lock (LRU update), so this should show whether reads serialize.
func BenchmarkMemoryCacheGetHitParallel(b *testing.B) {
	mc := newBenchCache(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			_, _ = mc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v)
			i++
		}
	})
}

func BenchmarkMemoryCacheGetMiss(b *testing.B) {
	mc := newBenchCache(b, 16)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, _ = mc.Get(ctx, "missing", &v)
	}
}

func BenchmarkMemoryCacheSet(b *testing.B) {
	mc := newBenchCache(b, 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mc.Set(ctx, "key-"+strconv.Itoa(i&1023), val, time.Hour)
	}
}

func BenchmarkMemoryCacheSetParallel(b *testing.B) {
	mc := newBenchCache(b, 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = mc.Set(ctx, "key-"+strconv.Itoa(i&1023), val, time.Hour)
			i++
		}
	})
}

func BenchmarkMemoryCacheExistsParallel(b *testing.B) {
	mc := newBenchCache(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = mc.Exists(ctx, "key-"+strconv.Itoa(i&1023))
			i++
		}
	})
}

// Mixed 90% read / 10% write, the usual cache shape.
func BenchmarkMemoryCacheMixedParallel(b *testing.B) {
	mc := newBenchCache(b, 1024)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := "key-" + strconv.Itoa(i&1023)
			if i%10 == 0 {
				_ = mc.Set(ctx, k, val, time.Hour)
			} else {
				var v benchValue
				_, _ = mc.Get(ctx, k, &v)
			}
			i++
		}
	})
}

func newConfiguredBenchCache(b *testing.B, cfg cache.Config, keys int) *cache.MemoryCache {
	b.Helper()
	mc := cache.NewMemoryCacheWithConfig(cfg)
	b.Cleanup(func() { _ = mc.Close() })
	ctx := context.Background()
	for i := 0; i < keys; i++ {
		if err := mc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name", Tags: []string{"a", "b"}}, time.Hour); err != nil {
			b.Fatal(err)
		}
	}
	return mc
}

func shardedBenchConfig() cache.Config {
	cfg := cache.DefaultConfig()
	cfg.Shards = 16
	cfg.MaxSize = 0
	return cfg
}

func nativeBenchConfig() cache.Config {
	cfg := cache.DefaultConfig()
	cfg.Serializer = &cache.NativeSerializer{}
	cfg.MaxSize = 0
	return cfg
}

func BenchmarkMemoryCacheMixedParallelSharded(b *testing.B) {
	mc := newConfiguredBenchCache(b, shardedBenchConfig(), 1024)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := "key-" + strconv.Itoa(i&1023)
			if i%10 == 0 {
				_ = mc.Set(ctx, k, val, time.Hour)
			} else {
				var v benchValue
				_, _ = mc.Get(ctx, k, &v)
			}
			i++
		}
	})
}

func BenchmarkMemoryCacheSetParallelSharded(b *testing.B) {
	mc := newConfiguredBenchCache(b, shardedBenchConfig(), 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = mc.Set(ctx, "key-"+strconv.Itoa(i&1023), val, time.Hour)
			i++
		}
	})
}

func BenchmarkMemoryCacheGetHitNative(b *testing.B) {
	mc := newConfiguredBenchCache(b, nativeBenchConfig(), 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, _ = mc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v)
	}
}

func BenchmarkMemoryCacheGetHitParallelNative(b *testing.B) {
	mc := newConfiguredBenchCache(b, nativeBenchConfig(), 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			_, _ = mc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v)
			i++
		}
	})
}

func BenchmarkMemoryCacheMixedParallelShardedNative(b *testing.B) {
	cfg := nativeBenchConfig()
	cfg.Shards = 16
	mc := newConfiguredBenchCache(b, cfg, 1024)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := "key-" + strconv.Itoa(i&1023)
			if i%10 == 0 {
				_ = mc.Set(ctx, k, val, time.Hour)
			} else {
				var v benchValue
				_, _ = mc.Get(ctx, k, &v)
			}
			i++
		}
	})
}
