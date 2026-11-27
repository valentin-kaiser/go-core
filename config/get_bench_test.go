package config_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/config"
)

const benchGetName = "bench-get"

// Get() is typically called in hot paths, from many goroutines.

func setupGet(b *testing.B) {
	b.Helper()
	tempDir := b.TempDir()
	setFlagPath(b, tempDir)

	cfg := &TestConfig{ApplicationName: benchGetName, ServerPort: testPort, DatabaseURL: testDatabaseURL}
	err := config.Manager().WithPath(tempDir).WithName(benchGetName).Register(cfg)
	if err != nil {
		b.Fatalf("Register failed: %v", err)
	}
	err = config.Read()
	if err != nil {
		b.Fatalf("Read failed: %v", err)
	}
}

func BenchmarkGet(b *testing.B) {
	setupGet(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.Get()
	}
}

func BenchmarkGetParallel(b *testing.B) {
	setupGet(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = config.Get()
		}
	})
}
