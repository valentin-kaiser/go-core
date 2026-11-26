package etcd_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/config"
	"github.com/valentin-kaiser/go-core/v2/internal/await"
)

const (
	configAppName     = "app"
	missingConfigApp  = "missing"
	watchedConfigApp  = "watched"
	configName        = "alpha"
	configPort        = 4242
	watchedFirstName  = "v1"
	watchedSecondName = "v2"
	watchRetryEvery   = 250 * time.Millisecond
	watchEventTimeout = 10 * time.Second
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
	src := cli.ConfigSource(configAppName)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	cfg := &cfgConfig{Name: configName, Port: configPort}
	err := src.Save(ctx, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	values, err := src.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if values["name"] != configName {
		t.Fatalf("expected name=%s, got %v", configName, values["name"])
	}
	// YAML decodes integer leaves as int.
	if v, ok := values["port"].(int); !ok || v != configPort {
		t.Fatalf("expected port=%d, got %v (%T)", configPort, values["port"], values["port"])
	}
}

func TestConfigSourceLoadNotFound(t *testing.T) {
	cli := newTestClient(t)
	src := cli.ConfigSource(missingConfigApp)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	_, err := src.Load(ctx)
	if !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestConfigSourceWatchEmitsChanges(t *testing.T) {
	cli := newTestClient(t)
	src := cli.ConfigSource(watchedConfigApp)

	ctx := t.Context()

	err := src.Save(ctx, &cfgConfig{Name: watchedFirstName, Port: 1})
	if err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	var mu sync.Mutex
	var got map[string]any
	done := make(chan struct{}, 4)

	cancelWatch, err := src.Watch(ctx, func(values map[string]any) {
		mu.Lock()
		got = values
		mu.Unlock()
		// Only signal once we observe the post-update snapshot to tolerate
		// the intermediate delete event produced by Save.
		if values["name"] == watchedSecondName {
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

	// The watch subscribes asynchronously and misses updates made before that, so the
	// update is repeated until the callback reports it.
	port := 2
	await.Until(t, watchEventTimeout, func() bool {
		err := src.Save(ctx, &cfgConfig{Name: watchedSecondName, Port: port})
		if err != nil {
			t.Fatalf("Save %s: %v", watchedSecondName, err)
		}
		port++
		select {
		case <-done:
			return true
		case <-time.After(watchRetryEvery):
			return false
		}
	}, "the watch callback to report the update")

	mu.Lock()
	defer mu.Unlock()
	if got["name"] != watchedSecondName {
		t.Fatalf("expected watch payload name=%s, got %v", watchedSecondName, got["name"])
	}
}
