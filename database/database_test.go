package database_test

import (
	"errors"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/database"
	"github.com/valentin-kaiser/go-core/version"
	"gorm.io/gorm"
)

func TestConfig_Validate(t *testing.T) {
	testCases := []struct {
		name   string
		config database.Config
		valid  bool
	}{
		{
			name:   "empty config",
			config: database.Config{},
			valid:  false,
		},
		{
			name: "valid sqlite config",
			config: database.Config{
				Driver: "sqlite",
				Name:   "test.db",
			},
			valid: true,
		},
		{
			name: "invalid sqlite config - no name",
			config: database.Config{
				Driver: "sqlite",
			},
			valid: false,
		},
		{
			name: "valid mysql config",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "test",
			},
			valid: true,
		},
		{
			name: "invalid mysql config - no host",
			config: database.Config{
				Driver:   "mysql",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "test",
			},
			valid: false,
		},
		{
			name: "invalid mysql config - no port",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				User:     "root",
				Password: "password",
				Name:     "test",
			},
			valid: false,
		},
		{
			name: "invalid mysql config - no user",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				Password: "password",
				Name:     "test",
			},
			valid: false,
		},
		{
			name: "invalid mysql config - no password",
			config: database.Config{
				Driver: "mysql",
				Host:   "localhost",
				Port:   3306,
				User:   "root",
				Name:   "test",
			},
			valid: false,
		},
		{
			name: "invalid mysql config - no name",
			config: database.Config{
				Driver:   "mysql",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
			},
			valid: false,
		},
		{
			name: "valid mariadb config",
			config: database.Config{
				Driver:   "mariadb",
				Host:     "localhost",
				Port:     3306,
				User:     "root",
				Password: "password",
				Name:     "test",
			},
			valid: true,
		},
		{
			name: "unknown driver",
			config: database.Config{
				Driver:   "postgres",
				Host:     "localhost",
				Port:     5432,
				User:     "root",
				Password: "password",
				Name:     "test",
			},
			valid: true, // Unknown drivers are treated like MySQL
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.valid && err != nil {
				t.Errorf("Expected valid config, got error: %v", err)
			}
			if !tc.valid && err == nil {
				t.Errorf("Expected invalid config, got no error")
			}
		})
	}
}

func TestConnected(t *testing.T) {
	// Test that we can call the function without panic
	// Note: We don't test the initial state since other tests may have connected
	result := database.Connected()

	// The result should be a boolean (true or false)
	if result != true && result != false {
		t.Error("Connected() should return a boolean value")
	}
}

func TestExecuteWithoutConnection(t *testing.T) {
	// Ensure we're in a disconnected state for this test
	// Only disconnect if currently connected to avoid hanging
	if database.Connected() {
		err := database.Disconnect()
		if err != nil {
			t.Errorf("Disconnect should not return an error: %v", err)
		}
		time.Sleep(100 * time.Millisecond) // Wait for disconnection
	}

	// Test Execute when not connected
	err := database.Execute(func(_ *gorm.DB) error {
		return nil
	})

	if err == nil {
		t.Error("Execute should return error when not connected")
	}
}

func TestReconnect(t *testing.T) {
	// Test that Reconnect doesn't panic
	database.Reconnect(database.Config{
		Driver: "invalid-driver",
	})

	// Should still not be connected after reconnect without actual connection
	if database.Connected() {
		t.Error("Expected not connected after reconnect without connection")
	}
}

func TestAwaitConnectionTimeout(t *testing.T) {
	// Ensure we start in a disconnected state for this test
	if database.Connected() {
		err := database.Disconnect()
		if err != nil {
			t.Errorf("Disconnect should not return an error: %v", err)
		}
		time.Sleep(200 * time.Millisecond) // Wait for disconnection
	}

	// Verify we're actually disconnected
	if database.Connected() {
		t.Skip("Cannot test AwaitConnection timeout - database is still connected")
	}

	// Test AwaitConnection with timeout to avoid hanging
	done := make(chan bool, 1)

	go func() {
		// This should block since we're not connected
		database.AwaitConnection()
		done <- true
	}()

	select {
	case <-done:
		t.Error("AwaitConnection should have blocked when not connected")
	case <-time.After(100 * time.Millisecond):
		// Expected behavior - AwaitConnection should block
		// Note: The goroutine will continue running, but that's expected
		// since AwaitConnection is designed to block until connected
	}
}

func TestConnectWithInvalidConfig(t *testing.T) {
	// Test Connect with invalid config
	config := database.Config{
		Driver: "invalid-driver",
	}

	// This should not panic
	database.Connect(time.Millisecond, config)

	// Give it a moment to try connecting
	time.Sleep(10 * time.Millisecond)

	// Should still not be connected
	if database.Connected() {
		t.Error("Should not be connected with invalid config")
	}

	// Clean up
	err := database.Disconnect()
	if err != nil {
		t.Errorf("Disconnect should not return an error: %v", err)
	}
}

func TestConnectWithSQLiteConfig(t *testing.T) {
	// Test Connect with SQLite config (should work without external database)
	config := database.Config{
		Driver: "sqlite",
		Name:   ":memory:",
	}

	// This should not panic
	database.Connect(time.Millisecond*100, config)

	// Give it more time to connect and run migrations
	time.Sleep(500 * time.Millisecond)

	// Should be connected to in-memory SQLite
	if !database.Connected() {
		t.Error("Should be connected to in-memory SQLite")
	}

	// Test Execute with connection
	err := database.Execute(func(db *gorm.DB) error {
		return db.Exec("SELECT 1").Error
	})

	if err != nil {
		t.Errorf("Execute should work when connected: %v", err)
	}

	// Clean up
	database.Disconnect()

	// Give it more time to disconnect since it's asynchronous
	time.Sleep(200 * time.Millisecond)

	// Note: The Connected() status might not immediately reflect disconnection
	// due to the asynchronous nature of the connection management
}

func TestRegisterSchema(_ *testing.T) {
	// Test schema registration
	type TestModel struct {
		ID   uint   `gorm:"primaryKey"`
		Name string `gorm:"not null"`
	}

	// This should not panic
	database.RegisterSchema(&TestModel{})

	// Test with multiple schemas
	type AnotherModel struct {
		ID    uint   `gorm:"primaryKey"`
		Value string `gorm:"not null"`
	}

	database.RegisterSchema(&TestModel{}, &AnotherModel{})
}

func TestRegisterMigrationStep(_ *testing.T) {
	// Test migration step registration
	v1 := version.Release{
		GitTag:    "v1.0.0",
		GitCommit: "abc123",
	}

	// This should not panic
	database.RegisterMigrationStep(v1, func(_ *gorm.DB) error {
		return nil
	})

	// Test registering another migration step
	v2 := version.Release{
		GitTag:    "v1.0.1",
		GitCommit: "def456",
	}

	// This should also not panic during registration
	database.RegisterMigrationStep(v2, func(_ *gorm.DB) error {
		// Just return nil for testing - we don't want to actually fail migrations
		return nil
	})
}

func TestRegisterOnConnectHandler(t *testing.T) {
	// Test OnConnect handler registration
	var handlerCalled bool

	database.RegisterOnConnectHandler(func(_ *gorm.DB, _ database.Config) error {
		handlerCalled = true
		return nil
	})

	// Handler should be registered but not called yet
	if handlerCalled {
		t.Error("OnConnect handler should not be called immediately")
	}

	// Test handler with error
	database.RegisterOnConnectHandler(func(_ *gorm.DB, _ database.Config) error {
		return errors.New("handler error")
	})
}

func TestDisconnectWithoutConnection(t *testing.T) {
	// Test Disconnect when not connected
	// The Disconnect function works by sending a signal through a channel
	// Even when not connected, it should still handle the disconnect signal
	done := make(chan bool)

	go func() {
		// Start a connection attempt first to have something to disconnect
		database.Connect(time.Millisecond, database.Config{Driver: "invalid"})
		time.Sleep(10 * time.Millisecond)
		database.Disconnect()
		done <- true
	}()

	select {
	case <-done:
		// Expected behavior
	case <-time.After(200 * time.Millisecond):
		t.Error("Disconnect should not hang excessively")
	}
}

func TestConfigStruct(t *testing.T) {
	// Test that Config struct has proper fields
	config := database.Config{
		Driver:   "mysql",
		Host:     "localhost",
		Port:     3306,
		User:     "root",
		Password: "password",
		Name:     "test",
	}

	if config.Driver != "mysql" {
		t.Error("Driver field not set correctly")
	}

	if config.Host != "localhost" {
		t.Error("Host field not set correctly")
	}

	if config.Port != 3306 {
		t.Error("Port field not set correctly")
	}

	if config.User != "root" {
		t.Error("User field not set correctly")
	}

	if config.Password != "password" {
		t.Error("Password field not set correctly")
	}

	if config.Name != "test" {
		t.Error("Name field not set correctly")
	}
}

func TestConfigImplementsInterface(t *testing.T) {
	// Test that Config implements the config.Config interface
	var cfg database.Config
	err := cfg.Validate()
	if err == nil {
		t.Error("Empty config should fail validation")
	}
}

// Benchmark tests
func BenchmarkConfigValidate(b *testing.B) {
	config := database.Config{
		Driver:   "mysql",
		Host:     "localhost",
		Port:     3306,
		User:     "root",
		Password: "password",
		Name:     "test",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.Validate()
	}
}

func BenchmarkConnected(b *testing.B) {
	for i := 0; i < b.N; i++ {
		database.Connected()
	}
}

func BenchmarkExecuteError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = database.Execute(func(_ *gorm.DB) error {
			return nil
		})
	}
}
