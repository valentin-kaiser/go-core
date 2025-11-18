package logging

import (
	"fmt"
	"log"
	"runtime"
	"sync"
)

var (
	// global is the default adapter used when no package-specific adapter is set
	global = NewNoOpAdapter()
	// packages stores package-specific adapters
	packages sync.Map
	// mu protects the global adapter
	mu sync.RWMutex
	// debug enables/disables caller tracking for all adapters
	debug bool
	// anonymous enables anonymous caller tracking by using the package name and line instead of file path
	anonymous bool
)

// SetGlobalAdapter sets the global logging adapter for all packages
// This will be used as the default for all packages unless they have a specific adapter
func SetGlobalAdapter(adapter Adapter) {
	mu.Lock()
	defer mu.Unlock()
	global = adapter
}

// GetGlobalAdapter returns the current global adapter
func GetGlobalAdapter[T Adapter]() (T, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := global.(T)
	return a, ok
}

// SetPackageAdapter sets a specific adapter for a package
// This overrides the global adapter for the specified package
func SetPackageAdapter(pkg string, adapter Adapter) {
	packages.Store(pkg, adapter)
}

// DynamicAdapter wraps the package lookup to always use the current adapter
type DynamicAdapter struct {
	pkg string
}

// NewDynamicAdapter creates a dynamic adapter for a package
func NewDynamicAdapter(pkg string) Adapter {
	return &DynamicAdapter{pkg: pkg}
}

// SetLevel sets the log level for this dynamic adapter by delegating to the current active adapter.
func (d *DynamicAdapter) SetLevel(level Level) Adapter {
	d.current().SetLevel(level)
	return d
}

// GetLevel returns the current log level from the active adapter for this package.
func (d *DynamicAdapter) GetLevel() Level {
	return d.current().GetLevel()
}

// Trace returns a trace level event from the current active adapter.
func (d *DynamicAdapter) Trace() Event {
	return d.current().Trace()
}

// Debug returns a debug level event from the current active adapter.
func (d *DynamicAdapter) Debug() Event {
	return d.current().Debug()
}

// Info returns an info level event from the current active adapter.
func (d *DynamicAdapter) Info() Event {
	return d.current().Info()
}

// Warn returns a warn level event from the current active adapter.
func (d *DynamicAdapter) Warn() Event {
	return d.current().Warn()
}

// Error returns an error level event from the current active adapter.
func (d *DynamicAdapter) Error() Event {
	return d.current().Error()
}

// Fatal returns a fatal level event from the current active adapter.
func (d *DynamicAdapter) Fatal() Event {
	return d.current().Fatal()
}

// Panic returns a panic level event from the current active adapter.
func (d *DynamicAdapter) Panic() Event {
	return d.current().Panic()
}

// Printf logs a formatted message using the current active adapter.
func (d *DynamicAdapter) Printf(format string, v ...interface{}) {
	d.current().Printf(format, v...)
}

// WithPackage returns a new adapter instance for the specified package from the current active adapter.
func (d *DynamicAdapter) WithPackage(pkg string) Adapter {
	return d.current().WithPackage(pkg)
}

// Enabled returns whether logging is enabled for the current active adapter.
func (d *DynamicAdapter) Enabled() bool {
	return d.current().Enabled()
}

// Logger returns the underlying logger from the current active adapter.
func (d *DynamicAdapter) Logger() *log.Logger {
	return d.current().Logger()
}

// Debug sets whether to use caller tracking
func Debug(d bool) {
	debug = d
}

// Anonymous sets whether to use anonymous caller tracking
func Anonymous(a bool) {
	anonymous = a
}

// GetPackageLogger returns a logger for a specific package
// Returns a dynamic adapter that will always use the current global/package-specific adapter
func GetPackageLogger(pkg string) Adapter {
	return NewDynamicAdapter(pkg)
}

// DisablePackage disables logging for a specific package
func DisablePackage(pkg string) {
	SetPackageAdapter(pkg, NewNoOpAdapter())
}

// EnablePackage removes package-specific adapter, falling back to global
func EnablePackage(pkg string) {
	packages.Delete(pkg)
}

// SetPackageLevel sets the log level for a specific package
// If the package doesn't have a specific adapter, this creates one based on the global adapter
func SetPackageLevel(pkg string, level Level) {
	if adapter, ok := packages.Load(pkg); ok {
		a, ok := adapter.(Adapter)
		if !ok {
			return
		}

		a.SetLevel(level)
		return
	}

	// Create a package-specific adapter based on the global one
	mu.RLock()
	var newAdapter Adapter
	switch adapter := global.(type) {
	case *ZerologAdapter:
		newAdapter = NewZerologAdapterWithLogger(adapter.logger)
	case *StandardAdapter:
		newAdapter = NewStandardAdapterWithLogger(adapter.logger)
	default:
		newAdapter = NewNoOpAdapter() // Fallback to NoOpAdapter if unknown type
	}
	mu.RUnlock()

	newAdapter.SetLevel(level)
	SetPackageAdapter(pkg, newAdapter.WithPackage(pkg))
}

// GetPackageLevel returns the log level for a specific package
func GetPackageLevel(pkg string) Level {
	return GetPackageLogger(pkg).GetLevel()
}

// ListPackages returns all packages that have specific adapters
func ListPackages() []string {
	var p []string
	packages.Range(func(key, _ interface{}) bool {
		if pkg, ok := key.(string); ok {
			p = append(p, pkg)
		}
		return true
	})
	return p
}

// current returns the current adapter for this package
func (d *DynamicAdapter) current() Adapter {
	if adapter, ok := packages.Load(d.pkg); ok {
		a, ok := adapter.(Adapter)
		if !ok {
			return global
		}
		return a
	}

	mu.RLock()
	defer mu.RUnlock()
	return global.WithPackage(d.pkg)
}

func track() string {
	pc, file, line, ok := runtime.Caller(3)
	if !ok {
		return ""
	}

	if anonymous {
		if f := runtime.FuncForPC(pc); f != nil {
			return fmt.Sprintf("%s:%d", f.Name(), line)
		}
	}

	return fmt.Sprintf("%s:%d", file, line)
}
