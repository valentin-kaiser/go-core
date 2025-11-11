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
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/version"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gl "gorm.io/gorm/logger"
)

var (
	db               *gorm.DB
	dbMutex          sync.RWMutex
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
func Reconnect() {
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
func Connect(interval time.Duration, config Config) {
	go func() {
		for {
			func() {
				defer interruption.Catch()
				defer time.Sleep(interval)

				// If we are not connected to the database, try to connect
				if !connected.Load() {
					var err error
					dbInstance, err := connect(config)
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

					onConnect(config)
					handlerMutex.Lock()
					handlers := make([]func(db *gorm.DB, config Config) error, len(onConnectHandler))
					copy(handlers, onConnectHandler)
					handlerMutex.Unlock()

					for _, handler := range handlers {
						err := handler(dbInstance, config)
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
func connect(config Config) (*gorm.DB, error) {
	// Silence gorm internal logging
	newLogger := gl.New(
		logger,
		gl.Config{
			SlowThreshold: time.Second,
			LogLevel:      gl.Silent,
		},
	)
	// If we are in trace loglevel, enable gorm logging
	if logger.GetLevel() < logging.TraceLevel && flag.Debug {
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
			if _, err := os.Stat(flag.Path); os.IsNotExist(err) {
				err := os.Mkdir(flag.Path, 0750)
				if err != nil {
					return nil, err
				}
			}
			dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000", filepath.Join(flag.Path, config.Name+".db"))
		}

		var err error
		conn, err := gorm.Open(sqlite.Open(dsn), cfg)
		if err != nil {
			return nil, err
		}

		sqlDB, err := conn.DB()
		if err != nil {
			return nil, err
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
			return nil, err
		}

		err = create.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`;", config.Name)).Error
		if err != nil {
			return nil, err
		}

		sdb, err := create.DB()
		if err != nil {
			return nil, err
		}
		sdb.Close()

		conn, err := gorm.Open(mysql.Open(fmt.Sprintf(
			"%v:%v@tcp(%v:%v)/%v?charset=utf8mb4&parseTime=True&loc=Local",
			config.User,
			config.Password,
			config.Host,
			config.Port,
			config.Name,
		)), cfg)
		if err != nil {
			return nil, err
		}

		// Set global db with proper locking
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
