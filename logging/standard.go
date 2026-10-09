package logging

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
)

// StandardAdapter implements LogAdapter using Go's standard log package
type StandardAdapter struct {
	logger *log.Logger
	level  atomic.Int64
	pkg    string
}

func newStandardAdapter(logger *log.Logger, level Level, pkg string) *StandardAdapter {
	s := &StandardAdapter{logger: logger, pkg: pkg}
	s.level.Store(int64(level))
	return s
}

// NewStandardAdapter creates a new standard log adapter with the default logger
func NewStandardAdapter() Adapter {
	return newStandardAdapter(log.Default(), InfoLevel, "")
}

// NewStandardAdapterWithLogger creates a new standard log adapter with a custom logger
func NewStandardAdapterWithLogger(logger *log.Logger) Adapter {
	return newStandardAdapter(logger, InfoLevel, "")
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
	if !e.shouldLog() {
		return
	}
	e.Msg(fmt.Sprintf(format, v...))
}

// shouldLog checks if the event should be logged based on the level
func (e *StandardEvent) shouldLog() bool {
	return int64(e.level) >= e.adapter.level.Load()
}

// formatMessage formats the message with level, fields, and error
func (e *StandardEvent) formatMessage(msg string) string {
	var b strings.Builder
	b.Grow(len(msg) + 48 + 24*len(e.fields))

	// Level prefix
	b.WriteByte('[')
	b.WriteString(upperLevelName(e.level))
	b.WriteByte(']')

	if e.caller != "" {
		b.WriteByte(' ')
		b.WriteString(e.caller)
		b.WriteString(" > ")
	}

	// The main message
	b.WriteByte(' ')
	b.WriteString(msg)

	// Package name if set
	if e.adapter.pkg != "" {
		b.WriteString(" pkg=")
		b.WriteString(e.adapter.pkg)
	}

	for _, field := range e.fields {
		b.WriteByte(' ')
		b.WriteString(field.Key)
		b.WriteByte('=')
		writeValue(&b, field.Value)
	}

	if e.err != nil {
		b.WriteString(" error=")
		writeValue(&b, e.err)
	}

	return b.String()
}

// upperLevelName returns the upper case name of the level without allocating
func upperLevelName(l Level) string {
	switch l {
	case VerboseLevel:
		return "VERBOSE"
	case TraceLevel:
		return "TRACE"
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	case PanicLevel:
		return "PANIC"
	case DisabledLevel:
		return "DISABLED"
	default:
		return "UNKNOWN"
	}
}

// writeValue writes the value like fmt.Sprintf("%v") would, without going through fmt for
// the common types
func writeValue(b *strings.Builder, v interface{}) {
	var scratch [32]byte
	switch x := v.(type) {
	case string:
		b.WriteString(x)
	case int:
		b.Write(strconv.AppendInt(scratch[:0], int64(x), 10))
	case int64:
		b.Write(strconv.AppendInt(scratch[:0], x, 10))
	case int32:
		b.Write(strconv.AppendInt(scratch[:0], int64(x), 10))
	case uint:
		b.Write(strconv.AppendUint(scratch[:0], uint64(x), 10))
	case uint64:
		b.Write(strconv.AppendUint(scratch[:0], x, 10))
	case uint32:
		b.Write(strconv.AppendUint(scratch[:0], uint64(x), 10))
	case bool:
		b.Write(strconv.AppendBool(scratch[:0], x))
	case float64:
		b.Write(strconv.AppendFloat(scratch[:0], x, 'g', -1, 64))
	case error:
		b.WriteString(x.Error())
	default:
		fmt.Fprintf(b, "%v", v)
	}
}

// SetLevel sets the log level
func (s *StandardAdapter) SetLevel(level Level) Adapter {
	s.level.Store(int64(level))
	return s
}

// GetLevel returns the current log level
func (s *StandardAdapter) GetLevel() Level {
	return Level(s.level.Load())
}

// event creates an event for the level. The caller lookup is skipped for levels that will not be logged.
func (s *StandardAdapter) event(level Level) Event {
	e := &StandardEvent{adapter: s, level: level}
	if debug.Load() && e.shouldLog() {
		e.caller = callerInfo(4)
	}
	return e
}

// Trace returns a trace level event
func (s *StandardAdapter) Trace() Event {
	return s.event(TraceLevel)
}

// Debug returns a debug level event
func (s *StandardAdapter) Debug() Event {
	return s.event(DebugLevel)
}

// Info returns an info level event
func (s *StandardAdapter) Info() Event {
	return s.event(InfoLevel)
}

// Warn returns a warning level event
func (s *StandardAdapter) Warn() Event {
	return s.event(WarnLevel)
}

// Error returns an error level event
func (s *StandardAdapter) Error() Event {
	return s.event(ErrorLevel)
}

// Fatal returns a fatal level event
func (s *StandardAdapter) Fatal() Event {
	return s.event(FatalLevel)
}

// Panic returns a panic level event
func (s *StandardAdapter) Panic() Event {
	return s.event(PanicLevel)
}

// Printf prints a formatted message
func (s *StandardAdapter) Printf(format string, v ...interface{}) {
	s.logger.Printf(format, v...)
}

// WithPackage returns a new adapter with package name field
func (s *StandardAdapter) WithPackage(pkg string) Adapter {
	return newStandardAdapter(s.logger, Level(s.level.Load()), pkg)
}

// Enabled returns true if the adapter's log level is not DisabledLevel
func (s *StandardAdapter) Enabled() bool {
	return Level(s.level.Load()) != DisabledLevel
}

func (s *StandardAdapter) Logger() *log.Logger {
	return s.logger
}
