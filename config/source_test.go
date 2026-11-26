package config_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/config"
	"github.com/valentin-kaiser/go-core/v2/internal/await"
)

// mockSource is an in-memory config.Source used to verify the manager's
// pluggable source plumbing without touching the file system or etcd.
type mockSource struct {
	name string

	mu        sync.Mutex
	values    map[string]any
	notFound  bool
	readOnly  bool
	saveCount int
	loadCount int
	listeners []func(map[string]any)
}

func newMockSource(name string) *mockSource {
	return &mockSource{name: name, values: map[string]any{}}
}

func (m *mockSource) Name() string { return m.name }

func (m *mockSource) Load(_ context.Context) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadCount++
	if m.notFound {
		return nil, config.ErrNotFound
	}
	out := make(map[string]any, len(m.values))
	maps.Copy(out, m.values)
	return out, nil
}

func (m *mockSource) Save(_ context.Context, _ config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readOnly {
		return config.ErrReadOnly
	}
	m.saveCount++
	// Pretend we serialised; for our tests we only need to clear notFound.
	m.notFound = false
	return nil
}

func (m *mockSource) Watch(_ context.Context, onChange func(map[string]any)) (func(), error) {
	m.mu.Lock()
	m.listeners = append(m.listeners, onChange)
	idx := len(m.listeners) - 1
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if idx < len(m.listeners) {
			m.listeners[idx] = nil
		}
	}, nil
}

func (m *mockSource) setValue(k string, v any) {
	m.mu.Lock()
	m.values[k] = v
	listeners := append([]func(map[string]any){}, m.listeners...)
	snap := make(map[string]any, len(m.values))
	maps.Copy(snap, m.values)
	m.mu.Unlock()
	for _, l := range listeners {
		if l != nil {
			l(snap)
		}
	}
}

type sourceTestConfig struct {
	Name string `yaml:"name" usage:"name"`
	Port int    `yaml:"port" usage:"port"`
}

func (c *sourceTestConfig) Validate() error {
	if c.Name == "" {
		return errors.New("name required")
	}
	return nil
}

func TestWithSourceLoadsFromSource(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	src := newMockSource("mock")
	src.setValue("name", "from-source")
	src.setValue("port", 9000)

	cfg := &sourceTestConfig{Name: "default", Port: 1}
	err := config.Manager().WithName("src-test-1").WithSource(src).Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = config.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got, ok := config.Get().(*sourceTestConfig)
	if !ok {
		t.Fatalf("Get returned %T", config.Get())
	}
	if got.Name != "from-source" || got.Port != 9000 {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestWithSourceBootstrapsOnNotFound(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	src := newMockSource("mock")
	src.notFound = true

	cfg := &sourceTestConfig{Name: "default", Port: 1}
	err := config.Manager().WithName("src-test-2").WithSource(src).Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = config.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if src.saveCount != 1 {
		t.Fatalf("expected one Save during bootstrap, got %d", src.saveCount)
	}
	if src.loadCount < 2 {
		t.Fatalf("expected at least two Load calls (initial + after bootstrap), got %d", src.loadCount)
	}
}

func TestWithOverlayMergesOnTopOfBase(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	base := newMockSource("base")
	base.setValue("name", "from-base")
	base.setValue("port", 1)
	overlay := newMockSource("overlay")
	overlay.setValue("port", 7777)

	cfg := &sourceTestConfig{Name: "default", Port: 0}
	err := config.Manager().
		WithName("src-test-3").
		WithSource(base).
		WithOverlay(overlay).
		Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	err = config.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got, ok := config.Get().(*sourceTestConfig)
	if !ok {
		t.Fatalf("Get returned %T", config.Get())
	}
	if got.Name != "from-base" || got.Port != 7777 {
		t.Fatalf("unexpected merged config: %+v", got)
	}
}

func TestStartWatchTriggersReload(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	src := newMockSource("mock")
	src.setValue("name", "v1")
	src.setValue("port", 1)

	cfg := &sourceTestConfig{Name: "default", Port: 1}
	err := config.Manager().WithName("src-test-4").WithSource(src).Register(cfg)
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

	src.setValue("name", "v2")

	n := await.Receive(t, changed, watchTimeout, "OnChange after the watch event")
	got, ok := n.(*sourceTestConfig)
	if !ok {
		t.Fatalf("OnChange received %T", n)
	}
	if got.Name != "v2" {
		t.Fatalf("expected name=v2 after watch, got %q", got.Name)
	}
}
