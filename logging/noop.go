package logging

// NoOpEvent implements Event interface but does nothing
type NoOpEvent struct{}

// Fields does nothing and returns itself for chaining
func (e *NoOpEvent) Fields(_ ...Field) Event {
	return e
}

// Field does nothing and returns itself for chaining
func (e *NoOpEvent) Field(_ string, _ interface{}) Event {
	return e
}

// Err does nothing and returns itself for chaining
func (e *NoOpEvent) Err(_ error) Event {
	return e
}

// Msg does nothing
func (e *NoOpEvent) Msg(_ string) {}

// Msgf does nothing
func (e *NoOpEvent) Msgf(_ string, _ ...interface{}) {}

// NoOpAdapter implements LogAdapter but does nothing
// This is the default implementation for minimal overhead when logging is disabled
type NoOpAdapter struct{}

// NewNoOpAdapter creates a new no-op adapter
func NewNoOpAdapter() Adapter {
	return &NoOpAdapter{}
}

// SetLevel sets the log level (no-op)
func (n *NoOpAdapter) SetLevel(_ Level) Adapter {
	return n
}

// GetLevel returns the current log level
func (n *NoOpAdapter) GetLevel() Level {
	return DisabledLevel
}

// Trace returns a no-op event
func (n *NoOpAdapter) Trace() Event {
	return &NoOpEvent{}
}

// Debug returns a no-op event
func (n *NoOpAdapter) Debug() Event {
	return &NoOpEvent{}
}

// Info returns a no-op event
func (n *NoOpAdapter) Info() Event {
	return &NoOpEvent{}
}

// Warn returns a no-op event
func (n *NoOpAdapter) Warn() Event {
	return &NoOpEvent{}
}

// Error returns a no-op event
func (n *NoOpAdapter) Error() Event {
	return &NoOpEvent{}
}

// Fatal returns a no-op event
func (n *NoOpAdapter) Fatal() Event {
	return &NoOpEvent{}
}

// Panic returns a no-op event
func (n *NoOpAdapter) Panic() Event {
	return &NoOpEvent{}
}

// Printf does nothing
func (n *NoOpAdapter) Printf(_ string, _ ...interface{}) {}

// WithPackage returns the same no-op adapter
func (n *NoOpAdapter) WithPackage(_ string) Adapter {
	return n
}

// Enabled returns false indicating logging is disabled
func (n *NoOpAdapter) Enabled() bool {
	return false
}
