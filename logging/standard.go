package logging

import (
	"fmt"
	"log"
	"strings"
)

// StandardAdapter implements LogAdapter using Go's standard log package
type StandardAdapter struct {
	logger *log.Logger
	level  Level
	pkg    string
}

// NewStandardAdapter creates a new standard log adapter with the default logger
func NewStandardAdapter() Adapter {
	return &StandardAdapter{
		logger: log.Default(),
		level:  InfoLevel,
	}
}

// NewStandardAdapterWithLogger creates a new standard log adapter with a custom logger
func NewStandardAdapterWithLogger(logger *log.Logger) Adapter {
	return &StandardAdapter{
		logger: logger,
		level:  InfoLevel,
	}
}

// StandardEvent wraps standard log functionality to implement our Event interface
type StandardEvent struct {
	adapter *StandardAdapter
	level   Level
	fields  []Field
	err     error
	caller  string
}

// Fields adds structured fields to the event
func (e *StandardEvent) Fields(fields ...Field) Event {
	e.fields = append(e.fields, fields...)
	return e
}

// Field adds a single field to the event
func (e *StandardEvent) Field(key string, value interface{}) Event {
	e.fields = append(e.fields, Field{Key: key, Value: value})
	return e
}

// Err adds an error to the event
func (e *StandardEvent) Err(err error) Event {
	e.err = err
	return e
}

// Msg logs the message with all accumulated fields
func (e *StandardEvent) Msg(msg string) {
	if !e.shouldLog() {
		return
	}

	logMsg := e.formatMessage(msg)

	switch e.level {
	case FatalLevel:
		e.adapter.logger.Fatal(logMsg)
	case PanicLevel:
		e.adapter.logger.Panic(logMsg)
	default:
		e.adapter.logger.Print(logMsg)
	}
}

// Msgf logs the formatted message with all accumulated fields
func (e *StandardEvent) Msgf(format string, v ...interface{}) {
	e.Msg(fmt.Sprintf(format, v...))
}

// shouldLog checks if the event should be logged based on the level
func (e *StandardEvent) shouldLog() bool {
	return e.level >= e.adapter.level
}

// formatMessage formats the message with level, fields, and error
func (e *StandardEvent) formatMessage(msg string) string {
	var parts []string

	// Add level prefix
	parts = append(parts, fmt.Sprintf("[%s]", strings.ToUpper(e.level.String())))

	if e.caller != "" {
		parts = append(parts, e.caller+" > ")
	}

	// Add the main message
	parts = append(parts, msg)

	// Add package name if set
	if e.adapter.pkg != "" {
		parts = append(parts, "pkg="+e.adapter.pkg)
	}

	// Add fields
	for _, field := range e.fields {
		parts = append(parts, field.Key+"="+fmt.Sprintf("%v", field.Value))
	}

	// Add error if present
	if e.err != nil {
		parts = append(parts, "error="+fmt.Sprintf("%v", e.err))
	}

	return strings.Join(parts, " ")
}

// SetLevel sets the log level
func (s *StandardAdapter) SetLevel(level Level) Adapter {
	s.level = level
	return s
}

// GetLevel returns the current log level
func (s *StandardAdapter) GetLevel() Level {
	return s.level
}

// Trace returns a trace level event
func (s *StandardAdapter) Trace() Event {
	e := &StandardEvent{adapter: s, level: TraceLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Debug returns a debug level event
func (s *StandardAdapter) Debug() Event {
	e := &StandardEvent{adapter: s, level: DebugLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Info returns an info level event
func (s *StandardAdapter) Info() Event {
	e := &StandardEvent{adapter: s, level: InfoLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Warn returns a warning level event
func (s *StandardAdapter) Warn() Event {
	e := &StandardEvent{adapter: s, level: WarnLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Error returns an error level event
func (s *StandardAdapter) Error() Event {
	e := &StandardEvent{adapter: s, level: ErrorLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Fatal returns a fatal level event
func (s *StandardAdapter) Fatal() Event {
	e := &StandardEvent{adapter: s, level: FatalLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Panic returns a panic level event
func (s *StandardAdapter) Panic() Event {
	e := &StandardEvent{adapter: s, level: PanicLevel}
	if debug {
		e.caller = track()
	}
	return e
}

// Printf prints a formatted message
func (s *StandardAdapter) Printf(format string, v ...interface{}) {
	s.logger.Printf(format, v...)
}

// WithPackage returns a new adapter with package name field
func (s *StandardAdapter) WithPackage(pkg string) Adapter {
	return &StandardAdapter{
		logger: s.logger,
		level:  s.level,
		pkg:    pkg,
	}
}

// Enabled returns true if the adapter's log level is not DisabledLevel
func (s *StandardAdapter) Enabled() bool {
	return s.level != DisabledLevel
}

func (s *StandardAdapter) Logger() *log.Logger {
	return s.logger
}
