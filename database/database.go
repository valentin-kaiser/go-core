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
//
// Example:
//
//	package main
//
//	import (
//		"database/sql"
//		"fmt"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/database"
//		"github.com/valentin-kaiser/go-core/flag"
//		"github.com/valentin-kaiser/go-core/version"
//	)
//
//	func main() {
//		flag.Init()
//
//		// Create a new database instance
//		db := database.New("main")
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
//		// Get the underlying *sql.DB
//		sqlDB := db.GetDB()
//		var ver version.Release
//		err := sqlDB.QueryRow("SELECT git_tag, git_commit FROM releases ORDER BY id DESC LIMIT 1").Scan(&ver.GitTag, &ver.GitCommit)
//		if err != nil {
//			panic(err)
//		}
//
//		fmt.Println("Version:", ver.GitTag, ver.GitCommit)
//	}
//
// Multi-Instance Example:
//
//	// Connect to multiple databases simultaneously
//	postgres := database.New("postgres-main")
//	mysql := database.New("mysql-analytics")
//	sqlite := database.New("sqlite-cache")
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
package database

import (
	"database/sql"
	"fmt"
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
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/version"
)

// Database represents a database connection instance with its own state and configuration.
// Multiple instances can be created to manage connections to different databases.
type Database struct {
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
	migrationSteps   map[string]map[string][]Step
	migrationMutex   sync.RWMutex
}

// New creates a new Database instance with the given name for logging purposes.
// The name parameter is used to identify this database instance in logs.
func New(name string) *Database {
	return &Database{
		done:             make(chan bool),
		logger:           logging.GetPackageLogger("database:" + name),
		migrationSteps:   make(map[string]map[string][]Step),
		onConnectHandler: make([]func(db *sql.DB, config Config) error, 0),
	}
}

// Get returns the active database connection.
// It will return nil if the database is not connected.
// Use this function to get the database connection for executing queries with sqlc or raw SQL.
func (d *Database) Get() *sql.DB {
	d.dbMutex.RLock()
	defer d.dbMutex.RUnlock()
	return d.db
}

// Execute executes a function with a database connection.
// It will return an error if the database is not connected or if the function returns an error.
func (d *Database) Execute(call func(db *sql.DB) error) error {
	defer interruption.Catch()

	d.dbMutex.RLock()
	dbInstance := d.db
	d.dbMutex.RUnlock()

	if !d.connected.Load() || dbInstance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	err := call(dbInstance)
	if err != nil {
		return err
	}

	return nil
}

// Transaction executes a function within a database transaction.
// It will return an error if the database is not connected or if the function returns an error.
// If the function returns an error, the transaction will be rolled back.
func (d *Database) Transaction(call func(tx *sql.Tx) error) error {
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

	if err := tx.Commit(); err != nil {
		return apperror.NewErrorf("failed to commit transaction").AddError(err)
	}

	return nil
}

// Connected returns true if the database is connected, false otherwise
func (d *Database) Connected() bool {
	return d.connected.Load()
}

// Reconnect will set the connected state to false and trigger a reconnect
func (d *Database) Reconnect(c Config) {
	d.configMutex.Lock()
	d.config = &c
	d.configMutex.Unlock()

	d.logger.Trace().Msg("reconnecting...")
	d.connected.Store(false)
	d.failed.Store(false)
}

// Disconnect will stop the database connection and wait for the connection to be closed
func (d *Database) Disconnect() error {
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
func (d *Database) AwaitConnection() {
	for !d.connected.Load() {
		time.Sleep(time.Second)
	}
}

// Connect will try to connect to the database every interval until it is connected
// It will also check if the connection is still alive every interval and reconnect if it is not
func (d *Database) Connect(interval time.Duration, c Config) {
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

					d.onConnect(c)
					d.handlerMutex.Lock()
					handlers := make([]func(db *sql.DB, config Config) error, len(d.onConnectHandler))
					copy(handlers, d.onConnectHandler)
					d.handlerMutex.Unlock()

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
					d.failed.Store(false)
					d.connected.Store(true)
					d.logger.Debug().Msg("connection established")
					return
				}

				// Verify that we are indeed connected, if 'SELECT 1;' fails we assume
				// that the database is currently unavailable
				d.dbMutex.RLock()
				dbInstance := d.db
				d.dbMutex.RUnlock()

				if dbInstance != nil {
					err := dbInstance.Ping()
					if err != nil && d.connected.Load() {
						d.logger.Error().Err(err).Msg("connection lost")
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
func (d *Database) RegisterOnConnectHandler(handler func(db *sql.DB, config Config) error) {
	if handler == nil {
		return
	}

	d.handlerMutex.Lock()
	defer d.handlerMutex.Unlock()
	d.onConnectHandler = append(d.onConnectHandler, handler)
}

// connect will try to connect to the database and return the connection
func (d *Database) connect() (*sql.DB, error) {
	d.configMutex.RLock()
	defer d.configMutex.RUnlock()

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

		conn, err := sql.Open("sqlite3", dsn)
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

		create, err := sql.Open("mysql", createDSN)
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
			"%v:%v@tcp(%v:%v)/%v?charset=utf8mb4&parseTime=True&loc=Local",
			d.config.User,
			d.config.Password,
			d.config.Host,
			d.config.Port,
			d.config.Name,
		)

		conn, err := sql.Open("mysql", dsn)
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

		create, err := sql.Open("pgx", createDSN)
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

		// Now connect to the actual database
		dsn := fmt.Sprintf(
			"host=%v port=%v user=%v password=%v dbname=%v sslmode=disable",
			d.config.Host,
			d.config.Port,
			d.config.User,
			d.config.Password,
			d.config.Name,
		)

		conn, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		if err := conn.Ping(); err != nil {
			conn.Close()
			return nil, apperror.Wrap(err)
		}

		// Create schema if it doesn't exist
		err = conn.QueryRow("SELECT exists (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1);", d.config.Name).Scan(&exists)
		if err != nil {
			conn.Close()
			return nil, apperror.Wrap(err)
		}

		if !exists {
			quotedName, err := quoteIdentifier(d.config.Name, "postgres")
			if err != nil {
				conn.Close()
				return nil, apperror.NewErrorf("invalid schema name").AddError(err)
			}
			_, err = conn.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", quotedName))
			if err != nil {
				conn.Close()
				return nil, apperror.Wrap(err)
			}
		}

		// Set search path
		_, err = conn.Exec(fmt.Sprintf("SET search_path TO %s, public;", d.config.Name))
		if err != nil {
			conn.Close()
			return nil, apperror.Wrap(err)
		}

		return conn, nil

	default:
		return nil, apperror.NewErrorf("unsupported database driver: %v", d.config.Driver)
	}
}

// onConnect is an internal Event handler that will be called when a Database Connection is successfully established
// It will run the migration setup and check if the database is up to date
func (d *Database) onConnect(config Config) {
	err := setup(d.db, config.Driver)
	if err != nil {
		d.logger.Fatal().Err(err).Msg("schema setup failed")
	}

	// Check for the current version in the database
	revision := version.Get()
	exists, err := versionExists(d.db, config.Driver, revision.GitTag, revision.GitCommit)
	if err != nil {
		d.logger.Fatal().Err(err).Msg("version check failed")
	}

	if !exists {
		// Run all registered migration steps
		for _, steps := range d.getMigrationSteps() {
			if len(steps) == 0 {
				continue
			}

			// Check if this migration version already exists
			stepExists, err := versionExists(d.db, config.Driver, steps[0].Version.GitTag, steps[0].Version.GitCommit)
			if err != nil {
				d.logger.Fatal().Err(err).Msg("migration version check failed")
			}

			if !stepExists {
				for _, step := range steps {
					// Execute all migration step actions for this version
					err = step.Action(d.db)
					if err != nil {
						d.logger.Fatal().Err(err).Msg("migration failed")
					}
				}

				// Create the version record for this migration step
				err = insertVersion(d.db, config.Driver, steps[0].Version)
				if err != nil {
					d.logger.Fatal().Err(err).Msg("version creation failed")
				}
			}
		}

		// Insert the current version
		err = insertVersion(d.db, config.Driver, *revision)
		if err != nil {
			d.logger.Fatal().Err(err).Msg("version creation failed")
		}
	}

	if config.Driver == "sqlite" {
		_, err = d.db.Exec("VACUUM;")
		if err != nil {
			d.logger.Warn().Err(err).Msg("vacuum failed")
		}
	}
}

// Backup creates a backup of the database to the specified path.
// For SQLite: copies the database file
// For MySQL/MariaDB: uses mysqldump
// For PostgreSQL: uses pg_dump
// Returns an error if the database is not connected or if the backup fails.
func (d *Database) Backup(path string) error {
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
func (d *Database) Restore(backupPath string) error {
	if !d.connected.Load() {
		return apperror.NewErrorf("database is not connected")
	}

	d.configMutex.RLock()
	defer d.configMutex.RUnlock()

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return apperror.NewErrorf("backup file does not exist: %s", backupPath)
	}

	switch d.config.Driver {
	case "sqlite":
		if d.config.Name == ":memory:" {
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

		targetPath := filepath.Join(flag.Path, d.config.Name+".db")
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

		d.Reconnect(*d.config)
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
	if err := validateIdentifier(identifier); err != nil {
		return "", err
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
