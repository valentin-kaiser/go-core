package logging

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
)

// adapterBox lets the global adapter, an interface value, live in an atomic.Pointer
type adapterBox struct {
	adapter Adapter
}

var (
	// global is the default adapter used when no package-specific adapter is set.
	// It is read on every log call, so it is an atomic pointer instead of a mutex-guarded value.
	global atomic.Pointer[adapterBox]
	// packages stores package-specific adapters
	packages sync.Map
	// debug enables/disables caller tracking for all adapters
	debug atomic.Bool
	// anonymous enables anonymous caller tracking by using the package name and line instead of file path
	anonymous atomic.Bool
)

func init() {
	global.Store(&adapterBox{adapter: NewNoOpAdapter()})
}

// SetGlobalAdapter sets the global logging adapter for all packages
// This will be used as the default for all packages unless they have a specific adapter
func SetGlobalAdapter(adapter Adapter) {
	global.Store(&adapterBox{adapter: adapter})
}

// GetGlobalAdapter returns the current global adapter
func GetGlobalAdapter[T Adapter]() (T, bool) {
	a, ok := global.Load().adapter.(T)
	return a, ok
}

// GetGlobalAdapterInterface returns the current global adapter as an interface
func GetGlobalAdapterInterface() Adapter {
	return global.Load().adapter
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
	debug.Store(d)
}

// Anonymous sets whether to use anonymous caller tracking
func Anonymous(a bool) {
	anonymous.Store(a)
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
	var newAdapter Adapter
	switch adapter := global.Load().adapter.(type) {
	case *ZerologAdapter:
		newAdapter = NewZerologAdapterWithLogger(adapter.state.Load().logger)
	case *StandardAdapter:
		newAdapter = NewStandardAdapterWithLogger(adapter.logger)
	default:
		newAdapter = NewNoOpAdapter() // Fallback to NoOpAdapter if unknown type
	}

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
			return global.Load().adapter
		}
		return a
	}

	// WithPackage caches the derived adapter per package, so this does not allocate
	return global.Load().adapter.WithPackage(d.pkg)
}

// track returns the location of the caller of the adapter method that called it.
func track() string {
	return callerInfo(4)
}

// callerInfo resolves the call site skip frames above itself.
func callerInfo(skip int) string {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return ""
	}

	if anonymous.Load() {
		if f := runtime.FuncForPC(pc); f != nil {
			return fmt.Sprintf("%s:%d", f.Name(), line)
		}
	}

	return fmt.Sprintf("%s:%d", file, line)
}
