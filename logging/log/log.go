// Package log provides a convenient global logger interface that wraps
// the core logging functionality. It offers simplified access to logging
// operations without requiring explicit logger instantiation.
package log

import (
	"github.com/valentin-kaiser/go-core/logging"
)

// Logger returns the current global logger adapter
func Logger() logging.Adapter {
	return global()
}

// SetLevel sets the log level for the global logger
func SetLevel(level logging.Level) {
	global().SetLevel(level)
}

// GetLevel returns the current log level
func GetLevel() logging.Level {
	return global().GetLevel()
}

// Trace returns a trace level event
func Trace() logging.Event {
	return global().Trace()
}

// Debug returns a debug level event
func Debug() logging.Event {
	return global().Debug()
}

// Info returns an info level event
func Info() logging.Event {
	return global().Info()
}

// Warn returns a warning level event
func Warn() logging.Event {
	return global().Warn()
}

// Error returns an error level event
func Error() logging.Event {
	return global().Error()
}

// Fatal returns a fatal level event
func Fatal() logging.Event {
	return global().Fatal()
}

// Panic returns a panic level event
func Panic() logging.Event {
	return global().Panic()
}

// Printf logs a formatted message
func Printf(format string, v ...interface{}) {
	global().Printf(format, v...)
}

// F is a helper function to create fields
func F(key string, value interface{}) logging.Field {
	return logging.F(key, value)
}

func global() logging.Adapter {
	adapter, ok := logging.GetGlobalAdapter[logging.Adapter]()
	if !ok {
		return logging.NewNoOpAdapter()
	}
	return adapter
}
