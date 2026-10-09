package config_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/config"
	"github.com/valentin-kaiser/go-core/flag"
)

// Get() is called per scan batch in bleep's hot paths, from many goroutines.

func setupGet(b *testing.B) {
	b.Helper()
	tempDir := b.TempDir()
	originalPath := flag.Path
	flag.Path = tempDir
	b.Cleanup(func() { flag.Path = originalPath })

	cfg := &TestConfig{ApplicationName: "bench-get", ServerPort: 8080, DatabaseURL: "sqlite:///test.db"}
	if err := config.Manager().WithPath(tempDir).WithName("bench-get").Register(cfg); err != nil {
		b.Fatalf("Register failed: %v", err)
	}
	if err := config.Read(); err != nil {
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
