// Package logging provides a flexible and extensible logging framework with support
// for multiple adapters and log levels. It offers a structured approach to logging
// with configurable output destinations and severity levels.
//
// The package supports various logging adapters including standard library logger,
// zerolog, and no-op implementations. It provides a global logger registry for
// convenient access across applications and supports hierarchical log levels
// from trace to fatal.
//
// Example usage:
//
//	import "github.com/valentin-kaiser/go-core/logging"
//
//	logger := logging.GetGlobalAdapter()
//	logger.Info().Str("key", "value").Msg("Application started")
package logging

import "log"

// Level represents log levels
type Level int

// Log level constants define the severity levels for logging operations.
// These levels are ordered from most verbose (TraceLevel) to least verbose (DisabledLevel).
const (
	TraceLevel    Level = -1 // TraceLevel logs very detailed diagnostic information
	DebugLevel    Level = 0  // DebugLevel logs debug information useful for development
	InfoLevel     Level = 1  // InfoLevel logs general information about application execution
	WarnLevel     Level = 2  // WarnLevel logs warnings about potentially harmful situations
	ErrorLevel    Level = 3  // ErrorLevel logs error events that might still allow the application to continue
	FatalLevel    Level = 4  // FatalLevel logs very severe error events that will presumably lead the application to abort
	PanicLevel    Level = 5  // PanicLevel logs error events that will cause the application to panic
	DisabledLevel Level = 6  // DisabledLevel disables all logging
)

// String returns the string representation of the log level
func (l Level) String() string {
	switch l {
	case TraceLevel:
		return "trace"
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	case FatalLevel:
		return "fatal"
	case PanicLevel:
		return "panic"
	case DisabledLevel:
		return "disabled"
	default:
		return "unknown"
	}
}

// Adapter defines the interface for internal logging
type Adapter interface {
	// Level control
	SetLevel(level Level) Adapter
	GetLevel() Level

	Trace() Event
	Debug() Event
	Info() Event
	Warn() Event
	Error() Event
	Fatal() Event
	Panic() Event

	Printf(format string, v ...interface{})

	// Package-specific logger
	WithPackage(pkg string) Adapter

	// Enabled returns true if logging is enabled for this adapter, false otherwise.
	// This can be used to skip expensive log message construction when logging is disabled.
	Enabled() bool

	Logger() *log.Logger
}

// Field represents a structured log field
type Field struct {
	Key   string
	Value interface{}
}

// F is a helper function to create fields
func F(key string, value interface{}) Field {
	return Field{Key: key, Value: value}
}

// Event represents a log event with a fluent interface
type Event interface {
	// Add fields to the event
	Fields(fields ...Field) Event

	Field(key string, value interface{}) Event

	// Add an error to the event
	Err(err error) Event

	// Log the message
	Msg(msg string)

	// Log the formatted message
	Msgf(format string, v ...interface{})
}
