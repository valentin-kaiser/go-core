package config_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/config"
)

const (
	benchHost  = "localhost"
	benchLimit = 1.5
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
	section := benchSection{Host: benchHost, Port: testPort, Enabled: true, Timeout: time.Second, Name: "x", Limit: benchLimit}
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
	setFlagPath(b, tempDir)
	b.Cleanup(config.Reset)

	config.Reset()
	cfg := newBenchLarge()
	err := config.Manager().WithPath(tempDir).WithName(name).Register(cfg)
	if err != nil {
		b.Fatalf("Register failed: %v", err)
	}
	err = config.Write(cfg)
	if err != nil {
		b.Fatalf("Write failed: %v", err)
	}
}

// Register walks the struct tags with reflection and builds the key and flag tables.
func BenchmarkRegisterLarge(b *testing.B) {
	tempDir := b.TempDir()
	setFlagPath(b, tempDir)
	b.Cleanup(config.Reset)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		config.Reset()
		b.StartTimer()
		err := config.Manager().WithPath(tempDir).WithName(fmt.Sprintf("bench-large-%d", i)).Register(newBenchLarge())
		if err != nil {
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
		err := config.Read()
		if err != nil {
			b.Fatal(err)
		}
	}
}
