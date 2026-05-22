package etcd_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/config"
)

type cfgConfig struct {
	Name string `yaml:"name" usage:"name"`
	Port int    `yaml:"port" usage:"port"`
}

func (c *cfgConfig) Validate() error {
	if c.Name == "" {
		return errors.New("name required")
	}
	return nil
}

func TestConfigSourceSaveAndLoad(t *testing.T) {
	cli := newTestClient(t)
	src := cli.ConfigSource("app")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := &cfgConfig{Name: "alpha", Port: 4242}
	if err := src.Save(ctx, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	values, err := src.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if values["name"] != "alpha" {
		t.Fatalf("expected name=alpha, got %v", values["name"])
	}
	// YAML decodes integer leaves as int.
	if v, ok := values["port"].(int); !ok || v != 4242 {
		t.Fatalf("expected port=4242, got %v (%T)", values["port"], values["port"])
	}
}

func TestConfigSourceLoadNotFound(t *testing.T) {
	cli := newTestClient(t)
	src := cli.ConfigSource("missing")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := src.Load(ctx)
	if !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestConfigSourceWatchEmitsChanges(t *testing.T) {
	cli := newTestClient(t)
	src := cli.ConfigSource("watched")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := src.Save(ctx, &cfgConfig{Name: "v1", Port: 1}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	var mu sync.Mutex
	var got map[string]interface{}
	done := make(chan struct{}, 4)

	cancelWatch, err := src.Watch(ctx, func(values map[string]interface{}) {
		mu.Lock()
		got = values
		mu.Unlock()
		// Only signal once we observe the post-update snapshot to tolerate
		// the intermediate delete event produced by Save.
		if values["name"] == "v2" {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer cancelWatch()

	// Give the watch a moment to subscribe before triggering an update.
	time.Sleep(200 * time.Millisecond)

	if err := src.Save(ctx, &cfgConfig{Name: "v2", Port: 2}); err != nil {
		t.Fatalf("Save v2: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watch callback never fired")
	}

	mu.Lock()
	defer mu.Unlock()
	if got["name"] != "v2" {
		t.Fatalf("expected watch payload name=v2, got %v", got["name"])
	}
}
