package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	d "database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
)

var (
	registeredDrivers   = make(map[string]bool)
	registeredDriversMu sync.Mutex
)

// Middleware represents a SQL middleware that can intercept and log SQL statements
type Middleware interface {
	// BeforeExec is called before executing a statement
	BeforeExec(ctx context.Context, query string, args []d.NamedValue) context.Context
	// AfterExec is called after executing a statement
	AfterExec(ctx context.Context, query string, args []d.NamedValue, result d.Result, err error)
	// BeforeQuery is called before executing a query
	BeforeQuery(ctx context.Context, query string, args []d.NamedValue) context.Context
	// AfterQuery is called after executing a query
	AfterQuery(ctx context.Context, query string, args []d.NamedValue, err error)
	// Optionally implement driver.ConnBeginTx if needed
	BeginTx(ctx context.Context, opts d.TxOptions) (d.Tx, error)
}

// driver wraps a driver.Driver to intercept and log SQL statements
type driver struct {
	driver      d.Driver
	middlewares []Middleware
}

// Open returns a new connection to the database with middleware support
func (d *driver) Open(name string) (d.Conn, error) {
	conn, err := d.driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &connection{conn: conn, middlewares: d.middlewares}, nil
}

// connection wraps a driver.Conn to intercept SQL statements
type connection struct {
	conn        d.Conn
	middlewares []Middleware
}

// Prepare returns a prepared statement with middleware support
func (c *connection) Prepare(query string) (d.Stmt, error) {
	stmt, err := c.conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &statement{stmt: stmt, query: query, middlewares: c.middlewares}, nil
}

// Close closes the connection
func (c *connection) Close() error {
	return c.conn.Close()
}

func (c *connection) Begin() (d.Tx, error) {
	return c.BeginTx(context.Background(), d.TxOptions{})
}

// BeginTx starts a transaction with options
func (c *connection) BeginTx(ctx context.Context, opts d.TxOptions) (d.Tx, error) {
	if connBeginTx, ok := c.conn.(d.ConnBeginTx); ok {
		return connBeginTx.BeginTx(ctx, opts)
	}
	return c.conn.Begin()
}

// PrepareContext returns a prepared statement with context and middleware support
func (c *connection) PrepareContext(ctx context.Context, query string) (d.Stmt, error) {
	pCtx, ok := c.conn.(d.ConnPrepareContext)
	if !ok {
		return c.Prepare(query)
	}

	stmt, err := pCtx.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &statement{stmt: stmt, query: query, middlewares: c.middlewares}, nil
}

// ExecContext executes a query with context and middleware support
func (c *connection) ExecContext(ctx context.Context, query string, args []d.NamedValue) (d.Result, error) {
	for _, mw := range c.middlewares {
		ctx = mw.BeforeExec(ctx, query, args)
	}

	var result d.Result
	var err error

	eCtx, ok := c.conn.(d.ExecerContext)
	if ok {
		result, err = eCtx.ExecContext(ctx, query, args)
		for _, mw := range c.middlewares {
			mw.AfterExec(ctx, query, args, result, err)
		}
		return result, err
	}

	stmt, err := c.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	stmtExecCtx, ok := stmt.(d.StmtExecContext)
	if !ok {
		return nil, d.ErrSkip
	}

	return stmtExecCtx.ExecContext(ctx, args)
}

// QueryContext executes a query with context and middleware support
func (c *connection) QueryContext(ctx context.Context, query string, args []d.NamedValue) (d.Rows, error) {
	for _, mw := range c.middlewares {
		ctx = mw.BeforeQuery(ctx, query, args)
	}

	var rows d.Rows
	var err error

	connQueryCtx, ok := c.conn.(d.QueryerContext)
	if ok {
		rows, err = connQueryCtx.QueryContext(ctx, query, args)
		for _, mw := range c.middlewares {
			mw.AfterQuery(ctx, query, args, err)
		}
		return rows, err
	}

	stmt, err := c.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	stmtQueryCtx, ok := stmt.(d.StmtQueryContext)
	if !ok {
		return nil, d.ErrSkip
	}

	return stmtQueryCtx.QueryContext(ctx, args)
}

// Ping verifies the connection is alive
func (c *connection) Ping(ctx context.Context) error {
	pinger, ok := c.conn.(d.Pinger)
	if !ok {
		return nil
	}
	return pinger.Ping(ctx)
}

// statement wraps a driver.Stmt to intercept SQL statements
type statement struct {
	stmt        d.Stmt
	query       string
	middlewares []Middleware
}

// Close closes the statement
func (s *statement) Close() error {
	return s.stmt.Close()
}

// NumInput returns the number of placeholder parameters
func (s *statement) NumInput() int {
	return s.stmt.NumInput()
}

// Exec executes a statement
func (s *statement) Exec(args []d.Value) (d.Result, error) {
	namedArgs := make([]d.NamedValue, len(args))
	for i, v := range args {
		namedArgs[i] = d.NamedValue{Ordinal: i + 1, Value: v}
	}
	return s.ExecContext(context.Background(), namedArgs)
}

// Query executes a query
func (s *statement) Query(args []d.Value) (d.Rows, error) {
	namedArgs := make([]d.NamedValue, len(args))
	for i, v := range args {
		namedArgs[i] = d.NamedValue{Ordinal: i + 1, Value: v}
	}
	return s.QueryContext(context.Background(), namedArgs)
}

// ExecContext executes a statement with context
func (s *statement) ExecContext(ctx context.Context, args []d.NamedValue) (d.Result, error) {
	for _, mw := range s.middlewares {
		ctx = mw.BeforeExec(ctx, s.query, args)
	}

	var result d.Result
	var err error

	stmtExecCtx, ok := s.stmt.(d.StmtExecContext)
	if !ok {
		return nil, d.ErrSkip
	}

	result, err = stmtExecCtx.ExecContext(ctx, args)
	for _, mw := range s.middlewares {
		mw.AfterExec(ctx, s.query, args, result, err)
	}
	return result, err
}

// QueryContext executes a query with context
func (s *statement) QueryContext(ctx context.Context, args []d.NamedValue) (d.Rows, error) {
	for _, mw := range s.middlewares {
		ctx = mw.BeforeQuery(ctx, s.query, args)
	}

	var rows d.Rows
	var err error

	stmtQueryCtx, ok := s.stmt.(d.StmtQueryContext)
	if !ok {
		return nil, d.ErrSkip
	}

	rows, err = stmtQueryCtx.QueryContext(ctx, args)
	for _, mw := range s.middlewares {
		mw.AfterQuery(ctx, s.query, args, err)
	}
	return rows, err
}

// LoggingMiddleware logs all SQL statements and their execution time
type LoggingMiddleware struct {
	logger  logging.Adapter
	enabled atomic.Bool
	trace   atomic.Bool
}

// NewLoggingMiddleware creates a new logging middleware with the provided logger
// Logging is enabled by default
func NewLoggingMiddleware(logger logging.Adapter) *LoggingMiddleware {
	mw := &LoggingMiddleware{logger: logger}
	mw.enabled.Store(true)
	return mw
}

// SetEnabled enables or disables logging at runtime
func (m *LoggingMiddleware) SetEnabled(enabled bool) *LoggingMiddleware {
	m.enabled.Store(enabled)
	return m
}

func (m *LoggingMiddleware) SetTrace(enabled bool) *LoggingMiddleware {
	m.trace.Store(enabled)
	return m
}

// IsEnabled returns true if logging is currently enabled
func (m *LoggingMiddleware) IsEnabled() bool {
	return m.enabled.Load()
}

type contextKey string

const startTimeKey contextKey = "startTime"

// BeforeExec does nothing before executing a statement
func (m *LoggingMiddleware) BeforeExec(ctx context.Context, query string, args []d.NamedValue) context.Context {
	return context.WithValue(ctx, startTimeKey, time.Now())
}

// AfterExec logs after executing a statement
func (m *LoggingMiddleware) AfterExec(ctx context.Context, query string, args []d.NamedValue, result d.Result, err error) {
	if !m.enabled.Load() {
		return
	}
	duration := time.Duration(0)
	if startTime, ok := ctx.Value(startTimeKey).(time.Time); ok {
		duration = time.Since(startTime)
	}

	if err != nil && errors.Is(err, d.ErrSkip) {
		return
	}

	if err != nil {
		l := m.logger.Error().
			Err(err).
			Field("type", "exec").
			Field("args", convertNamedValuesToInterface(args)).
			Field("duration", duration.String())

		if m.trace.Load() {
			l = l.Field("caller", apperror.Where(2))
		}
		l.Msgf("\n%s", query)
		return
	}

	var rowsAffected int64
	if result != nil {
		rowsAffected, _ = result.RowsAffected()
	}
	l := m.logger.Debug().
		Field("type", "exec").
		Field("args", convertNamedValuesToInterface(args)).
		Field("rows_affected", rowsAffected).
		Field("duration", duration.String())
	if m.trace.Load() {
		l = l.Field("caller", apperror.Where(2))
	}
	l.Msgf("\n%s", query)
}

// BeforeQuery does nothing before executing a query
func (m *LoggingMiddleware) BeforeQuery(ctx context.Context, query string, args []d.NamedValue) context.Context {
	return context.WithValue(ctx, startTimeKey, time.Now())
}

// AfterQuery logs after executing a query
func (m *LoggingMiddleware) AfterQuery(ctx context.Context, query string, args []d.NamedValue, err error) {
	if !m.enabled.Load() {
		return
	}
	duration := time.Duration(0)
	if startTime, ok := ctx.Value(startTimeKey).(time.Time); ok {
		duration = time.Since(startTime)
	}

	if err != nil && errors.Is(err, d.ErrSkip) {
		return
	}

	if err != nil {
		l := m.logger.Error().
			Err(err).
			Field("type", "query").
			Field("args", convertNamedValuesToInterface(args)).
			Field("duration", duration.String())
		if m.trace.Load() {
			l = l.Field("caller", apperror.Where(2))
		}
		l.Msgf("\n%s", query)
		return
	}

	l := m.logger.Trace().
		Field("type", "query").
		Field("args", convertNamedValuesToInterface(args)).
		Field("duration", duration.String())
	if m.trace.Load() {
		l = l.Field("caller", apperror.Where(2))
	}
	l.Msgf("\n%s", query)
}

// BeginTx is a no-op implementation to satisfy the Middleware interface
func (m *LoggingMiddleware) BeginTx(ctx context.Context, opts d.TxOptions) (d.Tx, error) {
	return nil, d.ErrSkip
}

// convertNamedValuesToInterface converts driver.NamedValue slice to a more readable format
func convertNamedValuesToInterface(args []d.NamedValue) []interface{} {
	if len(args) == 0 {
		return nil
	}
	result := make([]interface{}, len(args))
	for i, arg := range args {
		result[i] = arg.Value
	}
	return result
}

// wrap wraps a database driver with middleware support
// It computes a stable driver name based on the middleware types to avoid
// registering a new driver on every reconnection
func wrap(driverName string, middlewares []Middleware) string {
	if len(middlewares) == 0 {
		return driverName
	}

	// Compute a hash based on the middleware types to create a stable driver name
	hash := computeMiddlewareHash(middlewares)
	wrappedDriverName := fmt.Sprintf("middleware_%s_%s", driverName, hash)

	registeredDriversMu.Lock()
	defer registeredDriversMu.Unlock()

	// Reuse the driver if already registered
	if registeredDrivers[wrappedDriverName] {
		return wrappedDriverName
	}

	// Get the original driver
	db, err := sql.Open(driverName, "")
	if err != nil {
		return driverName
	}
	db.Close()

	originalDriver := db.Driver()

	// Make a defensive copy of the middlewares slice to prevent shared state issues
	cp := make([]Middleware, len(middlewares))
	copy(cp, middlewares)

	sql.Register(wrappedDriverName, &driver{
		driver:      originalDriver,
		middlewares: cp,
	})

	registeredDrivers[wrappedDriverName] = true
	return wrappedDriverName
}

// computeMiddlewareHash creates a stable hash based on the middleware instances
// This ensures the same middleware instances always produce the same driver name.
// The hash includes both the type name and the pointer address to distinguish
// between different instances of the same middleware type (e.g., two LoggingMiddleware
// instances with different configurations).
func computeMiddlewareHash(middlewares []Middleware) string {
	h := sha256.New()
	for _, mw := range middlewares {
		// Use the type name as part of the hash
		typeName := reflect.TypeOf(mw).String()
		h.Write([]byte(typeName))
		
		// Include the pointer address to distinguish between different instances
		// of the same middleware type with different configurations
		ptr := reflect.ValueOf(mw).Pointer()
		h.Write([]byte(fmt.Sprintf("%d", ptr)))
	}
	// Return first 8 characters of the hex hash for brevity
	return hex.EncodeToString(h.Sum(nil))[:8]
}
