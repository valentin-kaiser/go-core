package logging

import (
	"io"
	"os"
	"time"

	l "log"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valentin-kaiser/go-core/apperror"
	"gopkg.in/natefinch/lumberjack.v2"
)

// ZerologEvent wraps zerolog.Event to implement our Event interface
type ZerologEvent struct {
	event *zerolog.Event
}

// Fields adds structured fields to the event
func (e *ZerologEvent) Fields(fields ...Field) Event {
	for _, field := range fields {
		e.event = e.event.Interface(field.Key, field.Value)
	}
	return e
}

// Field adds a single structured field to the event
func (e *ZerologEvent) Field(key string, value interface{}) Event {
	e.event = e.event.Interface(key, value)
	return e
}

// Err adds an error to the event
func (e *ZerologEvent) Err(err error) Event {
	e.event = e.event.Err(err)
	return e
}

// Msg logs the message
func (e *ZerologEvent) Msg(msg string) {
	e.event.Msg(msg)
}

// Msgf logs the formatted message
func (e *ZerologEvent) Msgf(format string, v ...interface{}) {
	e.event.Msgf(format, v...)
}

// ZerologAdapter implements LogAdapter using zerolog
type ZerologAdapter struct {
	logger  zerolog.Logger
	level   Level
	file    *lumberjack.Logger
	stream  *StreamWriter
	outputs []io.Writer
}

// NewZerologAdapter creates a new zerolog adapter with the global zerolog logger
func NewZerologAdapter() *ZerologAdapter {
	return &ZerologAdapter{
		logger: log.Logger,
		level:  InfoLevel,
	}
}

// NewZerologAdapterWithLogger creates a new zerolog adapter with a custom logger
func NewZerologAdapterWithLogger(logger zerolog.Logger) Adapter {
	return &ZerologAdapter{
		logger: logger,
		level:  InfoLevel,
	}
}

// WithConsole adds a console writer to the logger if in interactive mode
func (z *ZerologAdapter) WithConsole() *ZerologAdapter {
	if Interactive() {
		return z.With(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}
	return z
}

// WithFileRotation adds a file writer with rotation to the logger
func (z *ZerologAdapter) WithFileRotation(name string, size, age, backups int, compress bool) *ZerologAdapter {
	z.file = &lumberjack.Logger{
		Filename:   name,
		MaxSize:    size,    // megabytes
		MaxAge:     age,     // days
		MaxBackups: backups, // number of backups
		Compress:   compress,
	}
	return z.With(z.file)
}

// WithStream adds a stream writer to the logger with the specified max buffer size
func (z *ZerologAdapter) WithStream(max int) *ZerologAdapter {
	z.stream = NewStreamWriter(max)
	return z.With(z.stream)
}

// With adds additional output writers to the logger
func (z *ZerologAdapter) With(writers ...io.Writer) *ZerologAdapter {
	z.outputs = append(z.outputs, writers...)
	z.logger = z.logger.Output(newMultiWriter(z.outputs...))
	return z
}

// Flush flushes all outputs
func (z *ZerologAdapter) Flush() {
	for _, output := range z.outputs {
		if flusher, ok := output.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
		if syncer, ok := output.(interface{ Sync() error }); ok {
			_ = syncer.Sync()
		}
	}
	if z.file != nil {
		_, _ = z.file.Write([]byte{})
	}
}

// Stop flushes and closes the log file if configured
func (z *ZerologAdapter) Stop() {
	z.Flush()
	if z.file == nil {
		return
	}

	err := z.file.Close()
	if err != nil {
		log.Error().Err(err).Msgf("failed to close log file")
	}
}

// Rotate rotates the log file manually.
// It creates a new log file and closes the old one.
func (z *ZerologAdapter) Rotate() error {
	if z.file == nil {
		return apperror.NewError("log file rotation not configured")
	}
	err := z.file.Rotate()
	if err != nil {
		return apperror.NewError("failed to rotate log file").AddError(err)
	}
	return nil
}

// GetPath returns the log file path if logging to a file
func (z *ZerologAdapter) Path() string {
	if z.file != nil {
		return z.file.Filename
	}
	return ""
}

func (z *ZerologAdapter) Stream() *StreamWriter {
	return z.stream
}

// SetLevel sets the log level
func (z *ZerologAdapter) SetLevel(level Level) Adapter {
	z.level = level
	z.logger = z.logger.Level(z.convertLevel(level))
	return z
}

// GetLevel returns the current log level
func (z *ZerologAdapter) GetLevel() Level {
	return z.level
}

// Trace returns a trace level event
func (z *ZerologAdapter) Trace() Event {
	e := &ZerologEvent{event: z.logger.Trace()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Debug returns a debug level event
func (z *ZerologAdapter) Debug() Event {
	e := &ZerologEvent{event: z.logger.Debug()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Info returns an info level event
func (z *ZerologAdapter) Info() Event {
	e := &ZerologEvent{event: z.logger.Info()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Warn returns a warning level event
func (z *ZerologAdapter) Warn() Event {
	e := &ZerologEvent{event: z.logger.Warn()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Error returns an error level event
func (z *ZerologAdapter) Error() Event {
	e := &ZerologEvent{event: z.logger.Error()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Fatal returns a fatal level event
func (z *ZerologAdapter) Fatal() Event {
	e := &ZerologEvent{event: z.logger.Fatal()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Panic returns a panic level event
func (z *ZerologAdapter) Panic() Event {
	e := &ZerologEvent{event: z.logger.Panic()}
	if debug {
		return e.Field("caller", track())
	}
	return e
}

// Printf logs a formatted message using the underlying zerolog logger.
func (z *ZerologAdapter) Printf(format string, v ...interface{}) {
	z.logger.Printf(format, v...)
}

// WithPackage returns a new adapter with package name field
func (z *ZerologAdapter) WithPackage(pkg string) Adapter {
	return &ZerologAdapter{
		logger: z.logger.With().Str("package", pkg).Logger(),
		level:  z.level,
	}
}

// Enabled returns whether logging is enabled
func (z *ZerologAdapter) Enabled() bool {
	return z.level != DisabledLevel
}

func (z *ZerologAdapter) Logger() *l.Logger {
	return l.New(z.logger, "", 0)
}

// convertLevel converts our Level to zerolog.Level
func (z *ZerologAdapter) convertLevel(level Level) zerolog.Level {
	if level < VerboseLevel {
		return zerolog.TraceLevel
	}

	switch level {
	case VerboseLevel:
		return zerolog.TraceLevel
	case TraceLevel:
		return zerolog.TraceLevel
	case DebugLevel:
		return zerolog.DebugLevel
	case InfoLevel:
		return zerolog.InfoLevel
	case WarnLevel:
		return zerolog.WarnLevel
	case ErrorLevel:
		return zerolog.ErrorLevel
	case FatalLevel:
		return zerolog.FatalLevel
	case PanicLevel:
		return zerolog.PanicLevel
	case DisabledLevel:
		return zerolog.Disabled
	default:
		return zerolog.InfoLevel
	}
}
