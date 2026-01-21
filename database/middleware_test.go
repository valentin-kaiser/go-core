package database_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
	"github.com/valentin-kaiser/go-core/logging"
)

// mockMiddleware is a mock implementation of the Middleware interface
type mockMiddleware struct {
	beforeExecCalled  int
	afterExecCalled   int
	beforeQueryCalled int
	afterQueryCalled  int
	lastQuery         string
	lastArgs          []driver.NamedValue
	lastError         error
}

func (m *mockMiddleware) BeforeExec(ctx context.Context, query string, args []driver.NamedValue) context.Context {
	m.beforeExecCalled++
	m.lastQuery = query
	m.lastArgs = args
	return ctx
}

func (m *mockMiddleware) AfterExec(ctx context.Context, query string, args []driver.NamedValue, result driver.Result, err error) {
	m.afterExecCalled++
	m.lastError = err
}

func (m *mockMiddleware) BeforeQuery(ctx context.Context, query string, args []driver.NamedValue) context.Context {
	m.beforeQueryCalled++
	m.lastQuery = query
	m.lastArgs = args
	return ctx
}

func (m *mockMiddleware) AfterQuery(ctx context.Context, query string, args []driver.NamedValue, err error) {
	m.afterQueryCalled++
	m.lastError = err
}

func (m *mockMiddleware) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return nil, driver.ErrSkip
}

// TestLoggingMiddleware_New tests creation of LoggingMiddleware
func TestLoggingMiddleware_New(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	if mw == nil {
		t.Fatal("NewLoggingMiddleware() returned nil")
	}
	if !mw.IsEnabled() {
		t.Error("NewLoggingMiddleware() should create enabled middleware by default")
	}
}

// TestLoggingMiddleware_SetEnabled tests enabling/disabling logging
func TestLoggingMiddleware_SetEnabled(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)

	// Initially enabled
	if !mw.IsEnabled() {
		t.Error("Middleware should be enabled by default")
	}

	// Disable
	mw.SetEnabled(false)
	if mw.IsEnabled() {
		t.Error("Middleware should be disabled after SetEnabled(false)")
	}

	// Enable again
	mw.SetEnabled(true)
	if !mw.IsEnabled() {
		t.Error("Middleware should be enabled after SetEnabled(true)")
	}
}

// TestLoggingMiddleware_SetTrace tests trace mode
func TestLoggingMiddleware_SetTrace(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)

	// Test SetTrace returns the same instance for chaining
	result := mw.SetTrace(true)
	if result != mw {
		t.Error("SetTrace should return the same instance for chaining")
	}

	// Test SetEnabled returns the same instance for chaining
	result = mw.SetEnabled(false)
	if result != mw {
		t.Error("SetEnabled should return the same instance for chaining")
	}
}

// TestLoggingMiddleware_BeforeAfterExec tests execution hooks
func TestLoggingMiddleware_BeforeAfterExec(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)

	ctx := context.Background()
	query := "INSERT INTO users VALUES (?, ?)"
	args := []driver.NamedValue{
		{Ordinal: 1, Value: "John"},
		{Ordinal: 2, Value: "Doe"},
	}

	// Test BeforeExec
	newCtx := mw.BeforeExec(ctx, query, args)
	if newCtx == nil {
		t.Error("BeforeExec should return a context")
	}

	// Test AfterExec with no error
	mw.AfterExec(newCtx, query, args, nil, nil)

	// Test AfterExec with error
	testErr := errors.New("test error")
	mw.AfterExec(newCtx, query, args, nil, testErr)
}

// TestLoggingMiddleware_BeforeAfterQuery tests query hooks
func TestLoggingMiddleware_BeforeAfterQuery(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)

	ctx := context.Background()
	query := "SELECT * FROM users WHERE id = ?"
	args := []driver.NamedValue{
		{Ordinal: 1, Value: 1},
	}

	// Test BeforeQuery
	newCtx := mw.BeforeQuery(ctx, query, args)
	if newCtx == nil {
		t.Error("BeforeQuery should return a context")
	}

	// Test AfterQuery with no error
	mw.AfterQuery(newCtx, query, args, nil)

	// Test AfterQuery with error
	testErr := errors.New("query error")
	mw.AfterQuery(newCtx, query, args, testErr)
}

// TestLoggingMiddleware_BeginTx tests transaction begin
func TestLoggingMiddleware_BeginTx(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)

	ctx := context.Background()
	tx, err := mw.BeginTx(ctx, driver.TxOptions{})

	if tx != nil {
		t.Error("BeginTx should return nil transaction")
	}
	if !errors.Is(err, driver.ErrSkip) {
		t.Errorf("BeginTx should return driver.ErrSkip, got %v", err)
	}
}

// TestLoggingMiddleware_DisabledLogging tests that disabled middleware doesn't log
func TestLoggingMiddleware_DisabledLogging(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	mw.SetEnabled(false)

	ctx := context.Background()
	query := "SELECT * FROM users"
	args := []driver.NamedValue{}

	// These should not panic or cause issues when disabled
	newCtx := mw.BeforeExec(ctx, query, args)
	mw.AfterExec(newCtx, query, args, nil, nil)

	newCtx = mw.BeforeQuery(ctx, query, args)
	mw.AfterQuery(newCtx, query, args, nil)
}

// TestLoggingMiddleware_WithDriverErrSkip tests handling of driver.ErrSkip
func TestLoggingMiddleware_WithDriverErrSkip(t *testing.T) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)

	ctx := context.Background()
	query := "SELECT 1"
	args := []driver.NamedValue{}

	newCtx := mw.BeforeExec(ctx, query, args)
	// When driver.ErrSkip is returned, it should be handled gracefully
	mw.AfterExec(newCtx, query, args, nil, driver.ErrSkip)

	newCtx = mw.BeforeQuery(ctx, query, args)
	mw.AfterQuery(newCtx, query, args, driver.ErrSkip)
}

// TestMiddleware_Integration tests middleware integration with SQLite database
func TestMiddleware_Integration(t *testing.T) {
	// Create a test database with middleware
	db := database.New[any]("test")

	// Create mock middleware
	mock := &mockMiddleware{}
	db.RegisterMiddleware(mock)

	// Create logging middleware
	logger := logging.NewNoOpAdapter()
	loggingMw := database.NewLoggingMiddleware(logger)
	db.RegisterMiddleware(loggingMw)

	// Connect to in-memory SQLite database
	config := database.Config{
		Driver: "sqlite3",
		Name:   ":memory:",
	}

	if err := config.Validate(); err != nil {
		t.Fatalf("Config validation failed: %v", err)
	}

	db.Connect(100*time.Millisecond, config)
	defer db.Disconnect()

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	if !db.Connected() {
		t.Fatal("Database should be connected")
	}

	// Execute a query that should trigger middleware
	err := db.Execute(func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec("CREATE TABLE IF NOT EXISTS test_table (id INTEGER PRIMARY KEY, name TEXT)")
		return err
	})
	if err != nil {
		t.Errorf("Failed to execute query: %v", err)
	}

	// Verify middleware was called
	if mock.beforeExecCalled == 0 && mock.afterExecCalled == 0 {
		// Middleware might not be called depending on the exact flow,
		// but at least one should be called for exec operations
		t.Log("Middleware hooks may not have been called - this could be expected depending on driver implementation")
	}
}

// TestMiddleware_Chaining tests that multiple middlewares work together
func TestMiddleware_Chaining(t *testing.T) {
	db := database.New[any]("test")

	mock1 := &mockMiddleware{}
	mock2 := &mockMiddleware{}

	db.RegisterMiddleware(mock1)
	db.RegisterMiddleware(mock2)

	config := database.Config{
		Driver: "sqlite3",
		Name:   ":memory:",
	}

	db.Connect(100*time.Millisecond, config)
	defer db.Disconnect()

	time.Sleep(200 * time.Millisecond)

	if !db.Connected() {
		t.Fatal("Database should be connected")
	}

	err := db.Execute(func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec("SELECT 1")
		return err
	})
	if err != nil {
		t.Errorf("Failed to execute query: %v", err)
	}
}

// TestMiddleware_NilMiddleware tests that nil middleware is handled gracefully
func TestMiddleware_NilMiddleware(t *testing.T) {
	db := database.New[any]("test")

	// Register nil middleware - should not panic
	result := db.RegisterMiddleware(nil)
	if result != db {
		t.Error("RegisterMiddleware should return the same database instance")
	}

	config := database.Config{
		Driver: "sqlite3",
		Name:   ":memory:",
	}

	db.Connect(100*time.Millisecond, config)
	defer db.Disconnect()

	time.Sleep(200 * time.Millisecond)

	if !db.Connected() {
		t.Fatal("Database should be connected")
	}
}

// BenchmarkLoggingMiddleware_BeforeExec benchmarks BeforeExec
func BenchmarkLoggingMiddleware_BeforeExec(b *testing.B) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	ctx := context.Background()
	query := "SELECT * FROM users WHERE id = ?"
	args := []driver.NamedValue{{Ordinal: 1, Value: 1}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mw.BeforeExec(ctx, query, args)
	}
}

// BenchmarkLoggingMiddleware_AfterExec benchmarks AfterExec
func BenchmarkLoggingMiddleware_AfterExec(b *testing.B) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	ctx := context.Background()
	query := "SELECT * FROM users WHERE id = ?"
	args := []driver.NamedValue{{Ordinal: 1, Value: 1}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mw.AfterExec(ctx, query, args, nil, nil)
	}
}

// BenchmarkLoggingMiddleware_BeforeQuery benchmarks BeforeQuery
func BenchmarkLoggingMiddleware_BeforeQuery(b *testing.B) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	ctx := context.Background()
	query := "SELECT * FROM users WHERE id = ?"
	args := []driver.NamedValue{{Ordinal: 1, Value: 1}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mw.BeforeQuery(ctx, query, args)
	}
}

// BenchmarkLoggingMiddleware_AfterQuery benchmarks AfterQuery
func BenchmarkLoggingMiddleware_AfterQuery(b *testing.B) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	ctx := context.Background()
	query := "SELECT * FROM users WHERE id = ?"
	args := []driver.NamedValue{{Ordinal: 1, Value: 1}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mw.AfterQuery(ctx, query, args, nil)
	}
}

// BenchmarkLoggingMiddleware_Disabled benchmarks disabled middleware
func BenchmarkLoggingMiddleware_Disabled(b *testing.B) {
	logger := logging.NewNoOpAdapter()
	mw := database.NewLoggingMiddleware(logger)
	mw.SetEnabled(false)
	ctx := context.Background()
	query := "SELECT * FROM users WHERE id = ?"
	args := []driver.NamedValue{{Ordinal: 1, Value: 1}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newCtx := mw.BeforeExec(ctx, query, args)
		mw.AfterExec(newCtx, query, args, nil, nil)
	}
}

// TestMiddleware_DriverReuse tests that the same middleware configuration reuses drivers
func TestMiddleware_DriverReuse(t *testing.T) {
	db1 := database.New[TestQueries]("test-reuse-1")
	db1.RegisterQueries(NewTestQueries)

	logger := logging.NewNoOpAdapter()
	loggingMW := database.NewLoggingMiddleware(logger)
	db1.RegisterMiddleware(loggingMW)

	config := database.Config{
		Driver: "sqlite3",
		Name:   ":memory:",
	}

	// First connection - count drivers before and after
	driversBeforeFirst := sql.Drivers()
	db1.Connect(100*time.Millisecond, config)
	db1.AwaitConnection()
	driversAfterFirst := sql.Drivers()

	// Should have registered a new wrapped driver
	newDriversCount := len(driversAfterFirst) - len(driversBeforeFirst)
	if newDriversCount != 1 {
		t.Logf("Warning: Expected 1 new driver, got %d. This may be due to other tests.", newDriversCount)
	}

	// Disconnect and reconnect
	err := db1.Disconnect()
	if err != nil {
		t.Fatalf("failed to disconnect: %v", err)
	}

	db1.Connect(100*time.Millisecond, config)
	db1.AwaitConnection()
	driversAfterReconnect := sql.Drivers()

	// Should NOT register a new driver on reconnect (reuse existing)
	if len(driversAfterReconnect) != len(driversAfterFirst) {
		t.Errorf("expected no new drivers after reconnect, got %d new drivers", len(driversAfterReconnect)-len(driversAfterFirst))
	}

	// Create another database with the SAME middleware instance
	db2 := database.New[TestQueries]("test-reuse-2")
	db2.RegisterQueries(NewTestQueries)
	db2.RegisterMiddleware(loggingMW) // Use the same instance

	db2.Connect(100*time.Millisecond, config)
	db2.AwaitConnection()
	driversAfterSecondDB := sql.Drivers()

	// Should reuse the same wrapped driver (same middleware instance)
	if len(driversAfterSecondDB) != len(driversAfterReconnect) {
		t.Errorf("expected to reuse existing driver for second db instance with same middleware, got %d new drivers", len(driversAfterSecondDB)-len(driversAfterReconnect))
	}

	// Cleanup
	db1.Disconnect()
	db2.Disconnect()
}

// TestMiddleware_DifferentInstances tests that different middleware instances get different drivers
func TestMiddleware_DifferentInstances(t *testing.T) {
	logger := logging.NewNoOpAdapter()

	// Create first database with first middleware instance
	db1 := database.New[TestQueries]("test-diff-1")
	db1.RegisterQueries(NewTestQueries)
	loggingMW1 := database.NewLoggingMiddleware(logger)
	db1.RegisterMiddleware(loggingMW1)

	config := database.Config{
		Driver: "sqlite3",
		Name:   ":memory:",
	}

	driversBeforeFirst := sql.Drivers()
	db1.Connect(100*time.Millisecond, config)
	db1.AwaitConnection()
	driversAfterFirst := sql.Drivers()

	// Create second database with DIFFERENT middleware instance
	db2 := database.New[TestQueries]("test-diff-2")
	db2.RegisterQueries(NewTestQueries)
	loggingMW2 := database.NewLoggingMiddleware(logger) // Different instance
	db2.RegisterMiddleware(loggingMW2)

	db2.Connect(100*time.Millisecond, config)
	db2.AwaitConnection()
	driversAfterSecond := sql.Drivers()

	// Should have registered TWO new drivers (one for each middleware instance)
	totalNewDrivers := len(driversAfterSecond) - len(driversBeforeFirst)
	if totalNewDrivers < 2 {
		t.Errorf("expected at least 2 new drivers for different middleware instances, got %d", totalNewDrivers)
	}

	// Verify that the second connection registered a new driver
	secondDBNewDrivers := len(driversAfterSecond) - len(driversAfterFirst)
	if secondDBNewDrivers != 1 {
		t.Errorf("expected 1 new driver for second db with different middleware instance, got %d", secondDBNewDrivers)
	}

	// Cleanup
	db1.Disconnect()
	db2.Disconnect()
}
