package logging

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	l "log"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/valentin-kaiser/go-core/apperror"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Field names added by the zerolog adapter.
const (
	fieldCaller  = "caller"
	fieldPackage = "package"
)

// ZerologEvent wraps zerolog.Event to implement our Event interface
type ZerologEvent struct {
	event *zerolog.Event
}

// Fields adds structured fields to the event
func (e *ZerologEvent) Fields(fields ...Field) Event {
	for _, field := range fields {
		e.event = addField(e.event, field.Key, field.Value)
	}
	return e
}

// Field adds a single structured field to the event
func (e *ZerologEvent) Field(key string, value any) Event {
	e.event = addField(e.event, key, value)
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
func (e *ZerologEvent) Msgf(format string, v ...any) {
	e.event.Msgf(format, v...)
}

// zerologState is an immutable snapshot of the logger configuration
type zerologState struct {
	logger zerolog.Logger
	level  Level
}

// derivedAdapter is a WithPackage result together with the state it was derived from
type derivedAdapter struct {
	source  *zerologState
	adapter *ZerologAdapter
}

// ZerologAdapter implements LogAdapter using zerolog
type ZerologAdapter struct {
	// state holds the logger and level. It is replaced as a whole, never modified, so the
	// log call path can read it without a lock while SetLevel and With run concurrently.
	state atomic.Pointer[zerologState]
	// config serializes writers of state, outputs, file and stream
	config  sync.Mutex
	file    *lumberjack.Logger
	stream  *StreamWriter
	outputs []io.Writer
	// packages caches the adapters derived by WithPackage
	packages sync.Map
}

func newZerologAdapter(logger zerolog.Logger, level Level) *ZerologAdapter {
	z := &ZerologAdapter{}
	z.state.Store(&zerologState{logger: logger, level: level})
	return z
}

// NewZerologAdapter creates a new zerolog adapter with the global zerolog logger
func NewZerologAdapter() *ZerologAdapter {
	return newZerologAdapter(log.Logger, InfoLevel)
}

// NewZerologAdapterWithLogger creates a new zerolog adapter with a custom logger
func NewZerologAdapterWithLogger(logger zerolog.Logger) Adapter {
	return newZerologAdapter(logger, InfoLevel)
}

// WithConsole adds a console writer to the logger if in interactive mode
func (z *ZerologAdapter) WithConsole() *ZerologAdapter {
	if Interactive() {
		return z.With(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339, NoColor: !terminal()})
	}
	return z
}

// WithFileRotation adds a file writer with rotation to the logger
func (z *ZerologAdapter) WithFileRotation(name string, size, age, backups int, compress bool) *ZerologAdapter {
	file := &lumberjack.Logger{
		Filename:   name,
		MaxSize:    size,    // megabytes
		MaxAge:     age,     // days
		MaxBackups: backups, // number of backups
		Compress:   compress,
	}
	z.config.Lock()
	z.file = file
	z.config.Unlock()
	return z.With(file)
}

// WithStream adds a stream writer to the logger with the specified max buffer size
func (z *ZerologAdapter) WithStream(maxSize int) *ZerologAdapter {
	stream := NewStreamWriter(maxSize)
	z.config.Lock()
	z.stream = stream
	z.config.Unlock()
	return z.With(stream)
}

// With adds additional output writers to the logger
func (z *ZerologAdapter) With(writers ...io.Writer) *ZerologAdapter {
	z.config.Lock()
	defer z.config.Unlock()
	z.outputs = append(z.outputs, writers...)
	st := z.state.Load()
	z.state.Store(&zerologState{logger: st.logger.Output(newMultiWriter(z.outputs...)), level: st.level})
	return z
}

// Flush flushes all outputs
func (z *ZerologAdapter) Flush() {
	z.config.Lock()
	outputs := append([]io.Writer(nil), z.outputs...)
	z.config.Unlock()
	for _, output := range outputs {
		if flusher, ok := output.(interface{ Flush() error }); ok {
			apperror.Catch(flusher.Flush, "flushing log output failed")
		}
		if syncer, ok := output.(interface{ Sync() error }); ok {
			apperror.Catch(syncer.Sync, "syncing log output failed")
		}
	}
	if file := z.fileLogger(); file != nil {
		apperror.Catch(func() error {
			_, err := file.Write([]byte{})
			return err
		}, "writing log file failed")
	}
}

// Stop flushes and closes the log file if configured
func (z *ZerologAdapter) Stop() {
	z.Flush()
	file := z.fileLogger()
	if file == nil {
		return
	}

	apperror.Catch(file.Close, "closing log file failed")
}

// Rotate rotates the log file manually.
// It creates a new log file and closes the old one.
func (z *ZerologAdapter) Rotate() error {
	file := z.fileLogger()
	if file == nil {
		return apperror.NewError("log file rotation not configured")
	}
	err := file.Rotate()
	if err != nil {
		return apperror.NewError("rotating log file failed").AddError(err)
	}
	return nil
}

// Path returns the log file path if logging to a file
func (z *ZerologAdapter) Path() string {
	if file := z.fileLogger(); file != nil {
		return file.Filename
	}
	return ""
}

// Stream returns the attached stream writer, or nil if none was configured.
func (z *ZerologAdapter) Stream() *StreamWriter {
	z.config.Lock()
	defer z.config.Unlock()
	return z.stream
}

// SetLevel sets the log level
func (z *ZerologAdapter) SetLevel(level Level) Adapter {
	z.config.Lock()
	defer z.config.Unlock()
	st := z.state.Load()
	z.state.Store(&zerologState{logger: st.logger.Level(z.convertLevel(level)), level: level})
	return z
}

// GetLevel returns the current log level
func (z *ZerologAdapter) GetLevel() Level {
	return z.state.Load().level
}

// Trace returns a trace level event
func (z *ZerologAdapter) Trace() Event {
	return z.event(z.state.Load().logger.Trace()) //nolint:zerologlint // returns the event to the caller
}

// Debug returns a debug level event
func (z *ZerologAdapter) Debug() Event {
	return z.event(z.state.Load().logger.Debug()) //nolint:zerologlint // returns the event to the caller
}

// Info returns an info level event
func (z *ZerologAdapter) Info() Event {
	return z.event(z.state.Load().logger.Info()) //nolint:zerologlint // returns the event to the caller
}

// Warn returns a warning level event
func (z *ZerologAdapter) Warn() Event {
	return z.event(z.state.Load().logger.Warn()) //nolint:zerologlint // returns the event to the caller
}

// Error returns an error level event
func (z *ZerologAdapter) Error() Event {
	return z.event(z.state.Load().logger.Error()) //nolint:zerologlint // returns the event to the caller
}

// Fatal returns a fatal level event
func (z *ZerologAdapter) Fatal() Event {
	return z.event(z.state.Load().logger.Fatal()) //nolint:zerologlint // returns the event to the caller
}

// Panic returns a panic level event
func (z *ZerologAdapter) Panic() Event {
	return z.event(z.state.Load().logger.Panic()) //nolint:zerologlint // returns the event to the caller
}

// Printf logs a formatted message using the underlying zerolog logger.
func (z *ZerologAdapter) Printf(format string, v ...any) {
	z.state.Load().logger.Printf(format, v...)
}

// WithPackage returns a new adapter with package name field
func (z *ZerologAdapter) WithPackage(pkg string) Adapter {
	st := z.state.Load()
	if cached, ok := z.packages.Load(pkg); ok {
		if d, ok := cached.(*derivedAdapter); ok && d.source == st {
			return d.adapter
		}
	}
	adapter := newZerologAdapter(st.logger.With().Str(fieldPackage, pkg).Logger(), st.level)
	z.packages.Store(pkg, &derivedAdapter{source: st, adapter: adapter})
	return adapter
}

// Enabled returns whether logging is enabled
func (z *ZerologAdapter) Enabled() bool {
	return z.state.Load().level != DisabledLevel
}

// Logger returns a standard library logger that writes through this adapter.
func (z *ZerologAdapter) Logger() *l.Logger {
	return l.New(z.state.Load().logger, "", 0)
}

// event wraps a zerolog event. zerolog returns nil for a disabled level; that case
// skips the allocation and the caller lookup.
func (z *ZerologAdapter) event(ev *zerolog.Event) Event {
	if ev == nil {
		return &NoOpEvent{}
	}
	e := &ZerologEvent{event: ev}
	if debug.Load() {
		return e.Field(fieldCaller, callerInfo(callerSkipFrames))
	}
	return e
}

func (z *ZerologAdapter) fileLogger() *lumberjack.Logger {
	z.config.Lock()
	defer z.config.Unlock()
	return z.file
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

// addField uses the typed zerolog setters for common types. Interface goes through
// reflection and a JSON marshal, which is several times slower.
func addField(ev *zerolog.Event, key string, value any) *zerolog.Event {
	switch v := value.(type) {
	case string:
		return ev.Str(key, v)
	case int:
		return ev.Int(key, v)
	case int8:
		return ev.Int8(key, v)
	case int16:
		return ev.Int16(key, v)
	case int32:
		return ev.Int32(key, v)
	case int64:
		return ev.Int64(key, v)
	case uint:
		return ev.Uint(key, v)
	case uint8:
		return ev.Uint8(key, v)
	case uint16:
		return ev.Uint16(key, v)
	case uint32:
		return ev.Uint32(key, v)
	case uint64:
		return ev.Uint64(key, v)
	case float32:
		return ev.Float32(key, v)
	case float64:
		return ev.Float64(key, v)
	case bool:
		return ev.Bool(key, v)
	default:
		return ev.Interface(key, value)
	}
}
