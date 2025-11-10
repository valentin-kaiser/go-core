package config_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/config"
	"github.com/valentin-kaiser/go-core/flag"
)

// TestConfig implements the Config interface for testing
type TestConfig struct {
	ApplicationName string `yaml:"application_name" usage:"The name of the application"`
	ServerPort      int    `yaml:"server_port" usage:"The port to listen on"`
	EnableVerbose   bool   `yaml:"enable_verbose" usage:"Enable verbose mode"`
	DatabaseURL     string `yaml:"database_url" usage:"Database connection URL"`
}

func (c *TestConfig) Validate() error {
	if c.ApplicationName == "" {
		return errors.New("application_name cannot be empty")
	}
	if c.ServerPort <= 0 || c.ServerPort > 65535 {
		return errors.New("server_port must be between 1 and 65535")
	}
	return nil
}

// TestConfigWithError implements Config with validation error
type TestConfigWithError struct {
	ApplicationName string `yaml:"application_name"`
}

func (c *TestConfigWithError) Validate() error {
	return errors.New("always invalid")
}

func TestRegisterBasic(t *testing.T) {
	// Test nil config
	err := config.Manager().WithName("test-nil").Register(nil)
	if err == nil {
		t.Error("Register() should return error for nil config")
	}

	// Test valid config (should pass)
	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   false,
		DatabaseURL:     "sqlite:///test.db",
	}

	err = config.Manager().WithName("test-valid").Register(cfg)
	if err != nil {
		t.Errorf("Register() with valid config should succeed: %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   false,
		DatabaseURL:     "sqlite:///test.db",
	}

	// Test valid config
	err := cfg.Validate()
	if err != nil {
		t.Errorf("Valid config failed validation: %v", err)
	}

	// Test invalid config - empty name
	cfg.ApplicationName = ""
	err = cfg.Validate()
	if err == nil {
		t.Error("Config with empty name should fail validation")
	}

	// Test invalid config - invalid port
	cfg.ApplicationName = "test-app"
	cfg.ServerPort = -1
	err = cfg.Validate()
	if err == nil {
		t.Error("Config with invalid port should fail validation")
	}

	// Test invalid config - port too high
	cfg.ServerPort = 70000
	err = cfg.Validate()
	if err == nil {
		t.Error("Config with port too high should fail validation")
	}
}

func TestConfigWithErrorValidation(t *testing.T) {
	cfg := &TestConfigWithError{
		ApplicationName: "test-app",
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("TestConfigWithError should always fail validation")
	}
}

func TestConfigInterface(_ *testing.T) {
	// Test that our test configs implement the Config interface
	var _ config.Config = &TestConfig{}
	var _ config.Config = &TestConfigWithError{}
}

func TestWriteWithNilConfig(t *testing.T) {
	err := config.Write(nil)
	if err == nil {
		t.Error("Write() should return error for nil config")
	}
}

func TestFileOperations(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Save original flag.Path
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()

	flag.Path = tempDir

	// Test that the temp directory exists
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Errorf("Temp directory %s does not exist", tempDir)
	}

	// Test that flag.Path is set correctly
	if flag.Path != tempDir {
		t.Errorf("flag.Path is %s, expected %s", flag.Path, tempDir)
	}

	// Test creating directory structure
	subDir := filepath.Join(tempDir, "subdir")
	err := os.MkdirAll(subDir, 0750)
	if err != nil {
		t.Errorf("Failed to create subdirectory: %v", err)
	}

	// Test file creation
	testFile := filepath.Join(tempDir, "test.yaml")
	err = os.WriteFile(testFile, []byte("test: value"), 0600)
	if err != nil {
		t.Errorf("Failed to create test file: %v", err)
	}

	// Test file reading
	content, err := os.ReadFile(filepath.Clean(testFile))
	if err != nil {
		t.Errorf("Failed to read test file: %v", err)
	}

	if string(content) != "test: value" {
		t.Errorf("File content is %s, expected 'test: value'", string(content))
	}
}

func TestGetWithoutRegistration(_ *testing.T) {
	// Test Get() when no config is registered
	// This should return nil or the previously registered config
	result := config.Get()
	// We can't make strong assertions here since the config package
	// maintains global state and other tests might have registered configs
	_ = result
}

func TestPackageConstants(_ *testing.T) {
	// Test that we can access package-level functions
	_ = config.Get()

	// Test that we can call OnChange (should not panic)
	config.OnChange(func(_, _ config.Config) error {
		return nil
	})
}

// Test concurrent access safety
func TestConcurrentAccess(_ *testing.T) {
	done := make(chan bool, 10)

	// Test concurrent Get operations
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			config := config.Get()
			_ = config // Use the config to avoid compiler optimization
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Test that the config structs work with YAML tags
func TestYAMLTags(t *testing.T) {
	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	// The actual YAML marshaling is handled by the config package
	// Here we just test that the struct is properly defined
	if cfg.ApplicationName != "test-app" {
		t.Error("ApplicationName field not properly set")
	}

	if cfg.ServerPort != 8080 {
		t.Error("ServerPort field not properly set")
	}

	if !cfg.EnableVerbose {
		t.Error("EnableVerbose field not properly set")
	}

	if cfg.DatabaseURL != "sqlite:///test.db" {
		t.Error("DatabaseURL field not properly set")
	}
}

// Test flag usage tags
func TestUsageTags(t *testing.T) {
	// Test that our struct has proper usage tags
	// This is mainly a compile-time check
	cfg := &TestConfig{}

	// Test that validation works
	err := cfg.Validate()
	if err == nil {
		t.Error("Empty config should fail validation")
	}

	// Test with valid values
	cfg.ApplicationName = "test"
	cfg.ServerPort = 8080
	err = cfg.Validate()
	if err != nil {
		t.Errorf("Valid config should pass validation: %v", err)
	}
}

// Test struct with nested fields
type NestedConfig struct {
	Server   ServerConfig   `yaml:"server" usage:"Server configuration"`
	Database DatabaseConfig `yaml:"database" usage:"Database configuration"`
}

func (c *NestedConfig) Validate() error {
	if err := c.Server.Validate(); err != nil {
		return err
	}
	return c.Database.Validate()
}

type ServerConfig struct {
	Host string `yaml:"host" usage:"Server host"`
	Port int    `yaml:"port" usage:"Server port"`
}

func (c *ServerConfig) Validate() error {
	if c.Host == "" {
		return errors.New("host cannot be empty")
	}
	if c.Port <= 0 {
		return errors.New("port must be positive")
	}
	return nil
}

type DatabaseConfig struct {
	URL     string `yaml:"url" usage:"Database URL"`
	Timeout int    `yaml:"timeout" usage:"Connection timeout"`
}

func (c *DatabaseConfig) Validate() error {
	if c.URL == "" {
		return errors.New("url cannot be empty")
	}
	return nil
}

// Test struct with pointers
type PointerConfig struct {
	Server   *ServerConfig   `yaml:"server" usage:"Server configuration"`
	Database *DatabaseConfig `yaml:"database" usage:"Database configuration"`
}

func (c *PointerConfig) Validate() error {
	if c.Server != nil {
		if err := c.Server.Validate(); err != nil {
			return err
		}
	}
	if c.Database != nil {
		return c.Database.Validate()
	}
	return nil
}

// UniquePointerConfig with different field names to avoid flag conflicts
type UniquePointerConfig struct {
	AppServer   *ServerConfig   `yaml:"appserver" usage:"Application server configuration"`
	AppDatabase *DatabaseConfig `yaml:"appdatabase" usage:"Application database configuration"`
}

func (c *UniquePointerConfig) Validate() error {
	if c.AppServer != nil {
		if err := c.AppServer.Validate(); err != nil {
			return err
		}
	}
	if c.AppDatabase != nil {
		return c.AppDatabase.Validate()
	}
	return nil
}

// Test struct with various types
type ComplexConfig struct {
	StringVal   string   `yaml:"string_val" usage:"String value"`
	IntVal      int      `yaml:"int_val" usage:"Int value"`
	UintVal     uint     `yaml:"uint_val" usage:"Uint value"`
	Int8Val     int8     `yaml:"int8_val" usage:"Int8 value"`
	Uint8Val    uint8    `yaml:"uint8_val" usage:"Uint8 value"`
	Int16Val    int16    `yaml:"int16_val" usage:"Int16 value"`
	Uint16Val   uint16   `yaml:"uint16_val" usage:"Uint16 value"`
	Int32Val    int32    `yaml:"int32_val" usage:"Int32 value"`
	Uint32Val   uint32   `yaml:"uint32_val" usage:"Uint32 value"`
	Int64Val    int64    `yaml:"int64_val" usage:"Int64 value"`
	Uint64Val   uint64   `yaml:"uint64_val" usage:"Uint64 value"`
	Float32Val  float32  `yaml:"float32_val" usage:"Float32 value"`
	Float64Val  float64  `yaml:"float64_val" usage:"Float64 value"`
	BoolVal     bool     `yaml:"bool_val" usage:"Bool value"`
	StringSlice []string `yaml:"string_slice" usage:"String slice"`
}

func (c *ComplexConfig) Validate() error {
	return nil
}

// Non-struct type for error testing
type StringConfig string

func (c StringConfig) Validate() error {
	return nil
}

func TestRegisterWithNonStruct(t *testing.T) {
	var cfg StringConfig = "test"
	// Since StringConfig doesn't have a pointer receiver for Validate,
	// we need to pass a pointer to it to test the non-struct error
	err := config.Manager().WithName("string-config").Register(&cfg)
	if err == nil {
		t.Error("Register() should return error for non-struct type")
	}
}

func TestRegisterWithNonPointer(t *testing.T) {
	cfg := TestConfig{}
	// This test is actually checking that we get a compile-time error
	// when trying to pass a non-pointer struct that implements Config with pointer receiver
	// We'll test this by trying to register a value instead of pointer
	_ = cfg // Just to use the variable

	// Instead, test with an interface value that's not a pointer to struct
	var iface interface{} = "not a struct"
	if c, ok := iface.(config.Config); ok {
		err := config.Manager().WithName("non-pointer").Register(c)
		if err == nil {
			t.Error("Register() should return error for non-pointer")
		}
	}
}

func TestRegisterWithNestedStructs(t *testing.T) {
	cfg := &NestedConfig{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		Database: DatabaseConfig{
			URL:     "postgres://localhost",
			Timeout: 30,
		},
	}

	err := config.Manager().WithName("nested-config").Register(cfg)
	if err != nil {
		t.Errorf("Register() with nested structs should succeed: %v", err)
	}
}

// PointerFieldsTestConfig for testing pointer fields without flag conflicts
type PointerFieldsTestConfig struct {
	PtrServer   *ServerConfig   `yaml:"ptrserver" usage:"Pointer server configuration"`
	PtrDatabase *DatabaseConfig `yaml:"ptrdatabase" usage:"Pointer database configuration"`
}

func (c *PointerFieldsTestConfig) Validate() error {
	if c.PtrServer != nil {
		if err := c.PtrServer.Validate(); err != nil {
			return err
		}
	}
	if c.PtrDatabase != nil {
		return c.PtrDatabase.Validate()
	}
	return nil
}

func TestRegisterWithPointerFields(t *testing.T) {
	cfg := &PointerFieldsTestConfig{
		PtrServer: &ServerConfig{
			Host: "pointer-host",
			Port: 9999,
		},
		PtrDatabase: &DatabaseConfig{
			URL:     "postgres://pointer-localhost",
			Timeout: 35,
		},
	}

	err := config.Manager().WithName("pointer-config-test-3").Register(cfg)
	if err != nil {
		t.Errorf("Register() with pointer fields should succeed: %v", err)
	}
}

// NilPointerTestConfig for testing nil pointer fields without flag conflicts
type NilPointerTestConfig struct {
	NilServer   *ServerConfig   `yaml:"nilserver" usage:"Nil server configuration"`
	NilDatabase *DatabaseConfig `yaml:"nildatabase" usage:"Nil database configuration"`
}

func (c *NilPointerTestConfig) Validate() error {
	if c.NilServer != nil {
		if err := c.NilServer.Validate(); err != nil {
			return err
		}
	}
	if c.NilDatabase != nil {
		return c.NilDatabase.Validate()
	}
	return nil
}

func TestRegisterWithNilPointerFields(t *testing.T) {
	cfg := &NilPointerTestConfig{
		NilServer:   nil,
		NilDatabase: nil,
	}

	err := config.Manager().WithName("nil-pointer-config").Register(cfg)
	if err != nil {
		t.Errorf("Register() with nil pointer fields should succeed: %v", err)
	}
}

func TestRegisterWithComplexTypes(t *testing.T) {
	cfg := &ComplexConfig{
		StringVal:   "test",
		IntVal:      42,
		UintVal:     42,
		Int8Val:     8,
		Uint8Val:    8,
		Int16Val:    16,
		Uint16Val:   16,
		Int32Val:    32,
		Uint32Val:   32,
		Int64Val:    64,
		Uint64Val:   64,
		Float32Val:  3.14,
		Float64Val:  3.14159,
		BoolVal:     true,
		StringSlice: []string{"a", "b", "c"},
	}

	err := config.Manager().WithName("complex-config").Register(cfg)
	if err != nil {
		t.Errorf("Register() with complex types should succeed: %v", err)
	}
}

func TestReadConfigFileOperations(t *testing.T) {
	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithName("read-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Test Read() when config file doesn't exist
	err = config.Read()
	if err != nil {
		t.Errorf("Read() should create config file if it doesn't exist: %v", err)
	}

	// Verify config file was created
	configPath := filepath.Join(tempDir, "read-test.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file should have been created")
	}

	// Test Read() when config file exists
	err = config.Read()
	if err != nil {
		t.Errorf("Read() should succeed when config file exists: %v", err)
	}
}

func TestWriteConfig(t *testing.T) {
	defer config.Reset() // Ensure clean state for other tests

	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("write-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Test Write() with valid config
	newCfg := &TestConfig{
		ApplicationName: "updated-app",
		ServerPort:      9090,
		EnableVerbose:   false,
		DatabaseURL:     "postgres://localhost",
	}

	err = config.Write(newCfg)
	if err != nil {
		t.Errorf("Write() should succeed with valid config: %v", err)
	}

	// Verify the config was updated in memory by Write()
	current, ok := config.Get().(*TestConfig)
	if !ok {
		t.Fatal("Expected config to be *TestConfig")
	}
	if current.ApplicationName != "updated-app" {
		t.Error("Write() should have updated the in-memory config")
	}

	// Verify the file was written correctly
	configFile := filepath.Join(tempDir, "write-test.yaml")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		t.Error("Write() should have created the config file")
	}
}

func TestWriteConfigWithInvalidConfig(t *testing.T) {
	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithName("write-invalid-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Test Write() with invalid config
	invalidCfg := &TestConfig{
		ApplicationName: "", // Invalid
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err = config.Write(invalidCfg)
	if err == nil {
		t.Error("Write() should fail with invalid config")
	}
}

func TestOnChangeCallbacks(t *testing.T) {
	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("onchange-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	callbackCalled := false
	var oldConfig, newConfig config.Config
	config.OnChange(func(o, n config.Config) error {
		callbackCalled = true
		oldConfig = o
		newConfig = n
		return nil
	})

	// Trigger a change via Write() - this should now call onChange handlers
	newCfg := &TestConfig{
		ApplicationName: "updated-app",
		ServerPort:      9090,
		EnableVerbose:   false,
		DatabaseURL:     "postgres://localhost",
	}

	err = config.Write(newCfg)
	if err != nil {
		t.Errorf("Write() failed: %v", err)
	}

	if !callbackCalled {
		t.Error("OnChange callback should have been called during Write()")
	}

	// Verify the callback received the correct old and new configs
	if oldConfig == nil {
		t.Error("Old config should not be nil")
	}
	if newConfig == nil {
		t.Error("New config should not be nil")
	}

	// Verify that the in-memory config was updated
	current := config.Get().(*TestConfig)
	if current.ApplicationName != "updated-app" {
		t.Error("In-memory config should have been updated by Write()")
	}
}

func TestOnChangeCallbackError(t *testing.T) {
	defer config.Reset()
	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("onchange-error-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	config.OnChange(func(_, _ config.Config) error {
		return errors.New("callback error")
	})

	// Trigger a change via Write() - this should now call onChange handlers and fail
	newCfg := &TestConfig{
		ApplicationName: "updated-app",
		ServerPort:      9090,
		EnableVerbose:   false,
		DatabaseURL:     "postgres://localhost",
	}

	err = config.Write(newCfg)
	if err == nil {
		t.Error("Write() should fail when callback returns error")
	}
}

func TestBasicConfigOperations(t *testing.T) {
	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "test-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("basic-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Create initial config file
	err = config.Read()
	if err != nil {
		t.Fatalf("Read() failed: %v", err)
	}

	// Give time for setup
	time.Sleep(100 * time.Millisecond)

	// Note: This test mainly ensures the basic functionality doesn't panic
	// File watching has been removed from the config package
}

func TestConcurrentConfigOperations(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &TestConfig{
		ApplicationName: "concurrent-test",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("concurrent-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	// Test concurrent reads - this should be safe
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			config := config.Get()
			if config == nil {
				errs <- errors.New("Get() returned nil")
			}
		}()
	}

	wg.Wait()

	// Test sequential writes to avoid file conflicts
	// (concurrent writes to the same config file can cause issues)
	for i := 0; i < 5; i++ {
		newCfg := &TestConfig{
			ApplicationName: fmt.Sprintf("concurrent-app-%d", i),
			ServerPort:      8080 + i,
			EnableVerbose:   true,
			DatabaseURL:     "sqlite:///test.db",
		}
		if err := config.Write(newCfg); err != nil {
			errs <- err
		}
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("Concurrent operation failed: %v", err)
	}
}

func TestConfigFilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "permissions-test",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("permissions-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	err = config.Read()
	if err != nil {
		t.Fatalf("Read() failed: %v", err)
	}

	// Check file permissions
	configPath := filepath.Join(tempDir, "permissions-test.yaml")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("Failed to stat config file: %v", err)
	}

	if runtime.GOOS != "windows" {
		// Config files should be readable and writable by owner only (0600)
		expectedMode := os.FileMode(0600)
		if info.Mode().Perm() != expectedMode {
			t.Errorf("Config file permissions are %v, expected %v", info.Mode().Perm(), expectedMode)
		}
	}
}

func TestConfigDirectoryCreation(t *testing.T) {
	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()

	// Set path to a non-existent subdirectory
	subDir := filepath.Join(tempDir, "config", "subdir")
	flag.Path = subDir

	cfg := &TestConfig{
		ApplicationName: "directory-test",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(subDir).WithName("directory-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	err = config.Read()
	if err != nil {
		t.Fatalf("Read() should create directory and config file: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(subDir); os.IsNotExist(err) {
		t.Error("Config directory should have been created")
	}

	// Verify config file was created
	configPath := filepath.Join(subDir, "directory-test.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file should have been created in subdirectory")
	}
}

// Benchmark tests
func BenchmarkRegister(b *testing.B) {
	for i := 0; i < b.N; i++ {
		cfg := &TestConfig{
			ApplicationName: "benchmark-test",
			ServerPort:      8080,
			EnableVerbose:   false,
			DatabaseURL:     "sqlite:///test.db",
		}
		if err := config.Manager().WithName(fmt.Sprintf("benchmark-%d", i)).Register(cfg); err != nil {
			b.Logf("Failed to register config: %v", err)
		}
	}
}

func BenchmarkWrite(b *testing.B) {
	tempDir := b.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "benchmark-write",
		ServerPort:      8080,
		EnableVerbose:   false,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("benchmark-write").Register(cfg)
	if err != nil {
		b.Fatalf("Register failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newCfg := &TestConfig{
			ApplicationName: fmt.Sprintf("benchmark-app-%d", i),
			ServerPort:      8080 + i,
			EnableVerbose:   i%2 == 0,
			DatabaseURL:     "sqlite:///test.db",
		}
		if err := config.Write(newCfg); err != nil {
			b.Logf("Failed to write config: %v", err)
		}
	}
}

func BenchmarkRead(b *testing.B) {
	tempDir := b.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "benchmark-read",
		ServerPort:      8080,
		EnableVerbose:   false,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("benchmark-read").Register(cfg)
	if err != nil {
		b.Fatalf("Register failed: %v", err)
	}

	// Create initial config file
	err = config.Read()
	if err != nil {
		b.Fatalf("Initial Read() failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := config.Read(); err != nil {
			b.Logf("Failed to read config: %v", err)
		}
	}
}

func TestReadAlwaysLoadsFromDisk(t *testing.T) {
	defer config.Reset() // Ensure clean state for other tests

	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "initial-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("read-disk-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Create initial config file with Read()
	err = config.Read()
	if err != nil {
		t.Fatalf("Initial Read() failed: %v", err)
	}

	// Verify initial state
	current := config.Get().(*TestConfig)
	if current.ApplicationName != "initial-app" {
		t.Errorf("Expected initial state to be 'initial-app', got %s", current.ApplicationName)
	}

	// Manually modify the file on disk
	configFile := filepath.Join(tempDir, "read-disk-test.yaml")
	manualYAML := `application_name: "disk-modified"
server_port: 7777
enable_verbose: false
database_url: "modified://disk"`

	err = os.WriteFile(configFile, []byte(manualYAML), 0600)
	if err != nil {
		t.Fatalf("Failed to write modified config to disk: %v", err)
	}

	// Read() again - this should load the modified config from disk
	err = config.Read()
	if err != nil {
		t.Fatalf("Read() after manual modification failed: %v", err)
	}

	// Verify that Read() loaded the changes from disk
	current = config.Get().(*TestConfig)
	if current.ApplicationName != "disk-modified" {
		t.Errorf("Read() should load from disk, expected 'disk-modified', got %s", current.ApplicationName)
	}
	if current.ServerPort != 7777 {
		t.Errorf("Read() should load from disk, expected port 7777, got %d", current.ServerPort)
	}
	if current.EnableVerbose != false {
		t.Errorf("Read() should load from disk, expected EnableVerbose=false, got %v", current.EnableVerbose)
	}
}

func TestWriteTriggersOnChange(t *testing.T) {
	defer config.Reset() // Ensure clean state for other tests

	tempDir := t.TempDir()
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()
	flag.Path = tempDir

	cfg := &TestConfig{
		ApplicationName: "initial-app",
		ServerPort:      8080,
		EnableVerbose:   true,
		DatabaseURL:     "sqlite:///test.db",
	}

	err := config.Manager().WithPath(tempDir).WithName("write-onchange-test").Register(cfg)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	callbackCalled := false
	var receivedOldConfig, receivedNewConfig config.Config

	// Register onChange handler
	config.OnChange(func(oldConf, newConf config.Config) error {
		callbackCalled = true
		receivedOldConfig = oldConf
		receivedNewConfig = newConf
		return nil
	})

	// Write a new config - this should trigger the onChange handler
	newCfg := &TestConfig{
		ApplicationName: "updated-by-write",
		ServerPort:      9090,
		EnableVerbose:   false,
		DatabaseURL:     "postgres://localhost",
	}

	err = config.Write(newCfg)
	if err != nil {
		t.Errorf("Write() should succeed: %v", err)
	}

	// Verify that onChange was called
	if !callbackCalled {
		t.Error("Write() should have triggered onChange handlers")
	}

	// Verify the callback received the correct configs
	if receivedOldConfig == nil {
		t.Error("onChange handler should receive old config")
	} else {
		oldTyped := receivedOldConfig.(*TestConfig)
		if oldTyped.ApplicationName != "initial-app" {
			t.Errorf("onChange old config should be 'initial-app', got %s", oldTyped.ApplicationName)
		}
	}

	if receivedNewConfig == nil {
		t.Error("onChange handler should receive new config")
	} else {
		newTyped := receivedNewConfig.(*TestConfig)
		if newTyped.ApplicationName != "updated-by-write" {
			t.Errorf("onChange new config should be 'updated-by-write', got %s", newTyped.ApplicationName)
		}
	}

	// Verify in-memory config was updated
	current := config.Get().(*TestConfig)
	if current.ApplicationName != "updated-by-write" {
		t.Errorf("In-memory config should be updated by Write(), got %s", current.ApplicationName)
	}
}
