// Package database provides a robust and flexible abstraction over database/sql,
// supporting SQLite, MySQL/MariaDB, and PostgreSQL as backend databases.
//
// The package uses an instance-based design, allowing applications to manage multiple
// database connections simultaneously. Each Database instance maintains its own connection
// state, configuration, and migration history.
//
// It offers features such as automatic connection handling, schema migrations,
// and version tracking. The package is designed to work with sqlc for type-safe SQL queries.
// It also allows registering custom on-connect handlers that are executed once the database
// connection is successfully established.
//
// Core features:
//
//   - Multiple database instances with independent connections
//   - Automatic (re)connection with health checks and retry mechanism
//   - Support for SQLite, MySQL/MariaDB, and PostgreSQL with configurable parameters
//   - Schema management with SQL-based migrations
//   - Versioning support using the go-core/version package
//   - Connection lifecycle management (Connect, Disconnect, AwaitConnection)
//   - Thread-safe access with `GetDB()` to retrieve the active connection
//   - Registering on-connect hooks to perform actions like seeding or setup
//   - Backup and restore functionality for SQLite, MySQL, and PostgreSQL
//   - SQL middleware support for logging and monitoring all database operations
//   - Debug mode for per-query logging (similar to GORM's Debug() method)
//
// Example:
//
//	package main
//
//	import (
//		"context"
//		"database/sql"
//		"fmt"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/database"
//		"github.com/valentin-kaiser/go-core/flag"
//		"github.com/valentin-kaiser/go-core/version"
//		"your-project/internal/sqlc"
//	)
//
//	func main() {
//		flag.Init()
//
//		// Create a new database instance with sqlc integration
//		db := database.New[sqlc.Queries](database.DriverSQLite, "main")
//
//		// Register queries constructor
//		db.RegisterQueries(sqlc.New)
//
//		// Register migration steps for this instance
//		db.RegisterMigrationStep(version.Release{
//			GitTag:    "v1.0.0",
//			GitCommit: "abc123",
//		}, func(sqlDB *sql.DB) error {
//			_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS users (
//				id INTEGER PRIMARY KEY AUTOINCREMENT,
//				name TEXT NOT NULL,
//				email TEXT UNIQUE NOT NULL,
//				password TEXT NOT NULL,
//				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
//			)`)
//			return err
//		})
//
//		// Connect to the database using DSN string
//		db.Connect(time.Second, "file:test.db")
//		defer db.Disconnect()
//
//		// Wait for connection to be established
//		db.AwaitConnection()
//
//		// Use Query method to execute type-safe sqlc queries
//		ctx := context.Background()
//		err := db.Query(func(q *sqlc.Queries) error {
//			user, err := q.GetUser(ctx, 1)
//			if err != nil {
//				return err
//			}
//			fmt.Println("User:", user.Name, user.Email)
//			return nil
//		})
//		if err != nil {
//			panic(err)
//		}
//	}
//
// Multi-Instance Example:
//
//	// Connect to multiple databases simultaneously with different sqlc Queries
//	postgres := database.New[pgSqlc.Queries](database.DriverPostgres, "postgres-main")
//	mysql := database.New[mysqlSqlc.Queries](database.DriverMySQL, "mysql-analytics")
//	sqlite := database.New[sqliteSqlc.Queries](database.DriverSQLite, "sqlite-cache")
//
//	postgres.Connect(time.Second, "postgres://postgres:secret@localhost:5432/maindb?sslmode=disable")
//
//	mysql.Connect(time.Second, "root:password@tcp(localhost:3306)/analytics")
//
//	sqlite.Connect(time.Second, ":memory:")
//
// Middleware Example:
//
//	// Create a new database instance with logging middleware
//	db := database.New[sqlc.Queries](database.DriverSQLite, "main")
//
//	// Register logging middleware to log all SQL statements
//	logger := logging.GetPackageLogger("database")
//	loggingMiddleware := database.NewLoggingMiddleware(logger)
//	db.RegisterMiddleware(loggingMiddleware)
//
//	// Connect to the database using DSN string
//	db.Connect(time.Second, "file:example.db")
//	defer db.Disconnect()
//
//	// All SQL statements will now be logged with timing information
//	ctx := context.Background()
//	db.Query(func(q *sqlc.Queries) error {
//		return q.CreateUser(ctx, sqlc.CreateUserParams{
//			Name:  "John Doe",
//			Email: "john@example.com",
//		})
//	})
//
// Debug Mode Example:
//
//	// Create a database instance and register LoggingMiddleware
//	db := database.New[sqlc.Queries](database.DriverSQLite, "main")
//
//	// LoggingMiddleware must be registered for Debug() to work
//	logger := logging.GetPackageLogger("database")
//	loggingMiddleware := database.NewLoggingMiddleware(logger)
//	loggingMiddleware.SetEnabled(false) // Disabled by default
//	db.RegisterMiddleware(loggingMiddleware)
//
//	db.Connect(time.Second, "file:example.db")
//	defer db.Disconnect()
//
//	ctx := context.Background()
//
//	// Regular query - no debug logging (middleware is disabled)
//	db.Query(func(q *sqlc.Queries) error {
//		return q.GetUser(ctx, 1)
//	})
//
//	// Debug query - temporarily enables logging for this specific query
//	db.Debug().Query(func(q *sqlc.Queries) error {
//		return q.GetUser(ctx, 1)
//	})
//
//	// You can also use Debug() with Execute
//	db.Debug().Execute(func(dbConn *sql.DB) error {
//		_, err := dbConn.ExecContext(ctx, "UPDATE users SET active = ? WHERE id = ?", true, 1)
//		return err
//	})
package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
)

type Driver string

const (
	DriverSQLite   Driver = "sqlite3"
	DriverMySQL    Driver = "mysql"
	DriverPostgres Driver = "pgx"
)

// Database represents a database connection instance with its own state and configuration.
// Multiple instances can be created to manage connections to different databases.
// The generic type parameter Q represents the sqlc-generated Queries type.
type Database[Q any] struct {
	db               *sql.DB
	dbMutex          sync.RWMutex
	driver           Driver
	dsn              string
	connected        atomic.Bool
	failed           atomic.Bool
	cancel           atomic.Bool
	done             chan bool
	onConnectHandler []func(db *sql.DB) error
	handlerMutex     sync.Mutex
	logger           logging.Adapter
	middlewares      []Middleware
	middlewareMutex  sync.RWMutex
	queries          any
	queriesFunc      func(DBTX) *Q
	queriesArg       atomic.Pointer[queriesArg]
	debug            bool
	parent           *Database[Q]
}

// New creates a new Database instance with the given name for logging purposes.
// The name parameter is used to identify this database instance in logs.
// The generic type parameter Q represents the sqlc-generated Queries type.
func New[Q any](driver Driver, name string) *Database[Q] {
	return &Database[Q]{
		driver:           driver,
		done:             make(chan bool),
		logger:           logging.GetPackageLogger("database:" + name),
		onConnectHandler: make([]func(db *sql.DB) error, 0),
		middlewares:      make([]Middleware, 0),
	}
}

// Get returns the active database connection.
// It will return nil if the database is not connected.
// Use this function to get the database connection for executing queries with sqlc or raw SQL.
func (d *Database[Q]) Get() *sql.DB {
	d.dbMutex.RLock()
	defer d.dbMutex.RUnlock()
	return d.db
}

// Debug returns a new Database handle with debug logging enabled for all queries and executions.
// This method requires that a LoggingMiddleware has been registered with RegisterMiddleware.
// It temporarily enables debug-level logging for the specific query or execution that follows.
// The debug handle shares the same underlying connection and configuration as the parent instance.
// Example: db.Debug().Query(func(q *sqlc.Queries) error { return q.GetUser(ctx, id) })
func (d *Database[Q]) Debug() *Database[Q] {
	// If already in debug mode, return self
	if d.debug {
		return d
	}

	// Determine the parent instance
	parent := d
	if d.parent != nil {
		parent = d.parent
	}

	// Create a debug instance that references the parent
	// We don't copy the struct fields directly to avoid copying locks
	return &Database[Q]{
		debug:  true,
		parent: parent,
	}
}

// Execute executes a function with a database connection.
// It will return an error if the database is not connected or if the function returns an error.
func (d *Database[Q]) Execute(call func(db *sql.DB) error) error {
	defer interruption.Catch()

	// Get the actual database instance (from parent if debug mode)
	parent := d
	if d.debug && d.parent != nil {
		parent = d.parent
	}

	parent.dbMutex.RLock()
	instance := parent.db
	parent.dbMutex.RUnlock()

	if !parent.connected.Load() || instance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	// If in debug mode, temporarily enable logging middleware
	if d.debug {
		loggingMW := d.findLoggingMiddleware()
		if loggingMW == nil {
			return apperror.NewErrorf("debug mode requires LoggingMiddleware to be registered")
		}
		wasEnabled := loggingMW.IsEnabled()
		loggingMW.SetEnabled(true)
		defer loggingMW.SetEnabled(wasEnabled)
	}

	err := call(instance)
	if err != nil {
		return err
	}

	return nil
}

// Query executes a function with sqlc-generated Queries instance.
// It will return an error if the database is not connected or if the function returns an error.
// Example: db.Query(func(q *sqlc.Queries) error { return q.GetUser(ctx, id) })
func (d *Database[Q]) Query(call func(q *Q) error) error {
	defer interruption.Catch()

	// Get the actual database instance (from parent if debug mode)
	parent := d
	if d.debug && d.parent != nil {
		parent = d.parent
	}

	if parent.queries == nil && parent.queriesFunc == nil {
		return apperror.NewErrorf("queries constructor not registered")
	}

	parent.dbMutex.RLock()
	dbInstance := parent.db
	parent.dbMutex.RUnlock()

	if !parent.connected.Load() || dbInstance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	// If in debug mode, temporarily enable logging middleware
	if d.debug {
		loggingMW := d.findLoggingMiddleware()
		if loggingMW == nil {
			return apperror.NewErrorf("debug mode requires LoggingMiddleware to be registered")
		}
		wasEnabled := loggingMW.IsEnabled()
		loggingMW.SetEnabled(true)
		defer loggingMW.SetEnabled(wasEnabled)
	}

	queries, err := parent.newQueries(dbInstance)
	if err != nil {
		return err
	}

	err = call(queries)
	if err != nil {
		return err
	}

	return nil
}

// Transaction executes a function within a database transaction.
// It will return an error if the database is not connected or if the function returns an error.
// If the function returns an error, the transaction will be rolled back.
func (d *Database[Q]) Transaction(call func(tx *sql.Tx) error) error {
	d.dbMutex.RLock()
	dbInstance := d.db
	d.dbMutex.RUnlock()

	if !d.connected.Load() || dbInstance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	tx, err := dbInstance.Begin()
	if err != nil {
		return apperror.NewErrorf("failed to begin transaction").AddError(err)
	}

	err = call(tx)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return apperror.NewErrorf("failed to rollback transaction").AddError(rbErr).AddError(err)
		}
		return err
	}

	err = tx.Commit()
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return apperror.NewErrorf("failed to rollback transaction").AddError(rbErr).AddError(err)
		}
		return apperror.NewErrorf("failed to commit transaction").AddError(err)
	}

	return nil
}

// QueryTransaction executes a function with a sqlc-generated Queries instance within a database transaction.
// It will return an error if the database is not connected or if the function returns an error.
// If the function returns an error, the transaction will be rolled back.
// Example: db.QueryTransaction(func(q *sqlc.Queries) error { return q.CreateUser(ctx, params) })
func (d *Database[Q]) QueryTransaction(call func(q *Q) error) error {
	defer interruption.Catch()

	// Get the actual database instance (from parent if debug mode)
	parent := d
	if d.debug && d.parent != nil {
		parent = d.parent
	}

	if parent.queries == nil && parent.queriesFunc == nil {
		return apperror.NewErrorf("queries constructor not registered")
	}

	parent.dbMutex.RLock()
	dbInstance := parent.db
	parent.dbMutex.RUnlock()

	if !parent.connected.Load() || dbInstance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	// If in debug mode, temporarily enable logging middleware
	if d.debug {
		loggingMW := d.findLoggingMiddleware()
		if loggingMW == nil {
			return apperror.NewErrorf("debug mode requires LoggingMiddleware to be registered")
		}
		wasEnabled := loggingMW.IsEnabled()
		loggingMW.SetEnabled(true)
		defer loggingMW.SetEnabled(wasEnabled)
	}

	// Begin transaction
	tx, err := dbInstance.Begin()
	if err != nil {
		return apperror.NewErrorf("failed to begin transaction").AddError(err)
	}

	queries, err := parent.newQueries(tx)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return apperror.NewErrorf("failed to rollback transaction").AddError(rbErr).AddError(err)
		}
		return err
	}

	// Execute the user's function
	err = call(queries)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return apperror.NewErrorf("failed to rollback transaction").AddError(rbErr).AddError(err)
		}
		return err
	}

	err = tx.Commit()
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return apperror.NewErrorf("failed to rollback transaction").AddError(rbErr).AddError(err)
		}
		return apperror.NewErrorf("failed to commit transaction").AddError(err)
	}

	return nil
}

// TestConnection tests the database connection by attempting to connect and ping the database.
func (d *Database[Q]) TestConnection(dsn string) error {
	instance, err := d.connect(d.driver, dsn)
	if err != nil {
		return apperror.NewError("connection test failed").AddError(err)
	}
	defer instance.Close()

	err = instance.Ping()
	if err != nil {
		return apperror.NewError("ping test failed").AddError(err)
	}

	return nil
}

// Connected returns true if the database is connected, false otherwise
func (d *Database[Q]) Connected() bool {
	return d.connected.Load()
}

// Reconnect will set the connected state to false and trigger a reconnect
func (d *Database[Q]) Reconnect(dsn string) {
	d.dsn = dsn
	d.logger.Trace().Msg("reconnecting...")
	d.connected.Store(false)
	d.failed.Store(false)
}

// Disconnect will stop the database connection and wait for the connection to be closed
func (d *Database[Q]) Disconnect() error {
	d.logger.Trace().Msg("closing connection...")
	d.cancel.Store(true)
	if d.connected.Load() && d.db != nil {
		err := d.db.Close()
		if err != nil {
			return apperror.NewErrorf("failed to close database connection").AddError(err)
		}
	}
	<-d.done
	d.logger.Trace().Msg("connection closed")
	return nil
}

// AwaitConnection will block until the database is connected
func (d *Database[Q]) AwaitConnection() {
	for !d.connected.Load() {
		time.Sleep(time.Second)
	}
}

// Connect will try to connect to the database every interval until it is connected
// It will also check if the connection is still alive every interval and reconnect if it is not
func (d *Database[Q]) Connect(interval time.Duration, dsn string) {
	d.dsn = dsn
	go func() {
		for {
			func() {
				defer interruption.Catch()
				defer time.Sleep(interval)

				// If we are not connected to the database, try to connect
				if !d.connected.Load() {
					var err error
					instance, err := d.connect(d.driver, d.dsn)
					if err != nil {
						// Prevent spamming the logs with connection errors
						if !d.failed.Load() {
							d.logger.Error().Err(err).Msg("connection failed")
						}
						d.failed.Store(true)
						return
					}

					d.dbMutex.Lock()
					replaced := d.db
					d.db = instance
					d.dbMutex.Unlock()

					// The pool being replaced (a reconnect after a configuration change or a
					// failed health check) is no longer reachable through the handle, so close
					// it: otherwise its idle connections and opener goroutine live until exit,
					// once per reconnect. Queries already running on it finish normally.
					if replaced != nil && replaced != instance {
						if err := replaced.Close(); err != nil {
							d.logger.Warn().Err(err).Msg("failed to close replaced connection pool")
						}
					}

					d.handlerMutex.Lock()
					handlers := make([]func(db *sql.DB) error, len(d.onConnectHandler))
					copy(handlers, d.onConnectHandler)
					d.handlerMutex.Unlock()

					d.failed.Store(false)
					d.connected.Store(true)

					for _, handler := range handlers {
						err := handler(instance)
						if err != nil {
							d.logger.Error().Err(err).Msg("onConnect handler failed")
							d.failed.Store(true)
							return
						}
					}

					if d.failed.Load() {
						d.logger.Debug().Msg("connection restored")
					}
					d.logger.Debug().Msg("connection established")
					return
				}

				// Verify that we are indeed connected, if 'SELECT 1;' fails we assume
				// that the database is currently unavailable
				d.dbMutex.RLock()
				dbInstance := d.db
				d.dbMutex.RUnlock()

				if dbInstance != nil {
					// Retry ping a few times before declaring connection lost
					// This prevents treating transient connection pool errors as database failures
					const maxRetries = 3
					var lastErr error
					pingSucceeded := false

					for i := 0; i < maxRetries; i++ {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						err := dbInstance.PingContext(ctx)
						cancel()

						if err == nil {
							pingSucceeded = true
							break
						}
						lastErr = err

						// Brief pause between retries to allow bad connections to be removed from pool
						if i < maxRetries-1 {
							time.Sleep(100 * time.Millisecond)
						}
					}

					if !pingSucceeded && d.connected.Load() {
						d.logger.Error().Err(lastErr).Msgf("connection lost after %d ping attempts", maxRetries)
						d.connected.Store(false)
						d.failed.Store(true)
					}
				}
			}()

			if d.cancel.Load() {
				d.done <- true
				return
			}
		}
	}()
}

// RegisterOnConnectHandler registers a function that will be called when the database connection is established
func (d *Database[Q]) RegisterOnConnectHandler(handler func(db *sql.DB) error) {
	if handler == nil {
		return
	}

	d.handlerMutex.Lock()
	defer d.handlerMutex.Unlock()
	d.onConnectHandler = append(d.onConnectHandler, handler)
}

// RegisterMiddleware registers a middleware that will intercept and log SQL statements
func (d *Database[Q]) RegisterMiddleware(middleware Middleware) *Database[Q] {
	if middleware == nil {
		return d
	}

	d.middlewareMutex.Lock()
	defer d.middlewareMutex.Unlock()
	d.middlewares = append(d.middlewares, middleware)
	return d
}

// RegisterQueries registers the sqlc Queries constructor function.
// This allows you to set the queries constructor after creating the database instance.
// Example: db.RegisterQueries(sqlc.New)
func (d *Database[Q]) RegisterQueries(queries any) *Database[Q] {
	if queries == nil {
		return d
	}
	d.queries = queries
	return d
}

// RegisterQueriesFunc registers a typed sqlc Queries constructor.
// Unlike RegisterQueries it is called directly, without reflection, which saves about a
// microsecond per Query and Transaction call. sqlc generates its own DBTX type, so wrap the
// constructor: db.RegisterQueriesFunc(func(c database.DBTX) *sqlc.Queries { return sqlc.New(c) })
func (d *Database[Q]) RegisterQueriesFunc(queries func(DBTX) *Q) *Database[Q] {
	if queries == nil {
		return d
	}
	d.queriesFunc = queries
	return d
}

// findLoggingMiddleware finds the LoggingMiddleware in the parent's middleware list.
// Returns nil if no LoggingMiddleware is registered.
func (d *Database[Q]) findLoggingMiddleware() *LoggingMiddleware {
	parent := d
	if d.parent != nil {
		parent = d.parent
	}

	parent.middlewareMutex.RLock()
	defer parent.middlewareMutex.RUnlock()

	for _, mw := range parent.middlewares {
		if loggingMW, ok := mw.(*LoggingMiddleware); ok {
			return loggingMW
		}
	}

	return nil
}

// connect will try to connect to the database and return the connection
func (d *Database[Q]) connect(driver Driver, dsn string) (*sql.DB, error) {
	d.middlewareMutex.RLock()
	middlewares := make([]Middleware, len(d.middlewares))
	copy(middlewares, d.middlewares)
	d.middlewareMutex.RUnlock()

	driverName := wrap(string(driver), middlewares)
	conn, err := sql.Open(driverName, escapeDSNParams(dsn))
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	err = conn.Ping()
	if err != nil {
		conn.Close()
		return nil, apperror.Wrap(err)
	}

	return conn, nil
}

// Backup creates a backup of the database to the specified path.
// For SQLite: copies the database file
// For MySQL/MariaDB: creates an SQL dump
// For PostgreSQL: creates an SQL dump
// Returns an error if the database is not connected or if the backup fails.
func (d *Database[Q]) Backup(path string, schema string) error {
	if !d.connected.Load() {
		return apperror.NewErrorf("database is not connected")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return apperror.NewErrorf("failed to create backup directory").AddError(err)
	}

	switch d.driver {
	case DriverSQLite:
		if !strings.HasPrefix(d.dsn, "file:") {
			return apperror.NewErrorf("database backup is only supported for file-based SQLite databases")
		}

		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		// Execute a checkpoint to ensure all WAL data is in the main database file
		_, err := dbInstance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		if err != nil {
			return apperror.NewErrorf("failed to checkpoint WAL").AddError(err)
		}

		// Parse and validate the SQLite DSN to get the file path
		sourceFilePath, err := parseSQLiteFilePath(d.dsn)
		if err != nil {
			return apperror.Wrap(err)
		}

		// Copy the database file
		if err := copyFile(sourceFilePath, path, 0640); err != nil {
			return apperror.NewErrorf("failed to copy database file").AddError(err)
		}

		d.logger.Info().Msgf("database backup created: %s", path)
		return nil

	case DriverMySQL:
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		return d.backupMySQL(dbInstance, path)

	case DriverPostgres:
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		return d.backupPostgres(dbInstance, path, schema)

	default:
		return apperror.NewErrorf("unsupported database driver for backup: %v", d.driver)
	}
}

// Restore restores the database from a backup file at the specified path.
// For SQLite: replaces the current database file with the backup
// For MySQL/MariaDB: uses mysql client to restore from SQL dump
// For PostgreSQL: uses psql to restore from SQL dump
// Returns an error if the database is not connected or if the restore fails.
// WARNING: This will overwrite the current database. Ensure you have a backup before restoring.
func (d *Database[Q]) Restore(backupPath string) error {
	if !d.connected.Load() {
		return apperror.NewErrorf("database is not connected")
	}

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return apperror.NewErrorf("backup file does not exist: %s", backupPath)
	}

	switch d.driver {
	case DriverSQLite:
		if !strings.HasPrefix(d.dsn, "file:") {
			return apperror.NewErrorf("restore is only supported for file-based SQLite databases")
		}

		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance != nil {
			err := dbInstance.Close()
			if err != nil {
				return apperror.NewErrorf("failed to close database connection").AddError(err)
			}
		}

		d.connected.Store(false)

		// Parse and validate the SQLite DSN to get the file path
		targetPath, err := parseSQLiteFilePath(d.dsn)
		if err != nil {
			return apperror.Wrap(err)
		}

		if err := copyFile(backupPath, targetPath, 0640); err != nil {
			return apperror.NewErrorf("failed to restore database file").AddError(err)
		}

		os.Remove(targetPath + "-wal")
		os.Remove(targetPath + "-shm")

		d.logger.Info().Msgf("database restored from backup: %s", backupPath)

		d.Reconnect(d.dsn)
		return nil

	case DriverMySQL:
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupFile, err := os.Open(backupPath)
		if err != nil {
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}
		defer func() { _ = backupFile.Close() }()

		// SET NAMES and SET FOREIGN_KEY_CHECKS in the dump apply to one connection, so all
		// statements have to run on the same one, not on whichever the pool hands out
		conn, err := dbInstance.Conn(context.Background())
		if err != nil {
			return apperror.NewErrorf("failed to get a database connection").AddError(err)
		}
		defer func() { _ = conn.Close() }()

		// Every statement is its own transaction otherwise, and each commit waits for the disk. The
		// CREATE and DROP statements of the dump commit implicitly, so the rows of a table go in as
		// one transaction.
		if _, err := conn.ExecContext(context.Background(), "SET autocommit = 0"); err != nil {
			return apperror.NewErrorf("failed to start the restore").AddError(err)
		}
		// The connection goes back to the pool, so it must not stay in this mode
		defer func() {
			_, _ = conn.ExecContext(context.Background(), "SET autocommit = 1")
			_, _ = conn.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS = 1")
		}()

		// Statements run as they are read, so the dump is never held in memory as a whole
		err = forEachStatement(backupFile, func(stmt string) {
			if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
				d.logger.Warn().Err(err).Msgf("failed to execute statement: %s", stmt[:min(50, len(stmt))])
			}
		})
		if err != nil {
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}

		if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
			return apperror.NewErrorf("failed to commit the restore").AddError(err)
		}

		d.logger.Info().Msgf("database restored from backup: %s", backupPath)
		return nil

	case DriverPostgres:
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupFile, err := os.Open(backupPath)
		if err != nil {
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}
		defer func() { _ = backupFile.Close() }()

		tx, err := dbInstance.Begin()
		if err != nil {
			return apperror.NewErrorf("failed to begin transaction").AddError(err)
		}

		// Statements run as they are read, so the dump is never held in memory as a whole
		err = forEachStatement(backupFile, func(stmt string) {
			if _, err := tx.Exec(stmt); err != nil {
				d.logger.Warn().Err(err).Msgf("failed to execute statement: %s", stmt[:min(50, len(stmt))])
			}
		})
		if err != nil {
			_ = tx.Rollback()
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}

		if err := tx.Commit(); err != nil {
			tx.Rollback()
			return apperror.NewErrorf("failed to commit transaction").AddError(err)
		}

		d.logger.Info().Msgf("database restored from backup: %s", backupPath)
		return nil

	default:
		return apperror.NewErrorf("unsupported database driver for restore: %v", d.driver)
	}
}

// validateIdentifier checks if a SQL identifier is safe to use
// Identifiers can only contain alphanumeric characters, underscores, hyphens, and dots
func validateIdentifier(identifier string) error {
	if identifier == "" {
		return apperror.NewErrorf("identifier cannot be empty")
	}
	for _, char := range identifier {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' && char != '.' {
			return apperror.NewErrorf("invalid character in identifier: %c", char)
		}
	}
	return nil
}

// quoteIdentifier safely quotes a SQL identifier for the given driver
func quoteIdentifier(identifier string, driver Driver) (string, error) {
	err := validateIdentifier(identifier)
	if err != nil {
		return "", apperror.Wrap(err)
	}
	switch driver {
	case DriverMySQL:
		return "`" + identifier + "`", nil
	case DriverPostgres:
		return "\"" + identifier + "\"", nil
	case DriverSQLite:
		return "\"" + identifier + "\"", nil
	default:
		return "", apperror.NewErrorf("unsupported driver: %s", driver)
	}
}

// parseSQLiteFilePath extracts and validates the file path from a SQLite DSN.
// SQLite DSN format: "file:path/to/db.db[?options]"
// Returns the file path without the "file:" prefix and query parameters.
func parseSQLiteFilePath(dsn string) (string, error) {
	if len(dsn) <= 5 || !strings.HasPrefix(dsn, "file:") {
		return "", apperror.NewErrorf("invalid SQLite DSN format: %s (expected 'file:path')", dsn)
	}

	// Remove "file:" prefix
	pathWithQuery := dsn[5:]
	if pathWithQuery == "" {
		return "", apperror.NewErrorf("SQLite DSN contains no file path after 'file:' prefix")
	}

	// Split on '?' to remove query parameters
	parts := strings.SplitN(pathWithQuery, "?", 2)
	filePath := parts[0]

	// Clean and normalize the file path
	return filepath.Clean(strings.TrimSpace(filePath)), nil
}

// escapeDSNParams takes a DSN string and URL-encodes the values of any query parameters.
// For example, "user:password@tcp(localhost:3306)/dbname?time_zone='+00:00'" becomes "user:password@tcp(localhost:3306)/dbname?time_zone=%27%2B00%3A00%27"
func escapeDSNParams(dsn string) string {
	s := strings.Split(dsn, "?")
	if len(s) != 2 {
		return dsn
	}

	base := s[0]
	params := strings.Split(s[1], "&")
	for i, param := range params {
		kv := strings.SplitN(param, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		value := kv[1]
		params[i] = fmt.Sprintf("%s=%s", key, url.QueryEscape(value))
	}

	return base + "?" + strings.Join(params, "&")

}
