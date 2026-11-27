package etcd_test

import (
	"context"
	"strconv"
	"testing"
	"time"
)

const (
	lockTTL        = 10 * time.Second
	heldLockTTL    = 30 * time.Second
	rawKeyMask     = 255
	rawKeyPrefix   = "/k"
	rawValue       = "v"
	benchLockKey   = "bench"
	heldLockKey    = "held"
	benchConfigApp = "bench"
	loadConfigApp  = "bench-load"
	benchName      = "alpha"
	benchPort      = 4242
)

// These run against the embedded etcd server of the tests, so they measure the client
// wrapper plus a local single node cluster, not a network round trip.

func BenchmarkLockUnlock(b *testing.B) {
	cli := newTestClient(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := cli.Lock(ctx, benchLockKey, lockTTL)
		if err != nil {
			b.Fatal(err)
		}
		err = l.Unlock(ctx)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTryLockContended(b *testing.B) {
	cli := newTestClient(b)
	ctx := context.Background()
	held, err := cli.Lock(ctx, heldLockKey, heldLockTTL)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		err := held.Unlock(ctx)
		if err != nil {
			b.Errorf("unlocking held lock failed: %v", err)
		}
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, err := cli.TryLock(ctx, heldLockKey, heldLockTTL)
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
	src := cli.ConfigSource(benchConfigApp)
	ctx := context.Background()
	cfg := &cfgConfig{Name: benchName, Port: benchPort}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg.Port = i
		err := src.Save(ctx, cfg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConfigSourceLoad(b *testing.B) {
	cli := newTestClient(b)
	src := cli.ConfigSource(loadConfigApp)
	ctx := context.Background()
	err := src.Save(ctx, &cfgConfig{Name: benchName, Port: benchPort})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := src.Load(ctx)
		if err != nil {
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
		_, err := raw.Put(ctx, cli.Prefix()+rawKeyPrefix+strconv.Itoa(i&rawKeyMask), rawValue)
		if err != nil {
			b.Fatal(err)
		}
	}
}
