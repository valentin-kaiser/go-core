package logging

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
	logger zerolog.Logger
	level  Level
}

// NewZerologAdapter creates a new zerolog adapter with the global zerolog logger
func NewZerologAdapter() Adapter {
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

// convertLevel converts our Level to zerolog.Level
func (z *ZerologAdapter) convertLevel(level Level) zerolog.Level {
	switch level {
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
