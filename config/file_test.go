package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/config"
	"github.com/valentin-kaiser/go-core/v2/internal/await"
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
	t.Cleanup(config.Reset)

	dir := t.TempDir()
	cfg := &fileTestConfig{Name: "v1", Port: 1}
	err := config.Manager().WithName("file-watch-test").WithPath(dir).Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = config.Read()
	if err != nil {
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

	ctx := t.Context()
	err = config.StartWatch(ctx)
	if err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	t.Cleanup(config.StopWatch)

	// Rewrite the file the way an editor might: truncate and write new
	// content directly to the same path.
	path := filepath.Join(dir, "file-watch-test.yaml")
	data := []byte("name: v2\nport: 2\n")
	err = os.WriteFile(path, data, fileMode)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	n := await.Receive(t, changed, watchTimeout, "OnChange after the file write")
	got, ok := n.(*fileTestConfig)
	if !ok {
		t.Fatalf("OnChange received %T", n)
	}
	if got.Name != "v2" || got.Port != 2 {
		t.Fatalf("expected name=v2 port=2 after watch, got %+v", got)
	}
}

func TestFileSourceWatchReloadsOnRenameOverwrite(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	dir := t.TempDir()
	cfg := &fileTestConfig{Name: "v1", Port: 1}
	err := config.Manager().WithName("file-watch-test-rename").WithPath(dir).Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = config.Read()
	if err != nil {
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

	ctx := t.Context()
	err = config.StartWatch(ctx)
	if err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	t.Cleanup(config.StopWatch)

	// Simulate an editor that saves atomically by writing a temp file and
	// renaming it over the original.
	path := filepath.Join(dir, "file-watch-test-rename.yaml")
	tmp := path + ".tmp"
	err = os.WriteFile(tmp, []byte("name: v3\nport: 3\n"), fileMode)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err = os.Rename(tmp, path)
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	n := await.Receive(t, changed, watchTimeout, "OnChange after the rename-based save")
	got, ok := n.(*fileTestConfig)
	if !ok {
		t.Fatalf("OnChange received %T", n)
	}
	if got.Name != "v3" || got.Port != 3 {
		t.Fatalf("expected name=v3 port=3 after watch, got %+v", got)
	}
}
