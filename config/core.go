// Package config provides a simple, structured, and extensible way to manage
// application configuration in Go.
//
// It builds upon the Viper library and adds
// powerful features like validation, default value registration,
// environment and flag integration, and structured config registration.
//
// Key Features:
//
//   - Register typed configuration structs with default values.
//   - Parse YAML configuration files and bind fields to CLI flags and environment variables.
//   - Automatically generate flags based on struct field tags.
//   - Validate configuration using custom logic (via `Validate()` method).
//   - Write current configuration back to disk and trigger onChange handlers.
//   - Load configuration from disk on each Read() call.
//   - Automatically fallbacks to default config creation if no file is found.
//
// All configuration structs must implement the `Config` interface:
//
//	type Config interface {
//	    Validate() error
//	}
//
// Example:
//
//	package config
//
//	import (
//	    "fmt"
//	    "github.com/valentin-kaiser/go-core/config"
//	    "github.com/valentin-kaiser/go-core/flag"
//	)
//
//	type ServerConfig struct {
//	    Host string `yaml:"host" usage:"The host of the server"`
//	    Port int    `yaml:"port" usage:"The port of the server"`
//	}
//
//	func (c *ServerConfig) Validate() error {
//	    if c.Host == "" {
//	        return fmt.Errorf("host cannot be empty")
//	    }
//	    if c.Port <= 0 {
//	        return fmt.Errorf("port must be greater than 0")
//	    }
//	    return nil
//	}
//
//	func Get() *ServerConfig {
//	    c, ok := config.Get().(*ServerConfig)
//	    if !ok {
//	        return &ServerConfig{}
//	    }
//	    return c
//	}
//
//	func init() {
//	    cfg := &ServerConfig{
//	        Host: "localhost",
//	        Port: 8080,
//	    }
//
//	    // Register config - path parameter is ignored, flag.Path will be used
//	    if err := config.Register("", "server", cfg); err != nil {
//	        fmt.Println("Error registering config:", err)
//	        return
//	    }
//
//	    // Parse flags (including --path and config-specific flags)
//	    flag.Init()
//
//	    // Read config using the parsed --path flag - loads from disk each time
//	    if err := config.Read(); err != nil {
//	        fmt.Println("Error reading config:", err)
//	        return
//	    }
//
//	    // Register onChange handler to be called when Write() is used
//	    config.OnChange(func(o, n config.Config) error {
//	        fmt.Println("Configuration changed")
//	        return nil
//	    })
//
//	    // Update configuration - this will trigger onChange handlers
//	    if err := config.Write(cfg); err != nil {
//	        fmt.Println("Error writing config:", err)
//	    }
//	}
package config

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/spf13/pflag"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
)

var (
	mutex = &sync.RWMutex{}
	cm    = &manager{
		name:     "config",
		defaults: make(map[string]interface{}),
		values:   make(map[string]interface{}),
		flags:    make(map[string]*pflag.Flag),
	}
)

// Config is the interface that all configuration structs must implement
// It should contain a Validate method that checks the configuration for errors
type Config interface {
	Validate() error
}

type manager struct {
	path         string
	name         string
	config       Config
	prefix       string
	defaults     map[string]interface{}
	values       map[string]interface{}
	flags        map[string]*pflag.Flag
	onChange     []func(o Config, n Config) error
	base         Source
	overlays     []Source
	watchCancels []func()
	watchCtx     context.Context
	watchCancel  context.CancelFunc
}

// Manager returns the singleton configuration manager instance
func Manager() *manager {
	mutex.Lock()
	defer mutex.Unlock()
	return cm
}

// WithPath sets the configuration path for the manager
func (m *manager) WithPath(path string) *manager {
	mutex.Lock()
	defer mutex.Unlock()
	m.path = path
	return m
}

// WithName sets the configuration name for the manager
func (m *manager) WithName(name string) *manager {
	mutex.Lock()
	defer mutex.Unlock()
	m.name = name
	m.prefix = strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return m
}

// WithSource replaces the base configuration source. Use this for
// etcd-only (or any other backend) mode where the local YAML file is not
// involved. Pass nil to revert to the default file source.
func (m *manager) WithSource(s Source) *manager {
	mutex.Lock()
	defer mutex.Unlock()
	m.base = s
	return m
}

// WithOverlay appends an overlay source that is merged on top of the base
// source on every Read(). Later overlays override earlier ones. Typical use:
// keep the file as the base source and add an etcd overlay for cluster-wide
// overrides.
func (m *manager) WithOverlay(s Source) *manager {
	if s == nil {
		return m
	}
	mutex.Lock()
	defer mutex.Unlock()
	m.overlays = append(m.overlays, s)
	return m
}

// Register registers a configuration struct and parses its tags
// The name is used as the name of the configuration file and the prefix for the environment variables
func (m *manager) Register(c Config) error {
	if c == nil {
		return apperror.NewError("the configuration provided is nil")
	}

	if reflect.TypeOf(c).Kind() != reflect.Ptr || reflect.TypeOf(c).Elem().Kind() != reflect.Struct {
		return apperror.NewErrorf("the configuration provided is not a pointer to a struct, got %T", c)
	}

	err := m.parseStructTags(reflect.ValueOf(c), "")
	if err != nil {
		return apperror.Wrap(err)
	}

	m.set(c)
	return nil
}

// OnChange registers a function that is called when the configuration changes
func OnChange(f func(o Config, n Config) error) {
	mutex.Lock()
	defer mutex.Unlock()
	cm.onChange = append(cm.onChange, f)
}

// Get returns the current configuration
func Get() Config {
	mutex.RLock()
	defer mutex.RUnlock()
	return cm.config
}

// Read loads the configuration from the configured sources, validates it and
// applies it. The base source is consulted first; any registered overlays are
// then merged on top. When the base source reports that no configuration
// exists yet (ErrNotFound), the current in-memory defaults are persisted and
// the source is loaded again. Read always reloads from the sources; values
// are never cached across calls.
func Read() error {
	if err := cm.ensureBase(); err != nil {
		return err
	}

	return cm.reload(context.Background())
}

// Write persists the configuration to the base source, validates it and
// applies it in memory. OnChange handlers are triggered with the previous and
// new configuration. If the base source is read-only, an error is returned.
func Write(change Config) error {
	if change == nil {
		return apperror.NewError("the configuration provided is nil")
	}

	if err := cm.ensureBase(); err != nil {
		return err
	}

	err := change.Validate()
	if err != nil {
		return apperror.Wrap(err)
	}

	err = cm.base.Save(context.Background(), change)
	if err != nil {
		return apperror.Wrap(err)
	}

	o := Get()
	cm.set(change)
	for _, f := range cm.onChange {
		err = f(o, change)
		if err != nil {
			return apperror.Wrap(err)
		}
	}

	return nil
}

// StartWatch subscribes to change notifications from the base source and all
// overlays that support them. When any watched source reports a change, the
// configuration is reloaded and OnChange handlers are invoked. Sources that
// return ErrWatchUnsupported are silently skipped. Call StopWatch to stop
// watching. StartWatch is safe to call multiple times; previous watches are
// cancelled first.
func StartWatch(ctx context.Context) error {
	return cm.startWatch(ctx)
}

// StopWatch cancels any active watches started via StartWatch.
func StopWatch() {
	cm.stopWatch()
}

// Reset clears the config package state
// Everything must be re-registered after calling this function
func Reset() {
	mutex.Lock()
	defer mutex.Unlock()

	if cm.watchCancel != nil {
		cm.watchCancel()
	}
	for _, c := range cm.watchCancels {
		if c != nil {
			c()
		}
	}

	cm = &manager{
		name:     "config",
		defaults: make(map[string]interface{}),
		values:   make(map[string]interface{}),
		flags:    make(map[string]*pflag.Flag),
	}
}

// Changed checks if two configuration values are different by comparing their reflection values.
// It returns true if the configurations differ, false if they are the same.
// This function handles nil values correctly and performs deep comparison of the underlying values.
func Changed(o, n any) bool {
	if o == nil && n == nil {
		return false
	}

	if o == nil || n == nil {
		return true
	}

	ov := reflect.ValueOf(o)
	nv := reflect.ValueOf(n)

	if ov.Kind() == reflect.Ptr && !ov.IsNil() {
		ov = ov.Elem()
	}
	if nv.Kind() == reflect.Ptr && !nv.IsNil() {
		nv = nv.Elem()
	}

	if ov.Kind() != nv.Kind() {
		return true
	}

	return !reflect.DeepEqual(ov.Interface(), nv.Interface())
}

// set applies the configuration to the global variable
func (m *manager) set(appConfig Config) {
	mutex.Lock()
	defer mutex.Unlock()
	m.config = appConfig
}

// ensureBase guarantees that a base source is configured. When none was set
// explicitly via WithSource, a file source pointing at the current name and
// flag.Path is created lazily.
func (m *manager) ensureBase() error {
	mutex.Lock()
	defer mutex.Unlock()

	if m.path == "" {
		m.path = flag.Path
	}

	if m.base == nil {
		m.base = newFileSource(m.name, m.path)
	} else if fs, ok := m.base.(*fileSource); ok {
		// Keep file source path/name in sync with WithPath/WithName updates.
		fs.name = m.name
		fs.path = m.path
	}

	return nil
}

// loadSources loads the base source and all overlays, merges them and stores
// the result in m.values. The caller must hold no locks.
func (m *manager) loadSources(ctx context.Context) error {
	mutex.RLock()
	base := m.base
	overlays := append([]Source(nil), m.overlays...)
	current := m.config
	mutex.RUnlock()

	if base == nil {
		return apperror.NewError("no configuration source configured")
	}

	baseValues, err := base.Load(ctx)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if current == nil {
				return apperror.NewError("configuration not found and no defaults registered")
			}
			saveErr := base.Save(ctx, current)
			if saveErr != nil {
				return apperror.NewErrorf("writing default configuration to %s source failed", base.Name()).AddError(saveErr)
			}
			baseValues, err = base.Load(ctx)
			if err != nil {
				return apperror.NewErrorf("reading configuration from %s source after bootstrap failed", base.Name()).AddError(err)
			}
		} else {
			return apperror.NewErrorf("reading configuration from %s source failed", base.Name()).AddError(err)
		}
	}

	overlayValues := make([]map[string]interface{}, 0, len(overlays))
	for _, o := range overlays {
		ov, err := o.Load(ctx)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return apperror.NewErrorf("reading configuration from %s overlay failed", o.Name()).AddError(err)
		}
		overlayValues = append(overlayValues, ov)
	}

	merged := mergeValues(append([]map[string]interface{}{baseValues}, overlayValues...)...)

	mutex.Lock()
	m.values = merged
	mutex.Unlock()
	return nil
}

// reload re-loads the configuration from all sources and applies it through
// the validate/set/OnChange pipeline.
func (m *manager) reload(ctx context.Context) error {
	if err := m.loadSources(ctx); err != nil {
		return err
	}

	if m.config == nil {
		return apperror.NewError("no configuration registered")
	}

	change, ok := reflect.New(reflect.TypeOf(m.config).Elem()).Interface().(Config)
	if !ok {
		return apperror.NewErrorf("creating new instance of %T failed", m.config)
	}

	err := m.unmarshal(change)
	if err != nil {
		return apperror.NewErrorf("unmarshalling configuration data in %T failed", m.config).AddError(err)
	}

	err = change.Validate()
	if err != nil {
		return apperror.Wrap(err)
	}

	o := Get()
	m.set(change)
	mutex.RLock()
	handlers := append([]func(Config, Config) error(nil), m.onChange...)
	mutex.RUnlock()
	for _, f := range handlers {
		if err := f(o, change); err != nil {
			return apperror.Wrap(err)
		}
	}
	return nil
}

// startWatch subscribes to all watchable sources. The provided context
// controls the lifetime of the watches. Calling startWatch more than once
// cancels the previous subscription set first.
func (m *manager) startWatch(ctx context.Context) error {
	m.stopWatch()

	if err := m.ensureBase(); err != nil {
		return err
	}

	watchCtx, cancel := context.WithCancel(ctx)

	mutex.Lock()
	m.watchCtx = watchCtx
	m.watchCancel = cancel
	sources := append([]Source{m.base}, m.overlays...)
	mutex.Unlock()

	callback := func(_ map[string]interface{}) {
		if err := m.reload(watchCtx); err != nil {
			// Reload errors during watch are non-fatal; swallow to keep the
			// watch loop alive. Users wanting visibility should register an
			// OnChange handler that logs.
			_ = err
		}
	}

	cancels := make([]func(), 0, len(sources))
	for _, s := range sources {
		c, err := s.Watch(watchCtx, callback)
		if err != nil {
			if errors.Is(err, ErrWatchUnsupported) {
				continue
			}
			cancel()
			for _, prev := range cancels {
				if prev != nil {
					prev()
				}
			}
			return apperror.NewErrorf("watching %s source failed", s.Name()).AddError(err)
		}
		if c != nil {
			cancels = append(cancels, c)
		}
	}

	mutex.Lock()
	m.watchCancels = cancels
	mutex.Unlock()
	return nil
}

// stopWatch cancels all active watches.
func (m *manager) stopWatch() {
	mutex.Lock()
	cancels := m.watchCancels
	rootCancel := m.watchCancel
	m.watchCancels = nil
	m.watchCancel = nil
	m.watchCtx = nil
	mutex.Unlock()

	for _, c := range cancels {
		if c != nil {
			c()
		}
	}
	if rootCancel != nil {
		rootCancel()
	}
}
