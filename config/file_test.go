package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/config"
)

type fileTestConfig struct {
	Name string `yaml:"name" usage:"name"`
	Port int    `yaml:"port" usage:"port"`
}

func (c *fileTestConfig) Validate() error {
	if c.Name == "" {
		return errors.New("name required")
	}
	return nil
}

func TestFileSourceWatchReloadsOnWrite(t *testing.T) {
	config.Reset()
	defer config.Reset()

	dir := t.TempDir()
	cfg := &fileTestConfig{Name: "v1", Port: 1}
	if err := config.Manager().WithName("file-watch-test").WithPath(dir).Register(cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := config.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}

	changed := make(chan config.Config, 1)
	config.OnChange(func(_, n config.Config) error {
		select {
		case changed <- n:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := config.StartWatch(ctx); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer config.StopWatch()

	// Rewrite the file the way an editor might: truncate and write new
	// content directly to the same path.
	path := filepath.Join(dir, "file-watch-test.yaml")
	data := []byte("name: v2\nport: 2\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	select {
	case n := <-changed:
		got, ok := n.(*fileTestConfig)
		if !ok {
			t.Fatalf("OnChange received %T", n)
		}
		if got.Name != "v2" || got.Port != 2 {
			t.Fatalf("expected name=v2 port=2 after watch, got %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnChange was not invoked after file write")
	}
}

func TestFileSourceWatchReloadsOnRenameOverwrite(t *testing.T) {
	config.Reset()
	defer config.Reset()

	dir := t.TempDir()
	cfg := &fileTestConfig{Name: "v1", Port: 1}
	if err := config.Manager().WithName("file-watch-test-rename").WithPath(dir).Register(cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := config.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}

	changed := make(chan config.Config, 1)
	config.OnChange(func(_, n config.Config) error {
		select {
		case changed <- n:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := config.StartWatch(ctx); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer config.StopWatch()

	// Simulate an editor that saves atomically by writing a temp file and
	// renaming it over the original.
	path := filepath.Join(dir, "file-watch-test-rename.yaml")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("name: v3\nport: 3\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	select {
	case n := <-changed:
		got, ok := n.(*fileTestConfig)
		if !ok {
			t.Fatalf("OnChange received %T", n)
		}
		if got.Name != "v3" || got.Port != 3 {
			t.Fatalf("expected name=v3 port=3 after watch, got %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnChange was not invoked after rename-based save")
	}
}
