package cache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/cache"
)

// Tiered cache with two in-process layers, so only the tier logic is measured (no Redis).
func newBenchTiered(b *testing.B, keys int) *cache.TieredCache {
	b.Helper()
	l1 := cache.NewMemoryCache()
	l2 := cache.NewMemoryCache()
	b.Cleanup(func() { _ = l1.Close(); _ = l2.Close() })
	tc := cache.NewTieredCache(l1, l2)
	ctx := context.Background()
	for i := 0; i < keys; i++ {
		if err := tc.Set(ctx, "key-"+strconv.Itoa(i), benchValue{ID: i, Name: "name"}, time.Hour); err != nil {
			b.Fatal(err)
		}
	}
	return tc
}

func BenchmarkTieredCacheGetL1Hit(b *testing.B) {
	tc := newBenchTiered(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var v benchValue
		_, _ = tc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v)
	}
}

func BenchmarkTieredCacheGetParallel(b *testing.B) {
	tc := newBenchTiered(b, 1024)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			var v benchValue
			_, _ = tc.Get(ctx, "key-"+strconv.Itoa(i&1023), &v)
			i++
		}
	})
}

func BenchmarkTieredCacheSet(b *testing.B) {
	tc := newBenchTiered(b, 0)
	ctx := context.Background()
	val := benchValue{ID: 1, Name: "name"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tc.Set(ctx, "key-"+strconv.Itoa(i&1023), val, time.Hour)
	}
}
