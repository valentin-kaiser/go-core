package config_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/config"
	"github.com/valentin-kaiser/go-core/flag"
)

// A configuration of the size a real service has: nested sections, slices and maps.
type benchSection struct {
	Host    string        `yaml:"host" usage:"host"`
	Port    int           `yaml:"port" usage:"port"`
	Enabled bool          `yaml:"enabled" usage:"enabled"`
	Timeout time.Duration `yaml:"timeout" usage:"timeout"`
	Name    string        `yaml:"name" usage:"name"`
	Limit   float64       `yaml:"limit" usage:"limit"`
}

type benchLargeConfig struct {
	Name     string            `yaml:"name" usage:"name"`
	Verbose  bool              `yaml:"verbose" usage:"verbose"`
	Web      benchSection      `yaml:"web" usage:"web"`
	Database benchSection      `yaml:"database" usage:"database"`
	Cache    benchSection      `yaml:"cache" usage:"cache"`
	Queue    benchSection      `yaml:"queue" usage:"queue"`
	Mail     benchSection      `yaml:"mail" usage:"mail"`
	Auth     benchSection      `yaml:"auth" usage:"auth"`
	Metrics  benchSection      `yaml:"metrics" usage:"metrics"`
	Tracing  benchSection      `yaml:"tracing" usage:"tracing"`
	Origins  []string          `yaml:"origins" usage:"origins"`
	Labels   map[string]string `yaml:"labels" usage:"labels"`
}

func (c *benchLargeConfig) Validate() error { return nil }

func newBenchLarge() *benchLargeConfig {
	section := benchSection{Host: "localhost", Port: 8080, Enabled: true, Timeout: time.Second, Name: "x", Limit: 1.5}
	return &benchLargeConfig{
		Name: "bench-large", Web: section, Database: section, Cache: section, Queue: section,
		Mail: section, Auth: section, Metrics: section, Tracing: section,
		Origins: []string{"a", "b", "c"},
		Labels:  map[string]string{"env": "test", "tier": "bench"},
	}
}

func setupLarge(b *testing.B, name string) {
	b.Helper()
	tempDir := b.TempDir()
	original := flag.Path
	flag.Path = tempDir
	b.Cleanup(func() { flag.Path = original; config.Reset() })

	config.Reset()
	cfg := newBenchLarge()
	if err := config.Manager().WithPath(tempDir).WithName(name).Register(cfg); err != nil {
		b.Fatalf("Register failed: %v", err)
	}
	if err := config.Write(cfg); err != nil {
		b.Fatalf("Write failed: %v", err)
	}
}

// Register walks the struct tags with reflection and builds the key and flag tables.
func BenchmarkRegisterLarge(b *testing.B) {
	tempDir := b.TempDir()
	original := flag.Path
	flag.Path = tempDir
	b.Cleanup(func() { flag.Path = original; config.Reset() })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		config.Reset()
		b.StartTimer()
		if err := config.Manager().WithPath(tempDir).WithName(fmt.Sprintf("bench-large-%d", i)).Register(newBenchLarge()); err != nil {
			b.Fatal(err)
		}
	}
}

// Read reloads the file and maps about 60 keys onto the struct (the per key lookup is getValue).
func BenchmarkReadLarge(b *testing.B) {
	setupLarge(b, "bench-large-read")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := config.Read(); err != nil {
			b.Fatal(err)
		}
	}
}
