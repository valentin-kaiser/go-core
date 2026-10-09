package cache_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/valentin-kaiser/go-core/cache"
)

// Needs a Redis server: REDIS_URL, default redis://localhost:6379. The benchmarks are skipped without one.
func newBenchRedis(b *testing.B, keys int) *cache.RedisCache {
	b.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		b.Skipf("invalid REDIS_URL: %v", err)
	}
	client := redis.NewClient(opt)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		b.Skipf("Redis not available: %v", err)
	}

	cfg := cache.DefaultConfig()
	cfg.Namespace = "bench:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	rc := cache.NewRedisCacheWithConfig(client, cfg)
	b.Cleanup(func() {
		_ = rc.Clear(context.Background())
		_ = rc.Close()
	})

	ctx := context.Background()
	for i := 0; i < keys; i++ {
		if err := rc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name", Tags: []string{"a", "b"}}, time.Hour); err != nil {
			b.Fatal(err)
		}
	}
	return rc
}

func BenchmarkRedisCacheGet(b *testing.B) {
	rc := newBenchRedis(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		if _, err := rc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRedisCacheGetParallel(b *testing.B) {
	rc := newBenchRedis(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			if _, err := rc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v); err != nil {
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
		if err := rc.Set(ctx, "key-"+strconv.Itoa(i&1023), val, time.Hour); err != nil {
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
			if err := rc.Set(ctx, "key-"+strconv.Itoa(i&1023), val, time.Hour); err != nil {
				b.Error(err)
				return
			}
			i++
		}
	})
}

func BenchmarkRedisCacheGetMulti(b *testing.B) {
	rc := newBenchRedis(b, 1024)
	ctx := context.Background()
	keys := make([]string, 50)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rc.GetMulti(ctx, keys); err != nil {
			b.Fatal(err)
		}
	}
}
