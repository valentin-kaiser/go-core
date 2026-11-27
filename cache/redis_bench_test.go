//go:build integration

package cache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

// Needs a Redis server: REDIS_URL, default redis://localhost:6379. The benchmarks fail without one.
func newBenchRedis(b *testing.B, keys int) *cache.RedisCache {
	b.Helper()

	client := connectRedis(b)

	cfg := cache.DefaultConfig()
	cfg.Namespace = "bench:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	rc := cache.NewRedisCacheWithConfig(client, cfg)
	b.Cleanup(func() {
		err := rc.Clear(context.Background())
		if err != nil {
			b.Error(err)
		}
		err = rc.Close()
		if err != nil {
			b.Error(err)
		}
	})

	ctx := context.Background()
	for i := range keys {
		err := rc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name", Tags: []string{"a", "b"}}, time.Hour)
		if err != nil {
			b.Fatal(err)
		}
	}
	return rc
}

func BenchmarkRedisCacheGet(b *testing.B) {
	rc := newBenchRedis(b, benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, err := rc.Get(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), &v)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRedisCacheGetParallel(b *testing.B) {
	rc := newBenchRedis(b, benchKeyCount)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			_, err := rc.Get(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), &v)
			if err != nil {
				b.Error(err)
				return
			}
			i++
		}
	})
}

func BenchmarkRedisCacheSet(b *testing.B) {
	rc := newBenchRedis(b, 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := rc.Set(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), val, time.Hour)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRedisCacheSetParallel(b *testing.B) {
	rc := newBenchRedis(b, 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			err := rc.Set(ctx, "key-"+strconv.Itoa(i&(benchKeyCount-1)), val, time.Hour)
			if err != nil {
				b.Error(err)
				return
			}
			i++
		}
	})
}

func BenchmarkRedisCacheGetMulti(b *testing.B) {
	rc := newBenchRedis(b, benchKeyCount)
	ctx := context.Background()
	keys := make([]string, benchMultiCount)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rc.GetMulti(ctx, keys)
		if err != nil {
			b.Fatal(err)
		}
	}
}
