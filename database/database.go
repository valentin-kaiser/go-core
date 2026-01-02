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
//		// Option 1: Pass sqlc.New as the constructor function
//		db := database.New("main", sqlc.New)
//
//		// Option 2: Register queries constructor later
//		// db := database.New[sqlc.Queries]("main", nil)
//		// db.RegisterQueries(sqlc.New)
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
//		// Connect to the database
//		db.Connect(time.Second, database.Config{
//			Driver: "sqlite",
//			Name:   "test",
//		})
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
//	postgres := database.New("postgres-main", pgSqlc.New)
//	mysql := database.New("mysql-analytics", mysqlSqlc.New)
//	sqlite := database.New("sqlite-cache", sqliteSqlc.New)
//
//	postgres.Connect(time.Second, database.Config{
//		Driver: "postgres",
//		Host:   "localhost",
//		Port:   5432,
//		User:   "postgres",
//		Password: "secret",
//		Name:   "maindb",
//	})
//
//	mysql.Connect(time.Second, database.Config{
//		Driver: "mysql",
//		Host:   "localhost",
//		Port:   3306,
//		User:   "root",
//		Password: "password",
//		Name:   "analytics",
//	})
//
//	sqlite.Connect(time.Second, database.Config{
//		Driver: "sqlite",
//		Name:   ":memory:",
//	})
//
// Middleware Example:
//
//	// Create a new database instance with logging middleware
//	db := database.New("main", sqlc.New)
//
//	// Register logging middleware to log all SQL statements
//	logger := logging.GetPackageLogger("database")
//	loggingMiddleware := database.NewLoggingMiddleware(logger)
//	db.RegisterMiddleware(loggingMiddleware)
//
//	// Connect to the database
//	db.Connect(time.Second, database.Config{
//		Driver: "sqlite",
//		Name:   "example",
//	})
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
//	db := database.New("main", sqlc.New)
//
//	// LoggingMiddleware must be registered for Debug() to work
//	logger := logging.GetPackageLogger("database")
//	loggingMiddleware := database.NewLoggingMiddleware(logger)
//	loggingMiddleware.SetEnabled(false) // Disabled by default
//	db.RegisterMiddleware(loggingMiddleware)
//
//	db.Connect(time.Second, database.Config{
//		Driver: "sqlite",
//		Name:   "example",
//	})
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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
)

// Database represents a database connection instance with its own state and configuration.
// Multiple instances can be created to manage connections to different databases.
// The generic type parameter Q represents the sqlc-generated Queries type.
type Database[Q any] struct {
	db               *sql.DB
	dbMutex          sync.RWMutex
	config           *Config
	configMutex      sync.RWMutex
	connected        atomic.Bool
	failed           atomic.Bool
	cancel           atomic.Bool
	done             chan bool
	onConnectHandler []func(db *sql.DB, config Config) error
	handlerMutex     sync.Mutex
	logger           logging.Adapter
	migrationMutex   sync.RWMutex
	middlewares      []Middleware
	middlewareMutex  sync.RWMutex
	queries          any
	debug            bool
	parent           *Database[Q]
}

// New creates a new Database instance with the given name for logging purposes.
// The name parameter is used to identify this database instance in logs.
// The generic type parameter Q represents the sqlc-generated Queries type.
func New[Q any](name string) *Database[Q] {
	return &Database[Q]{
		done:             make(chan bool),
		logger:           logging.GetPackageLogger("database:" + name),
		onConnectHandler: make([]func(db *sql.DB, config Config) error, 0),
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

	if parent.queries == nil {
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

	// Use reflection to call the constructor function
	// This allows it to work with any DBTX interface type from sqlc
	fnValue := reflect.ValueOf(parent.queries)
	if fnValue.Kind() != reflect.Func {
		return apperror.NewErrorf("queries constructor is not a function")
	}

	results := fnValue.Call([]reflect.Value{reflect.ValueOf(dbInstance)})
	if len(results) != 1 {
		return apperror.NewErrorf("queries constructor must return exactly one value")
	}

	queries, ok := results[0].Interface().(*Q)
	if !ok {
		return apperror.NewErrorf("queries constructor returned unexpected type")
	}

	err := call(queries)
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

	if parent.queries == nil {
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

	// Use reflection to call the constructor function with the transaction
	fnValue := reflect.ValueOf(parent.queries)
	if fnValue.Kind() != reflect.Func {
		return apperror.NewErrorf("queries constructor is not a function")
	}

	results := fnValue.Call([]reflect.Value{reflect.ValueOf(tx)})
	if len(results) != 1 {
		return apperror.NewErrorf("queries constructor must return exactly one value")
	}

	queries, ok := results[0].Interface().(*Q)
	if !ok {
		return apperror.NewErrorf("queries constructor returned unexpected type")
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
func (d *Database[Q]) TestConnection(c Config) error {
	d.configMutex.Lock()
	d.config = &c
	d.configMutex.Unlock()

	instance, err := d.connect()
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
func (d *Database[Q]) Reconnect(c Config) {
	d.configMutex.Lock()
	d.config = &c
	d.configMutex.Unlock()

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
func (d *Database[Q]) Connect(interval time.Duration, c Config) {
	d.configMutex.Lock()
	d.config = &c
	d.configMutex.Unlock()

	go func() {
		for {
			func() {
				defer interruption.Catch()
				defer time.Sleep(interval)

				// If we are not connected to the database, try to connect
				if !d.connected.Load() {
					var err error
					instance, err := d.connect()
					if err != nil {
						// Prevent spamming the logs with connection errors
						if !d.failed.Load() {
							d.logger.Error().Err(err).Msg("connection failed")
						}
						d.failed.Store(true)
						return
					}

					d.dbMutex.Lock()
					d.db = instance
					d.dbMutex.Unlock()

					d.handlerMutex.Lock()
					handlers := make([]func(db *sql.DB, config Config) error, len(d.onConnectHandler))
					copy(handlers, d.onConnectHandler)
					d.handlerMutex.Unlock()

					d.failed.Store(false)
					d.connected.Store(true)
					d.logger.Debug().Msg("connection established")

					for _, handler := range handlers {
						err := handler(instance, c)
						if err != nil {
							d.logger.Error().Err(err).Msg("onConnect handler failed")
							d.failed.Store(true)
							return
						}
					}

					if d.failed.Load() {
						d.logger.Debug().Msg("connection restored")
					}
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
func (d *Database[Q]) RegisterOnConnectHandler(handler func(db *sql.DB, config Config) error) {
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
func (d *Database[Q]) connect() (*sql.DB, error) {
	d.configMutex.RLock()
	defer d.configMutex.RUnlock()

	d.middlewareMutex.RLock()
	middlewares := make([]Middleware, len(d.middlewares))
	copy(middlewares, d.middlewares)
	d.middlewareMutex.RUnlock()

	switch d.config.Driver {
	case "sqlite":
		dsn := "file:memdb1?mode=memory&cache=shared&_busy_timeout=5000"
		if d.config.Name != ":memory:" {
			_, err := os.Stat(flag.Path)
			if os.IsNotExist(err) {
				err := os.Mkdir(flag.Path, 0750)
				if err != nil {
					return nil, apperror.Wrap(err)
				}
			}
			dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000", filepath.Join(flag.Path, d.config.Name+".db"))
		}

		driverName := wrap("sqlite3", middlewares)
		conn, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		conn.SetMaxOpenConns(16)
		conn.SetMaxIdleConns(8)

		// Test the connection
		if err := conn.Ping(); err != nil {
			conn.Close()
			return nil, apperror.Wrap(err)
		}

		// Set database instance with proper locking
		d.dbMutex.Lock()
		d.db = conn
		d.dbMutex.Unlock()

		return conn, nil

	case "mysql", "mariadb":
		// First connect without database to create it if needed
		createDSN := fmt.Sprintf(
			"%v:%v@tcp(%v:%v)/?charset=utf8mb4&parseTime=True&loc=Local",
			d.config.User,
			d.config.Password,
			d.config.Host,
			d.config.Port,
		)

		driverName := wrap("mysql", middlewares)
		create, err := sql.Open(driverName, createDSN)
		if err != nil {
			return nil, apperror.Wrap(err)
		}
		defer create.Close()

		if err := create.Ping(); err != nil {
			return nil, apperror.Wrap(err)
		}

		quotedName, err := quoteIdentifier(d.config.Name, "mysql")
		if err != nil {
			return nil, apperror.NewErrorf("invalid database name").AddError(err)
		}

		_, err = create.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s;", quotedName))
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		// Now connect to the actual database
		dsn := fmt.Sprintf(
			"%v:%v@tcp(%v:%v)/%v?charset=utf8mb4&parseTime=True&loc=Local&multiStatements=true",
			d.config.User,
			d.config.Password,
			d.config.Host,
			d.config.Port,
			d.config.Name,
		)

		conn, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		if err := conn.Ping(); err != nil {
			conn.Close()
			return nil, apperror.Wrap(err)
		}

		return conn, nil

	case "postgres":
		// First connect to postgres database to create our database if needed
		createDSN := fmt.Sprintf(
			"host=%v port=%v user=%v password=%v dbname=postgres sslmode=disable",
			d.config.Host,
			d.config.Port,
			d.config.User,
			d.config.Password,
		)

		driverName := wrap("pgx", middlewares)
		create, err := sql.Open(driverName, createDSN)
		if err != nil {
			return nil, apperror.Wrap(err)
		}
		defer create.Close()

		if err := create.Ping(); err != nil {
			return nil, apperror.Wrap(err)
		}

		var exists bool
		err = create.QueryRow("SELECT exists (SELECT 1 FROM pg_database WHERE datname = $1);", d.config.Name).Scan(&exists)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		if !exists {
			quotedName, err := quoteIdentifier(d.config.Name, "postgres")
			if err != nil {
				return nil, apperror.NewErrorf("invalid database name").AddError(err)
			}
			_, err = create.Exec(fmt.Sprintf("CREATE DATABASE %s;", quotedName))
			if err != nil {
				return nil, apperror.Wrap(err)
			}
		}

		if strings.TrimSpace(d.config.Search) == "" {
			d.config.Search = "public"
		}

		// Now connect to the actual database
		dsn := fmt.Sprintf(
			"host=%v port=%v user=%v password=%v dbname=%v sslmode=disable search_path=%v",
			d.config.Host,
			d.config.Port,
			d.config.User,
			d.config.Password,
			d.config.Name,
			d.config.Search,
		)

		conn, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		if err := conn.Ping(); err != nil {
			conn.Close()
			return nil, apperror.Wrap(err)
		}

		return conn, nil

	default:
		return nil, apperror.NewErrorf("unsupported database driver: %v", d.config.Driver)
	}
}

// Backup creates a backup of the database to the specified path.
// For SQLite: copies the database file
// For MySQL/MariaDB: uses mysqldump
// For PostgreSQL: uses pg_dump
// Returns an error if the database is not connected or if the backup fails.
func (d *Database[Q]) Backup(path string) error {
	if !d.connected.Load() {
		return apperror.NewErrorf("database is not connected")
	}

	d.configMutex.RLock()
	defer d.configMutex.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return apperror.NewErrorf("failed to create backup directory").AddError(err)
	}

	switch d.config.Driver {
	case "sqlite":
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		// Execute a checkpoint to ensure all WAL data is in the main database file
		_, err := dbInstance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		if err != nil {
			return apperror.NewErrorf("failed to checkpoint WAL").AddError(err)
		}

		// Determine source path
		var sourcePath string
		if d.config.Name == ":memory:" {
			return apperror.NewErrorf("cannot backup in-memory database")
		}
		sourcePath = filepath.Join(flag.Path, d.config.Name+".db")

		// Copy the database file
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			return apperror.NewErrorf("failed to read database file").AddError(err)
		}

		err = os.WriteFile(path, sourceData, 0640)
		if err != nil {
			return apperror.NewErrorf("failed to write backup file").AddError(err)
		}

		d.logger.Info().Msgf("database backup created: %s", path)
		return nil

	case "mysql", "mariadb":
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupFile, err := os.Create(path)
		if err != nil {
			return apperror.NewErrorf("failed to create backup file").AddError(err)
		}
		defer backupFile.Close()

		_, err = backupFile.WriteString(fmt.Sprintf("-- MySQL/MariaDB database backup\n-- Database: %s\n-- Generated: %s\n\n",
			d.config.Name, time.Now().Format(time.RFC3339)))
		if err != nil {
			return apperror.NewErrorf("failed to write backup header").AddError(err)
		}

		_, err = backupFile.WriteString("SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n\n")
		if err != nil {
			return apperror.NewErrorf("failed to write charset settings").AddError(err)
		}

		rows, err := dbInstance.Query("SHOW TABLES")
		if err != nil {
			return apperror.NewErrorf("failed to get table list").AddError(err)
		}

		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				rows.Close()
				return apperror.NewErrorf("failed to scan table name").AddError(err)
			}
			tables = append(tables, table)
		}
		rows.Close()

		for _, table := range tables {
			quotedTable, err := quoteIdentifier(table, "mysql")
			if err != nil {
				d.logger.Warn().Err(err).Msgf("invalid table name: %s", table)
				continue
			}
			var tableName, createStmt string
			err = dbInstance.QueryRow(fmt.Sprintf("SHOW CREATE TABLE %s", quotedTable)).Scan(&tableName, &createStmt)
			if err != nil {
				d.logger.Warn().Err(err).Msgf("failed to get schema for table %s", table)
				continue
			}

			_, err = backupFile.WriteString(fmt.Sprintf("\n-- Table structure for %s\nDROP TABLE IF EXISTS `%s`;\n%s;\n\n",
				table, table, createStmt))
			if err != nil {
				return apperror.NewErrorf("failed to write table schema").AddError(err)
			}

			dataRows, err := dbInstance.Query(fmt.Sprintf("SELECT * FROM %s", quotedTable))
			if err != nil {
				d.logger.Warn().Err(err).Msgf("failed to read data from table %s", table)
				continue
			}

			columns, err := dataRows.Columns()
			if err != nil {
				dataRows.Close()
				return apperror.NewErrorf("failed to get columns for table %s", table).AddError(err)
			}

			if len(columns) > 0 {
				_, err = backupFile.WriteString(fmt.Sprintf("-- Data for table %s\n", table))
				if err != nil {
					dataRows.Close()
					return apperror.NewErrorf("failed to write data header").AddError(err)
				}

				hasData := false
				values := make([]interface{}, len(columns))
				valuePtrs := make([]interface{}, len(columns))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				colsList := make([]string, len(columns))
				for i, col := range columns {
					quotedCol, err := quoteIdentifier(col, "mysql")
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("invalid column name").AddError(err)
					}
					colsList[i] = quotedCol
				}
				colsStr := strings.Join(colsList, ", ")

				for dataRows.Next() {
					if !hasData {
						quotedTableForInsert, err := quoteIdentifier(table, "mysql")
						if err != nil {
							dataRows.Close()
							return apperror.NewErrorf("invalid table name for insert").AddError(err)
						}
						_, err = backupFile.WriteString(fmt.Sprintf("INSERT INTO %s (%s) VALUES\n", quotedTableForInsert, colsStr))
						if err != nil {
							dataRows.Close()
							return apperror.NewErrorf("failed to write insert header").AddError(err)
						}
						hasData = true
					} else {
						_, err = backupFile.WriteString(",\n")
						if err != nil {
							dataRows.Close()
							return apperror.NewErrorf("failed to write comma").AddError(err)
						}
					}

					err = dataRows.Scan(valuePtrs...)
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("failed to scan row").AddError(err)
					}

					// Build value list
					var valueStrings []string
					for _, val := range values {
						if val == nil {
							valueStrings = append(valueStrings, "NULL")
						} else {
							switch v := val.(type) {
							case []byte:
								escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
								escaped = strings.ReplaceAll(escaped, "'", "\\'")
								escaped = strings.ReplaceAll(escaped, "\n", "\\n")
								escaped = strings.ReplaceAll(escaped, "\r", "\\r")
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", escaped))
							case string:
								escaped := strings.ReplaceAll(v, "\\", "\\\\")
								escaped = strings.ReplaceAll(escaped, "'", "\\'")
								escaped = strings.ReplaceAll(escaped, "\n", "\\n")
								escaped = strings.ReplaceAll(escaped, "\r", "\\r")
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", escaped))
							case time.Time:
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", v.Format("2006-01-02 15:04:05")))
							default:
								valueStrings = append(valueStrings, fmt.Sprintf("%v", v))
							}
						}
					}

					_, err = backupFile.WriteString(fmt.Sprintf("(%s)", strings.Join(valueStrings, ", ")))
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("failed to write values").AddError(err)
					}
				}

				if hasData {
					_, err = backupFile.WriteString(";\n\n")
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("failed to write statement terminator").AddError(err)
					}
				}
			}
			dataRows.Close()
		}

		// Re-enable foreign key checks
		_, err = backupFile.WriteString("SET FOREIGN_KEY_CHECKS = 1;\n")
		if err != nil {
			return apperror.NewErrorf("failed to write footer").AddError(err)
		}

		d.logger.Info().Msgf("database backup created: %s", path)
		return nil

	case "postgres":
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupFile, err := os.Create(path)
		if err != nil {
			return apperror.NewErrorf("failed to create backup file").AddError(err)
		}
		defer backupFile.Close()

		_, err = backupFile.WriteString(fmt.Sprintf("-- PostgreSQL database backup\n-- Database: %s\n-- Generated: %s\n\n",
			d.config.Name, time.Now().Format(time.RFC3339)))
		if err != nil {
			return apperror.NewErrorf("failed to write backup header").AddError(err)
		}

		rows, err := dbInstance.Query(`
			SELECT tablename 
			FROM pg_tables 
			WHERE schemaname = $1
			ORDER BY tablename
		`, d.config.Name)
		if err != nil {
			return apperror.NewErrorf("failed to get table list").AddError(err)
		}

		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				rows.Close()
				return apperror.NewErrorf("failed to scan table name").AddError(err)
			}
			tables = append(tables, table)
		}
		rows.Close()

		for _, table := range tables {
			var createStmt string
			err = dbInstance.QueryRow(`
				SELECT 'CREATE TABLE IF NOT EXISTS "' || c.relname || '" (' || 
					string_agg(a.attname || ' ' || pg_catalog.format_type(a.atttypid, a.atttypmod) || 
						CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END, ', ') || 
					');' as create_stmt
				FROM pg_class c
				JOIN pg_namespace n ON n.oid = c.relnamespace
				JOIN pg_attribute a ON a.attrelid = c.oid
				WHERE c.relname = $1 AND n.nspname = $2 AND a.attnum > 0 AND NOT a.attisdropped
				GROUP BY c.relname
			`, table, d.config.Name).Scan(&createStmt)
			if err != nil {
				d.logger.Warn().Err(err).Msgf("failed to get schema for table %s", table)
				continue
			}

			_, err = backupFile.WriteString(fmt.Sprintf("\n-- Table: %s\n%s\n\n", table, createStmt))
			if err != nil {
				return apperror.NewErrorf("failed to write table schema").AddError(err)
			}

			// Use schema-qualified table name with proper quoting
			quotedSchema, err := quoteIdentifier(d.config.Name, "postgres")
			if err != nil {
				return apperror.NewErrorf("invalid schema name").AddError(err)
			}
			quotedTableForQuery, err := quoteIdentifier(table, "postgres")
			if err != nil {
				return apperror.NewErrorf("invalid table name").AddError(err)
			}

			dataRows, err := dbInstance.Query(fmt.Sprintf("SELECT * FROM %s.%s", quotedSchema, quotedTableForQuery))
			if err != nil {
				d.logger.Warn().Err(err).Msgf("failed to read data from table %s", table)
				continue
			}

			columns, err := dataRows.Columns()
			if err != nil {
				dataRows.Close()
				return apperror.NewErrorf("failed to get columns for table %s", table).AddError(err)
			}

			if len(columns) > 0 {
				_, err = backupFile.WriteString(fmt.Sprintf("-- Data for table: %s\n", table))
				if err != nil {
					dataRows.Close()
					return apperror.NewErrorf("failed to write data header").AddError(err)
				}

				values := make([]interface{}, len(columns))
				valuePtrs := make([]interface{}, len(columns))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				colsList := make([]string, len(columns))
				for i, col := range columns {
					quotedCol, err := quoteIdentifier(col, "postgres")
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("invalid column name").AddError(err)
					}
					colsList[i] = quotedCol
				}

				for dataRows.Next() {
					err = dataRows.Scan(valuePtrs...)
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("failed to scan row").AddError(err)
					}

					var valueStrings []string
					for _, val := range values {
						if val == nil {
							valueStrings = append(valueStrings, "NULL")
						} else {
							switch v := val.(type) {
							case []byte:
								escaped := strings.ReplaceAll(string(v), "\\", "\\\\")
								escaped = strings.ReplaceAll(escaped, "'", "''")
								escaped = strings.ReplaceAll(escaped, "\n", "\\n")
								escaped = strings.ReplaceAll(escaped, "\r", "\\r")
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", escaped))
							case string:
								escaped := strings.ReplaceAll(v, "\\", "\\\\")
								escaped = strings.ReplaceAll(escaped, "'", "''")
								escaped = strings.ReplaceAll(escaped, "\n", "\\n")
								escaped = strings.ReplaceAll(escaped, "\r", "\\r")
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", escaped))
							case time.Time:
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", v.Format("2006-01-02 15:04:05")))
							default:
								valueStrings = append(valueStrings, fmt.Sprintf("%v", v))
							}
						}
					}

					quotedTableForInsert, err := quoteIdentifier(table, "postgres")
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("invalid table name").AddError(err)
					}

					insertStmt := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s);\n",
						quotedSchema, quotedTableForInsert,
						strings.Join(colsList, ", "),
						strings.Join(valueStrings, ", "))
					_, err = backupFile.WriteString(insertStmt)
					if err != nil {
						dataRows.Close()
						return apperror.NewErrorf("failed to write insert statement").AddError(err)
					}
				}
			}
			dataRows.Close()
			_, err = backupFile.WriteString("\n")
			if err != nil {
				return apperror.NewErrorf("failed to write newline").AddError(err)
			}
		}

		d.logger.Info().Msgf("database backup created: %s", path)
		return nil

	default:
		return apperror.NewErrorf("unsupported database driver for backup: %v", d.config.Driver)
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

	d.configMutex.RLock()

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		d.configMutex.RUnlock()
		return apperror.NewErrorf("backup file does not exist: %s", backupPath)
	}

	cp := *d.config
	driverType := d.config.Driver
	d.configMutex.RUnlock()

	switch driverType {
	case "sqlite":
		if cp.Name == ":memory:" {
			return apperror.NewErrorf("cannot restore to in-memory database")
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

		targetPath := filepath.Join(flag.Path, cp.Name+".db")
		backupData, err := os.ReadFile(backupPath)
		if err != nil {
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}

		err = os.WriteFile(targetPath, backupData, 0640)
		if err != nil {
			return apperror.NewErrorf("failed to write database file").AddError(err)
		}

		os.Remove(targetPath + "-wal")
		os.Remove(targetPath + "-shm")

		d.logger.Info().Msgf("database restored from backup: %s", backupPath)

		d.Reconnect(cp)
		return nil

	case "mysql", "mariadb":
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupData, err := os.ReadFile(backupPath)
		if err != nil {
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}

		sqlContent := string(backupData)
		statements := []string{}
		currentStmt := ""

		for _, line := range strings.Split(sqlContent, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}

			currentStmt += line + "\n"
			if strings.HasSuffix(trimmed, ";") {
				statements = append(statements, currentStmt)
				currentStmt = ""
			}
		}

		for _, stmt := range statements {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			_, err := dbInstance.Exec(stmt)
			if err != nil {
				d.logger.Warn().Err(err).Msgf("failed to execute statement: %s", stmt[:min(50, len(stmt))])
			}
		}

		d.logger.Info().Msgf("database restored from backup: %s", backupPath)
		return nil

	case "postgres":
		d.dbMutex.RLock()
		dbInstance := d.db
		d.dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupData, err := os.ReadFile(backupPath)
		if err != nil {
			return apperror.NewErrorf("failed to read backup file").AddError(err)
		}

		sqlContent := string(backupData)
		statements := []string{}
		currentStmt := ""

		for _, line := range strings.Split(sqlContent, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "--") {
				continue
			}

			currentStmt += line + "\n"

			if strings.HasSuffix(trimmed, ";") {
				statements = append(statements, currentStmt)
				currentStmt = ""
			}
		}

		tx, err := dbInstance.Begin()
		if err != nil {
			return apperror.NewErrorf("failed to begin transaction").AddError(err)
		}

		for _, stmt := range statements {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			_, err := tx.Exec(stmt)
			if err != nil {
				d.logger.Warn().Err(err).Msgf("failed to execute statement: %s", stmt[:min(50, len(stmt))])
			}
		}

		if err := tx.Commit(); err != nil {
			tx.Rollback()
			return apperror.NewErrorf("failed to commit transaction").AddError(err)
		}

		d.logger.Info().Msgf("database restored from backup: %s", backupPath)
		return nil

	default:
		return apperror.NewErrorf("unsupported database driver for restore: %v", d.config.Driver)
	}
}

// validateIdentifier checks if a SQL identifier is safe to use
// Identifiers can only contain alphanumeric characters, underscores, hyphens, and dots
func validateIdentifier(identifier string) error {
	if identifier == "" {
		return apperror.NewErrorf("identifier cannot be empty")
	}
	for _, char := range identifier {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.') {
			return apperror.NewErrorf("invalid character in identifier: %c", char)
		}
	}
	return nil
}

// quoteIdentifier safely quotes a SQL identifier for the given driver
func quoteIdentifier(identifier string, driver string) (string, error) {
	err := validateIdentifier(identifier)
	if err != nil {
		return "", apperror.Wrap(err)
	}
	switch driver {
	case "mysql", "mariadb":
		return "`" + identifier + "`", nil
	case "postgres":
		return "\"" + identifier + "\"", nil
	case "sqlite":
		return "\"" + identifier + "\"", nil
	default:
		return "", apperror.NewErrorf("unsupported driver: %s", driver)
	}
}
