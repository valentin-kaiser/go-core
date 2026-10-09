package etcd_test

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// These run against the embedded etcd server of the tests, so they measure the client
// wrapper plus a local single node cluster, not a network round trip.

func BenchmarkLockUnlock(b *testing.B) {
	cli := newTestClient(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := cli.Lock(ctx, "bench", 10*time.Second)
		if err != nil {
			b.Fatal(err)
		}
		if err := l.Unlock(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTryLockContended(b *testing.B) {
	cli := newTestClient(b)
	ctx := context.Background()
	held, err := cli.Lock(ctx, "held", 30*time.Second)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = held.Unlock(ctx) })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := cli.TryLock(ctx, "held", 30*time.Second)
		if err != nil {
			b.Fatal(err)
		}
		if l != nil {
			b.Fatal("lock was granted while held")
		}
	}
}

func BenchmarkConfigSourceSave(b *testing.B) {
	cli := newTestClient(b)
	src := cli.ConfigSource("bench")
	ctx := context.Background()
	cfg := &cfgConfig{Name: "alpha", Port: 4242}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg.Port = i
		if err := src.Save(ctx, cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConfigSourceLoad(b *testing.B) {
	cli := newTestClient(b)
	src := cli.ConfigSource("bench-load")
	ctx := context.Background()
	if err := src.Save(ctx, &cfgConfig{Name: "alpha", Port: 4242}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := src.Load(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRawPut(b *testing.B) {
	cli := newTestClient(b)
	ctx := context.Background()
	raw := cli.Raw()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := raw.Put(ctx, cli.Prefix()+"/k"+strconv.Itoa(i&255), "v"); err != nil {
			b.Fatal(err)
		}
	}
}
