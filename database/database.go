// Package database provides a robust and flexible abstraction over GORM,
// supporting both SQLite and MySQL/MariaDB as backend databases.
//
// It offers features such as automatic connection handling, schema migrations,
// and version tracking. The package also allows registering custom on-connect
// handlers that are executed once the database connection is successfully established.
//
// Core features:
//
//   - Automatic (re)connection with health checks and retry mechanism
//   - Support for SQLite and MySQL/MariaDB with configurable parameters
//   - Schema management and automatic migrations
//   - Versioning support using the go-core/version package
//   - Connection lifecycle management (Connect, Disconnect, AwaitConnection)
//   - Thread-safe access with `Execute(func(db *gorm.DB) error)` wrapper
//   - Registering on-connect hooks to perform actions like seeding or setup
//
// Example:
//
//	package main
//
//	import (
//		"fmt"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/database"
//		"github.com/valentin-kaiser/go-core/flag"
//		"github.com/valentin-kaiser/go-core/version"
//		"gorm.io/gorm"
//	)
//
//	type User struct {
//		gorm.Model
//		Name     string
//		Email    string `gorm:"unique"`
//		Password string
//	}
//
//	func main() {
//		flag.Init()
//
//		database.RegisterSchema(&User{})
//		database.RegisterMigrationStep(version.Version{
//			GitTag:    "v1.0.0",
//			GitCommit: "abc123",
//		}, func(db *gorm.DB) error {
//			// Custom migration logic
//			return nil
//		})
//
//		database.Connect(time.Second, database.Config{
//			Driver: "sqlite",
//			Name:   "test",
//		})
//		defer database.Disconnect()
//
//		database.AwaitConnection()
//
//		var ver version.Version
//		err := database.Execute(func(db *gorm.DB) error {
//			return db.First(&ver).Error
//		})
//		if err != nil {
//			panic(err)
//		}
//
//		fmt.Println("Version:", ver.GitTag, ver.GitCommit)
//	}
package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/version"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gl "gorm.io/gorm/logger"
)

var (
	db               *gorm.DB
	dbMutex          sync.RWMutex
	config           *Config
	configMutex      sync.RWMutex
	connected        atomic.Bool
	failed           atomic.Bool
	cancel           atomic.Bool
	done             = make(chan bool)
	onConnectHandler []func(db *gorm.DB, config Config) error
	handlerMutex     sync.Mutex
	logger           = logging.GetPackageLogger("database")
)

// Execute executes a function with a database connection.
// It will return an error if the database is not connected or if the function returns an error.
// The function will be executed with a new session, so it will not affect the current transaction.
func Execute(call func(db *gorm.DB) error) error {
	defer interruption.Catch()

	dbMutex.RLock()
	dbInstance := db
	dbMutex.RUnlock()

	if !connected.Load() || dbInstance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	err := call(dbInstance.Session(&gorm.Session{}))
	if err != nil {
		return err
	}

	return nil
}

// Transaction executes a function within a database transaction.
// It will return an error if the database is not connected or if the function returns an error.
// If the function returns an error, the transaction will be rolled back.
func Transaction(call func(tx *gorm.DB) error) error {
	dbMutex.RLock()
	dbInstance := db
	dbMutex.RUnlock()

	if !connected.Load() || dbInstance == nil {
		return apperror.NewErrorf("database is not connected")
	}

	err := dbInstance.Transaction(func(tx *gorm.DB) error {
		return call(tx)
	})
	if err != nil {
		return err
	}
	return nil
}

// Connected returns true if the database is connected, false otherwise
func Connected() bool {
	return connected.Load()
}

// Reconnect will set the connected state to false and trigger a reconnect
func Reconnect(c Config) {
	configMutex.Lock()
	config = &c
	configMutex.Unlock()

	logger.Trace().Msg("reconnecting...")
	connected.Store(false)
	failed.Store(false)
}

// Disconnect will stop the database connection and wait for the connection to be closed
func Disconnect() error {
	logger.Trace().Msg("closing connection...")
	cancel.Store(true)
	if connected.Load() && db != nil {
		sdb, err := db.DB()
		if err != nil {
			return apperror.NewErrorf("failed to get database instance").AddError(err)
		}
		err = sdb.Close()
		if err != nil {
			return apperror.NewErrorf("failed to close database connection").AddError(err)
		}
	}
	<-done
	logger.Trace().Msg("connection closed")
	return nil
}

// AwaitConnection will block until the database is connected
func AwaitConnection() {
	for !connected.Load() {
		time.Sleep(time.Second)
	}
}

// Connect will try to connect to the database every interval until it is connected
// It will also check if the connection is still alive every interval and reconnect if it is not
func Connect(interval time.Duration, c Config) {
	configMutex.Lock()
	config = &c
	configMutex.Unlock()

	go func() {
		for {
			func() {
				defer interruption.Catch()
				defer time.Sleep(interval)

				// If we are not connected to the database, try to connect
				if !connected.Load() {
					var err error
					dbInstance, err := connect()
					if err != nil {
						// Prevent spamming the logs with connection errors
						if !failed.Load() {
							logger.Error().Err(err).Msg("connection failed")
						}
						failed.Store(true)
						return
					}

					dbMutex.Lock()
					db = dbInstance
					dbMutex.Unlock()

					onConnect(c)
					handlerMutex.Lock()
					handlers := make([]func(db *gorm.DB, config Config) error, len(onConnectHandler))
					copy(handlers, onConnectHandler)
					handlerMutex.Unlock()

					for _, handler := range handlers {
						err := handler(dbInstance, c)
						if err != nil {
							logger.Error().Err(err).Msg("onConnect handler failed")
							failed.Store(true)
							return
						}
					}

					if failed.Load() {
						logger.Debug().Msg("connection restored")
					}
					failed.Store(false)
					connected.Store(true)
					logger.Debug().Msg("connection established")
					return
				}

				// Verify that we are indeed connected, if 'SELECT 1;' fails we assume
				// that the database is currently unavailable
				dbMutex.RLock()
				dbInstance := db
				dbMutex.RUnlock()

				if dbInstance != nil {
					err := dbInstance.Exec("SELECT 1;").Error
					if err != nil && connected.Load() {
						logger.Error().Err(err).Msg("connection lost")
						connected.Store(false)
						failed.Store(true)
					}
				}
			}()

			if cancel.Load() {
				done <- true
				return
			}
		}
	}()
}

// RegisterOnConnectHandler registers a function that will be called when the database connection is established
func RegisterOnConnectHandler(handler func(db *gorm.DB, config Config) error) {
	if handler == nil {
		return
	}

	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	onConnectHandler = append(onConnectHandler, handler)
}

// connect will try to connect to the database and return the connection
func connect() (*gorm.DB, error) {
	configMutex.RLock()
	defer configMutex.RUnlock()

	// Silence gorm internal logging
	newLogger := gl.New(
		logger,
		gl.Config{
			SlowThreshold: time.Second,
			LogLevel:      gl.Silent,
		},
	)
	// If we are in trace loglevel, enable gorm logging
	if logger.GetLevel() <= logging.VerboseLevel && flag.Debug {
		newLogger = gl.New(
			logger,
			gl.Config{
				SlowThreshold: time.Second,
				LogLevel:      gl.Info,
			},
		)
	}
	cfg := &gorm.Config{Logger: newLogger}

	switch config.Driver {
	case "sqlite":
		dsn := "file:memdb1?mode=memory&cache=shared&_busy_timeout=5000"
		if config.Name != ":memory:" {
			_, err := os.Stat(flag.Path)
			if os.IsNotExist(err) {
				err := os.Mkdir(flag.Path, 0750)
				if err != nil {
					return nil, apperror.Wrap(err)
				}
			}
			dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000", filepath.Join(flag.Path, config.Name+".db"))
		}

		var err error
		conn, err := gorm.Open(sqlite.Open(dsn), cfg)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		sqlDB, err := conn.DB()
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		sqlDB.SetMaxOpenConns(16)
		sqlDB.SetMaxIdleConns(8)

		// Set global db with proper locking
		dbMutex.Lock()
		db = conn
		dbMutex.Unlock()

		return conn, nil
	case "mysql", "mariadb":
		create, err := gorm.Open(mysql.Open(fmt.Sprintf(
			"%v:%v@tcp(%v:%v)/?charset=utf8mb4&parseTime=True&loc=Local",
			config.User,
			config.Password,
			config.Host,
			config.Port,
		)), cfg)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		sdb, err := create.DB()
		if err != nil {
			return nil, apperror.Wrap(err)
		}
		defer apperror.Catch(sdb.Close, "closing temporary database connection failed")

		quotedName, err := quoteIdentifier(config.Name, "mysql")
		if err != nil {
			return nil, apperror.NewErrorf("invalid database name").AddError(err)
		}

		err = create.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s;", quotedName)).Error
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		conn, err := gorm.Open(mysql.Open(fmt.Sprintf(
			"%v:%v@tcp(%v:%v)/%v?charset=utf8mb4&parseTime=True&loc=Local",
			config.User,
			config.Password,
			config.Host,
			config.Port,
			config.Name,
		)), cfg)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		// Set global db with proper locking
		dbMutex.Lock()
		db = conn
		dbMutex.Unlock()

		return conn, nil
	case "postgres":
		create, err := gorm.Open(postgres.Open(fmt.Sprintf(
			"host=%v port=%v user=%v password=%v dbname=postgres sslmode=disable",
			config.Host,
			config.Port,
			config.User,
			config.Password,
		)), cfg)
		if err != nil {
			return nil, apperror.Wrap(err)
		}
		sdb, err := create.DB()
		if err != nil {
			return nil, apperror.Wrap(err)
		}
		defer apperror.Catch(sdb.Close, "closing temporary database connection failed")

		var exists bool
		err = create.Raw("SELECT exists (SELECT 1 FROM pg_database WHERE datname = ?);", config.Name).Scan(&exists).Error
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		if !exists {
			quotedName, err := quoteIdentifier(config.Name, "postgres")
			if err != nil {
				return nil, apperror.NewErrorf("invalid database name").AddError(err)
			}
			err = create.Exec(fmt.Sprintf("CREATE DATABASE %s;", quotedName)).Error
			if err != nil {
				return nil, apperror.Wrap(err)
			}
		}

		conn, err := gorm.Open(postgres.Open(fmt.Sprintf(
			"host=%v port=%v user=%v password=%v dbname=%v sslmode=disable search_path=%v",
			config.Host,
			config.Port,
			config.User,
			config.Password,
			config.Name,
			config.Name+",public",
		)), cfg)
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		err = conn.Raw("SELECT exists (SELECT 1 FROM information_schema.schemata WHERE schema_name = ?);", config.Name).Scan(&exists).Error
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		if !exists {
			quotedName, err := quoteIdentifier(config.Name, "postgres")
			if err != nil {
				return nil, apperror.NewErrorf("invalid schema name").AddError(err)
			}
			err = conn.Debug().Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", quotedName)).Error
			if err != nil {
				return nil, apperror.Wrap(err)
			}
		}

		dbMutex.Lock()
		db = conn
		dbMutex.Unlock()
		return conn, nil
	default:
		return nil, apperror.NewErrorf("unsupported database driver: %v", config.Driver)
	}
}

// onConnect is an internal Event handler that will be called when a Database Connection is successfully established
// It will run the migration setup and check if the database is up to date
func onConnect(config Config) {
	err := setup(db)
	if err != nil {
		logger.Fatal().Err(err).Msg("schema setup failed")
	}

	// Check for the current version in the database
	revision := version.Get()
	err = db.Where("git_tag = ? AND git_commit = ?", revision.GitTag, revision.GitCommit).First(&version.Release{}).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Fatal().Err(err).Msg("version check failed")
	}

	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		err = migrateSchema(db)
		if err != nil {
			logger.Fatal().Err(err).Msg("schema migration failed")
		}

		for _, steps := range getMigrationSteps() {
			if len(steps) == 0 {
				continue
			}

			// Check if the key exists as a revision
			err = db.Where("git_tag = ? AND git_commit = ?", steps[0].Version.GitTag, steps[0].Version.GitCommit).First(&version.Release{}).Error
			if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
				for _, step := range steps {
					// Execute all migration step actions for this version
					err = step.Action(db)
					if err != nil {
						logger.Fatal().Err(err).Msg("migration failed")
					}
				}

				// Create the version record for this migration step
				err = db.Create(&steps[0].Version).Error
				if err != nil {
					logger.Fatal().Err(err).Msg("version creation failed")
				}
			}
		}

		err = db.Create(&revision).Error
		if err != nil {
			logger.Fatal().Err(err).Msg("version creation failed")
		}
	}

	if config.Driver == "sqlite" {
		err = db.Exec("VACUUM;").Error
		if err != nil {
			logger.Warn().Err(err).Msg("vacuum failed")
		}
	}
}

// Backup creates a backup of the database to the specified path.
// For SQLite: copies the database file
// For MySQL/MariaDB: uses mysqldump
// For PostgreSQL: uses pg_dump
// Returns an error if the database is not connected or if the backup fails.
func Backup(path string) error {
	if !connected.Load() {
		return apperror.NewErrorf("database is not connected")
	}

	configMutex.RLock()
	defer configMutex.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return apperror.NewErrorf("failed to create backup directory").AddError(err)
	}

	switch config.Driver {
	case "sqlite":
		dbMutex.RLock()
		dbInstance := db
		dbMutex.RUnlock()

		// Execute a checkpoint to ensure all WAL data is in the main database file
		err := dbInstance.Exec("PRAGMA wal_checkpoint(TRUNCATE);").Error
		if err != nil {
			return apperror.NewErrorf("failed to checkpoint WAL").AddError(err)
		}

		// Determine source path
		var sourcePath string
		if config.Name == ":memory:" {
			return apperror.NewErrorf("cannot backup in-memory database")
		}
		sourcePath = filepath.Join(flag.Path, config.Name+".db")

		// Copy the database file
		sourceData, err := os.ReadFile(sourcePath)
		if err != nil {
			return apperror.NewErrorf("failed to read database file").AddError(err)
		}

		err = os.WriteFile(path, sourceData, 0640)
		if err != nil {
			return apperror.NewErrorf("failed to write backup file").AddError(err)
		}

		logger.Info().Msgf("database backup created: %s", path)
		return nil

	case "mysql", "mariadb":
		dbMutex.RLock()
		dbInstance := db
		dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupFile, err := os.Create(path)
		if err != nil {
			return apperror.NewErrorf("failed to create backup file").AddError(err)
		}
		defer backupFile.Close()

		_, err = backupFile.WriteString(fmt.Sprintf("-- MySQL/MariaDB database backup\n-- Database: %s\n-- Generated: %s\n\n",
			config.Name, time.Now().Format(time.RFC3339)))
		if err != nil {
			return apperror.NewErrorf("failed to write backup header").AddError(err)
		}

		_, err = backupFile.WriteString("SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n\n")
		if err != nil {
			return apperror.NewErrorf("failed to write charset settings").AddError(err)
		}

		var tables []string
		err = dbInstance.Raw("SHOW TABLES").Scan(&tables).Error
		if err != nil {
			return apperror.NewErrorf("failed to get table list").AddError(err)
		}

		for _, table := range tables {
			quotedTable, err := quoteIdentifier(table, "mysql")
			if err != nil {
				logger.Warn().Err(err).Msgf("invalid table name: %s", table)
				continue
			}
			var tableName, createStmt string
			err = dbInstance.Raw(fmt.Sprintf("SHOW CREATE TABLE %s", quotedTable)).Row().Scan(&tableName, &createStmt)
			if err != nil {
				logger.Warn().Err(err).Msgf("failed to get schema for table %s", table)
				continue
			}

			_, err = backupFile.WriteString(fmt.Sprintf("\n-- Table structure for %s\nDROP TABLE IF EXISTS `%s`;\n%s;\n\n",
				table, table, createStmt))
			if err != nil {
				return apperror.NewErrorf("failed to write table schema").AddError(err)
			}

			rows, err := dbInstance.Table(table).Rows()
			if err != nil {
				logger.Warn().Err(err).Msgf("failed to read data from table %s", table)
				continue
			}

			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				return apperror.NewErrorf("failed to get columns for table %s", table).AddError(err)
			}

			if len(columns) > 0 {
				_, err = backupFile.WriteString(fmt.Sprintf("-- Data for table %s\n", table))
				if err != nil {
					rows.Close()
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
						rows.Close()
						return apperror.NewErrorf("invalid column name").AddError(err)
					}
					colsList[i] = quotedCol
				}
				colsStr := strings.Join(colsList, ", ")

				for rows.Next() {
					if !hasData {
						quotedTableForInsert, err := quoteIdentifier(table, "mysql")
						if err != nil {
							rows.Close()
							return apperror.NewErrorf("invalid table name for insert").AddError(err)
						}
						_, err = backupFile.WriteString(fmt.Sprintf("INSERT INTO %s (%s) VALUES\n", quotedTableForInsert, colsStr))
						if err != nil {
							rows.Close()
							return apperror.NewErrorf("failed to write insert header").AddError(err)
						}
						hasData = true
					} else {
						_, err = backupFile.WriteString(",\n")
						if err != nil {
							rows.Close()
							return apperror.NewErrorf("failed to write comma").AddError(err)
						}
					}

					err = rows.Scan(valuePtrs...)
					if err != nil {
						rows.Close()
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
						rows.Close()
						return apperror.NewErrorf("failed to write values").AddError(err)
					}
				}

				if hasData {
					_, err = backupFile.WriteString(";\n\n")
					if err != nil {
						rows.Close()
						return apperror.NewErrorf("failed to write statement terminator").AddError(err)
					}
				}
			}
			rows.Close()
		}

		// Re-enable foreign key checks
		_, err = backupFile.WriteString("SET FOREIGN_KEY_CHECKS = 1;\n")
		if err != nil {
			return apperror.NewErrorf("failed to write footer").AddError(err)
		}

		logger.Info().Msgf("database backup created: %s", path)
		return nil

	case "postgres":
		dbMutex.RLock()
		dbInstance := db
		dbMutex.RUnlock()

		if dbInstance == nil {
			return apperror.NewErrorf("database instance is nil")
		}

		backupFile, err := os.Create(path)
		if err != nil {
			return apperror.NewErrorf("failed to create backup file").AddError(err)
		}
		defer backupFile.Close()

		_, err = backupFile.WriteString(fmt.Sprintf("-- PostgreSQL database backup\n-- Database: %s\n-- Generated: %s\n\n",
			config.Name, time.Now().Format(time.RFC3339)))
		if err != nil {
			return apperror.NewErrorf("failed to write backup header").AddError(err)
		}

		var tables []string
		err = dbInstance.Raw(`
			SELECT tablename 
			FROM pg_tables 
			WHERE schemaname = ?
			ORDER BY tablename
		`, config.Name).Scan(&tables).Error
		if err != nil {
			return apperror.NewErrorf("failed to get table list").AddError(err)
		}

		for _, table := range tables {
			var createStmt string
			err = dbInstance.Raw(`
				SELECT 'CREATE TABLE IF NOT EXISTS "' || c.relname || '" (' || 
					string_agg(a.attname || ' ' || pg_catalog.format_type(a.atttypid, a.atttypmod) || 
						CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END, ', ') || 
					');' as create_stmt
				FROM pg_class c
				JOIN pg_namespace n ON n.oid = c.relnamespace
				JOIN pg_attribute a ON a.attrelid = c.oid
				WHERE c.relname = ? AND n.nspname = ? AND a.attnum > 0 AND NOT a.attisdropped
				GROUP BY c.relname
			`, table, config.Name).Scan(&createStmt).Error
			if err != nil {
				logger.Warn().Err(err).Msgf("failed to get schema for table %s", table)
				continue
			}

			_, err = backupFile.WriteString(fmt.Sprintf("\n-- Table: %s\n%s\n\n", table, createStmt))
			if err != nil {
				return apperror.NewErrorf("failed to write table schema").AddError(err)
			}

			// Use schema-qualified table name with proper quoting
			qualifiedTable := fmt.Sprintf("%s.%s", config.Name, table)
			rows, err := dbInstance.Table(qualifiedTable).Rows()
			if err != nil {
				logger.Warn().Err(err).Msgf("failed to read data from table %s", table)
				continue
			}

			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				return apperror.NewErrorf("failed to get columns for table %s", table).AddError(err)
			}

			if len(columns) > 0 {
				_, err = backupFile.WriteString(fmt.Sprintf("-- Data for table: %s\n", table))
				if err != nil {
					rows.Close()
					return apperror.NewErrorf("failed to write data header").AddError(err)
				}

				values := make([]interface{}, len(columns))
				valuePtrs := make([]interface{}, len(columns))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				for rows.Next() {
					err = rows.Scan(valuePtrs...)
					if err != nil {
						rows.Close()
						return apperror.NewErrorf("failed to scan row").AddError(err)
					}

					var valueStrings []string
					for _, val := range values {
						if val == nil {
							valueStrings = append(valueStrings, "NULL")
						} else {
							switch v := val.(type) {
							case []byte:
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", string(v)))
							case string:
								valueStrings = append(valueStrings, fmt.Sprintf("'%s'", v))
							default:
								valueStrings = append(valueStrings, fmt.Sprintf("%v", v))
							}
						}
					}

					colsList := make([]string, len(columns))
					for i, col := range columns {
						quotedCol, err := quoteIdentifier(col, "postgres")
						if err != nil {
							rows.Close()
							return apperror.NewErrorf("invalid column name").AddError(err)
						}
						colsList[i] = quotedCol
					}

					quotedSchema, err := quoteIdentifier(config.Name, "postgres")
					if err != nil {
						rows.Close()
						return apperror.NewErrorf("invalid schema name").AddError(err)
					}
					quotedTableForInsert, err := quoteIdentifier(table, "postgres")
					if err != nil {
						rows.Close()
						return apperror.NewErrorf("invalid table name").AddError(err)
					}

					insertStmt := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s);\n",
						quotedSchema, quotedTableForInsert,
						fmt.Sprintf("%s", fmt.Sprint(colsList)[1:len(fmt.Sprint(colsList))-1]),
						fmt.Sprintf("%s", fmt.Sprint(valueStrings)[1:len(fmt.Sprint(valueStrings))-1]))
					_, err = backupFile.WriteString(insertStmt)
					if err != nil {
						rows.Close()
						return apperror.NewErrorf("failed to write insert statement").AddError(err)
					}
				}
			}
			rows.Close()
			_, err = backupFile.WriteString("\n")
			if err != nil {
				return apperror.NewErrorf("failed to write newline").AddError(err)
			}
		}

		logger.Info().Msgf("database backup created: %s", path)
		return nil

	default:
		return apperror.NewErrorf("unsupported database driver for backup: %v", config.Driver)
	}
}

// Restore restores the database from a backup file at the specified path.
// For SQLite: replaces the current database file with the backup
// For MySQL/MariaDB: uses mysql client to restore from SQL dump
// For PostgreSQL: uses psql to restore from SQL dump
// Returns an error if the database is not connected or if the restore fails.
// WARNING: This will overwrite the current database. Ensure you have a backup before restoring.
func Restore(backupPath string) error {
	if !connected.Load() {
		return apperror.NewErrorf("database is not connected")
	}

	configMutex.RLock()
	defer configMutex.RUnlock()

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return apperror.NewErrorf("backup file does not exist: %s", backupPath)
	}

	switch config.Driver {
	case "sqlite":
		if config.Name == ":memory:" {
			return apperror.NewErrorf("cannot restore to in-memory database")
		}

		dbMutex.RLock()
		dbInstance := db
		dbMutex.RUnlock()

		if dbInstance != nil {
			sqlDB, err := dbInstance.DB()
			if err != nil {
				return apperror.NewErrorf("failed to get database instance").AddError(err)
			}
			err = sqlDB.Close()
			if err != nil {
				return apperror.NewErrorf("failed to close database connection").AddError(err)
			}
		}

		connected.Store(false)

		targetPath := filepath.Join(flag.Path, config.Name+".db")
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

		logger.Info().Msgf("database restored from backup: %s", backupPath)

		Reconnect(*config)
		return nil

	case "mysql", "mariadb":
		dbMutex.RLock()
		dbInstance := db
		dbMutex.RUnlock()

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
			err := dbInstance.Exec(stmt).Error
			if err != nil {
				logger.Warn().Err(err).Msgf("failed to execute statement: %s", stmt[:min(50, len(stmt))])
			}
		}

		logger.Info().Msgf("database restored from backup: %s", backupPath)
		return nil

	case "postgres":
		dbMutex.RLock()
		dbInstance := db
		dbMutex.RUnlock()

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

		err = dbInstance.Transaction(func(tx *gorm.DB) error {
			for _, stmt := range statements {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				err := tx.Exec(stmt).Error
				if err != nil {
					logger.Warn().Err(err).Msgf("failed to execute statement: %s", stmt[:min(50, len(stmt))])
				}
			}
			return nil
		})
		if err != nil {
			return apperror.NewErrorf("failed to restore database").AddError(err)
		}

		logger.Info().Msgf("database restored from backup: %s", backupPath)
		return nil

	default:
		return apperror.NewErrorf("unsupported database driver for restore: %v", config.Driver)
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
