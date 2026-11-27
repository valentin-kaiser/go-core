package cache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

const (
	benchKeyCount     = 1024
	benchMissKeyCount = 16
	benchShards       = 16
	benchMultiCount   = 50
)

type benchValue struct {
	ID   int
	Name string
	Tags []string
}

func newBenchCache(b *testing.B, keys int) *cache.MemoryCache {
	b.Helper()
	mc := cache.NewMemoryCache()
	closeOnCleanup(b, "cache", mc)
	ctx := context.Background()
	for i := range keys {
		err := mc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name", Tags: []string{"a", "b"}}, time.Hour)
		if err != nil {
			b.Fatal(err)
		}
	}
	return mc
}

func BenchmarkMemoryCacheGetHit(b *testing.B) {
	mc := newBenchCache(b, benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, err := mc.Get(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), &v)
		if err != nil {
			b.Error(err)
		}
	}
}

// Every hit takes the exclusive lock (LRU update), so this should show whether reads serialize.
func BenchmarkMemoryCacheGetHitParallel(b *testing.B) {
	mc := newBenchCache(b, benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			_, err := mc.Get(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), &v)
			if err != nil {
				b.Error(err)
			}
			i++
		}
	})
}

func BenchmarkMemoryCacheGetMiss(b *testing.B) {
	mc := newBenchCache(b, benchMissKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, err := mc.Get(ctx, "missing", &v)
		if err != nil {
			b.Error(err)
		}
	}
}

func BenchmarkMemoryCacheSet(b *testing.B) {
	mc := newBenchCache(b, 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := mc.Set(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), val, time.Hour)
		if err != nil {
			b.Error(err)
		}
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
			err := mc.Set(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), val, time.Hour)
			if err != nil {
				b.Error(err)
			}
			i++
		}
	})
}

func BenchmarkMemoryCacheExistsParallel(b *testing.B) {
	mc := newBenchCache(b, benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, err := mc.Exists(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)))
			if err != nil {
				b.Error(err)
			}
			i++
		}
	})
}

// Mixed 90% read / 10% write, the usual cache shape.
func BenchmarkMemoryCacheMixedParallel(b *testing.B) {
	mc := newBenchCache(b, benchKeyCount)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := "key-" + strconv.Itoa(i&(benchKeyCount-1))
			if i%10 == 0 {
				err := mc.Set(ctx, k, val, time.Hour)
				if err != nil {
					b.Error(err)
				}
			} else {
				var v benchValue
				_, err := mc.Get(ctx, k, &v)
				if err != nil {
					b.Error(err)
				}
			}
			i++
		}
	})
}

func newConfiguredBenchCache(b *testing.B, cfg cache.Config, keys int) *cache.MemoryCache {
	b.Helper()
	mc := cache.NewMemoryCacheWithConfig(cfg)
	closeOnCleanup(b, "cache", mc)
	ctx := context.Background()
	for i := range keys {
		err := mc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name", Tags: []string{"a", "b"}}, time.Hour)
		if err != nil {
			b.Fatal(err)
		}
	}
	return mc
}

func shardedBenchConfig() cache.Config {
	cfg := cache.DefaultConfig()
	cfg.Shards = benchShards
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
	mc := newConfiguredBenchCache(b, shardedBenchConfig(), benchKeyCount)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := "key-" + strconv.Itoa(i&(benchKeyCount-1))
			if i%10 == 0 {
				err := mc.Set(ctx, k, val, time.Hour)
				if err != nil {
					b.Error(err)
				}
			} else {
				var v benchValue
				_, err := mc.Get(ctx, k, &v)
				if err != nil {
					b.Error(err)
				}
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
			err := mc.Set(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), val, time.Hour)
			if err != nil {
				b.Error(err)
			}
			i++
		}
	})
}

func BenchmarkMemoryCacheGetHitNative(b *testing.B) {
	mc := newConfiguredBenchCache(b, nativeBenchConfig(), benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, err := mc.Get(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), &v)
		if err != nil {
			b.Error(err)
		}
	}
}

func BenchmarkMemoryCacheGetHitParallelNative(b *testing.B) {
	mc := newConfiguredBenchCache(b, nativeBenchConfig(), benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			_, err := mc.Get(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), &v)
			if err != nil {
				b.Error(err)
			}
			i++
		}
	})
}

func BenchmarkMemoryCacheMixedParallelShardedNative(b *testing.B) {
	cfg := nativeBenchConfig()
	cfg.Shards = benchShards
	mc := newConfiguredBenchCache(b, cfg, benchKeyCount)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			k := "key-" + strconv.Itoa(i&(benchKeyCount-1))
			if i%10 == 0 {
				err := mc.Set(ctx, k, val, time.Hour)
				if err != nil {
					b.Error(err)
				}
			} else {
				var v benchValue
				_, err := mc.Get(ctx, k, &v)
				if err != nil {
					b.Error(err)
				}
			}
			i++
		}
	})
}
