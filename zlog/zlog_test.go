package zlog_test

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/zlog"
)

func TestLogger(t *testing.T) {
	logger := zlog.Logger()
	if logger == nil {
		t.Error("Logger() returned nil")
	}

	// Test singleton pattern
	logger2 := zlog.Logger()
	if logger != logger2 {
		t.Error("Logger() should return singleton instance")
	}
}

func TestLoggerInit(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Save original flag.Path
	originalPath := flag.Path
	t.Cleanup(func() { flag.Path = originalPath })

	flag.Path = tempDir

	testCases := []struct {
		name     string
		logname  string
		level    zerolog.Level
		expected string
	}{
		{"with .log extension", "test.log", zerolog.InfoLevel, "test.log"},
		{"without .log extension", "test", zerolog.DebugLevel, "test.log"},
		{"empty name", "", zerolog.ErrorLevel, ".log"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := zlog.Logger()
			logger.Init(tc.logname, tc.level)

			if zerolog.GlobalLevel() != tc.level {
				t.Errorf("Expected global level %v, got %v", tc.level, zerolog.GlobalLevel())
			}
		})
	}
}

func TestLoggerWithConsole(t *testing.T) {
	zlog.SetModeDetector(func() bool {
		return true
	})

	logger := zlog.Logger()
	initialOutputs := len(logger.GetOutputs())

	result := logger.WithConsole()

	if result != logger {
		t.Error("WithConsole() should return self for chaining")
	}

	if len(logger.GetOutputs()) != initialOutputs+1 {
		t.Error("WithConsole() should add one output")
	}
}

func TestLoggerWithLogFile(t *testing.T) {
	logger := zlog.Logger()
	initialOutputs := len(logger.GetOutputs())

	result := logger.WithLogFile()

	if result != logger {
		t.Error("WithLogFile() should return self for chaining")
	}

	if len(logger.GetOutputs()) != initialOutputs+1 {
		t.Error("WithLogFile() should add one output")
	}

	if logger.GetFile() == nil {
		t.Error("WithLogFile() should set file property")
	}

	// Check default values
	if logger.GetFile().MaxSize != 10 {
		t.Errorf("Expected MaxSize 10, got %d", logger.GetFile().MaxSize)
	}
	if logger.GetFile().MaxAge != 28 {
		t.Errorf("Expected MaxAge 28, got %d", logger.GetFile().MaxAge)
	}
	if logger.GetFile().MaxBackups != 10 {
		t.Errorf("Expected MaxBackups 10, got %d", logger.GetFile().MaxBackups)
	}
	if !logger.GetFile().Compress {
		t.Error("Expected Compress to be true")
	}
}

func TestLoggerWith(t *testing.T) {
	logger := zlog.Logger()
	initialOutputs := len(logger.GetOutputs())

	var buffer bytes.Buffer
	result := logger.With(&buffer)

	if result != logger {
		t.Error("With() should return self for chaining")
	}

	if len(logger.GetOutputs()) != initialOutputs+1 {
		t.Error("With() should add one output")
	}

	// Test with multiple writers
	var buffer2 bytes.Buffer
	var buffer3 bytes.Buffer
	logger.With(&buffer2, &buffer3)

	if len(logger.GetOutputs()) != initialOutputs+3 {
		t.Error("With() should add all provided outputs")
	}
}

func TestLoggerSetLevel(t *testing.T) {
	logger := zlog.Logger()

	testLevels := []zerolog.Level{
		zerolog.DebugLevel,
		zerolog.InfoLevel,
		zerolog.WarnLevel,
		zerolog.ErrorLevel,
		zerolog.FatalLevel,
	}

	for _, level := range testLevels {
		result := logger.SetLevel(level)

		if result != logger {
			t.Error("SetLevel() should return self for chaining")
		}

		if logger.GetLevel() != level {
			t.Errorf("Expected level %v, got %v", level, logger.GetLevel())
		}

		if zerolog.GlobalLevel() != level {
			t.Errorf("Expected global level %v, got %v", level, zerolog.GlobalLevel())
		}
	}
}

func TestLoggerWithLevel(t *testing.T) {
	logger := zlog.Logger()
	originalLevel := logger.GetLevel()

	newLogger := logger.WithLevel(zerolog.DebugLevel)

	if newLogger == logger {
		t.Error("WithLevel() should return new logger instance")
	}

	if newLogger.GetLevel() != zerolog.DebugLevel {
		t.Errorf("Expected new logger level %v, got %v", zerolog.DebugLevel, newLogger.GetLevel())
	}

	// Original logger should be unchanged
	if logger.GetLevel() != originalLevel {
		t.Error("WithLevel() should not modify original logger")
	}
}

func TestLoggerFileOperations(t *testing.T) {
	logger := zlog.Logger().WithLogFile()

	// Test SetMaxSize
	result := logger.SetMaxSize(50)
	if result != logger {
		t.Error("SetMaxSize() should return self for chaining")
	}
	if logger.GetFile().MaxSize != 50 {
		t.Errorf("Expected MaxSize 50, got %d", logger.GetFile().MaxSize)
	}

	// Test SetMaxAge
	result = logger.SetMaxAge(7)
	if result != logger {
		t.Error("SetMaxAge() should return self for chaining")
	}
	if logger.GetFile().MaxAge != 7 {
		t.Errorf("Expected MaxAge 7, got %d", logger.GetFile().MaxAge)
	}

	// Test SetMaxBackups
	result = logger.SetMaxBackups(5)
	if result != logger {
		t.Error("SetMaxBackups() should return self for chaining")
	}
	if logger.GetFile().MaxBackups != 5 {
		t.Errorf("Expected MaxBackups 5, got %d", logger.GetFile().MaxBackups)
	}

	// Test SetCompress
	result = logger.SetCompress(false)
	if result != logger {
		t.Error("SetCompress() should return self for chaining")
	}
	if logger.GetFile().Compress != false {
		t.Errorf("Expected Compress false, got %v", logger.GetFile().Compress)
	}
}

func TestLoggerFileOperationsWithoutFile(t *testing.T) {
	logger := zlog.Logger()
	// Don't call WithLogFile()

	// All file operations should be no-ops when file is nil
	result := logger.SetMaxSize(50)
	if result != logger {
		t.Error("SetMaxSize() should return self even without file")
	}

	result = logger.SetMaxAge(7)
	if result != logger {
		t.Error("SetMaxAge() should return self even without file")
	}

	result = logger.SetMaxBackups(5)
	if result != logger {
		t.Error("SetMaxBackups() should return self even without file")
	}

	result = logger.SetCompress(false)
	if result != logger {
		t.Error("SetCompress() should return self even without file")
	}
}

func TestLoggerGetPath(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Save original flag.Path
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()

	flag.Path = tempDir

	logger := zlog.Logger()

	// Test without file
	path := logger.GetPath()
	if path != "" {
		t.Errorf("Expected empty path without file, got %s", path)
	}

	// Test with file
	logger.WithLogFile()
	logger.Init("test.log", zerolog.InfoLevel)

	path = logger.GetPath()
	expectedPath := filepath.Join(tempDir, "test.log")
	if path != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, path)
	}
}

func TestLoggerWrite(t *testing.T) {
	logger := zlog.Logger()

	// Test Write interface
	testMessage := "test log message"
	n, err := logger.Write([]byte(testMessage))

	if err != nil {
		t.Errorf("Write() returned error: %v", err)
	}

	if n != len(testMessage) {
		t.Errorf("Write() returned %d bytes, expected %d", n, len(testMessage))
	}
}

func TestLoggerWriteWithLevel(t *testing.T) {
	logger := zlog.Logger().WithLevel(zerolog.WarnLevel)

	testMessage := "test warning message"
	n, err := logger.Write([]byte(testMessage))

	if err != nil {
		t.Errorf("Write() returned error: %v", err)
	}

	if n != len(testMessage) {
		t.Errorf("Write() returned %d bytes, expected %d", n, len(testMessage))
	}
}

func TestLoggerStop(_ *testing.T) {
	logger := zlog.Logger()

	// Test Stop without file (should not panic)
	logger.Stop()

	// Test Stop with file
	logger.WithLogFile()
	logger.Stop()
}

func TestLoggerRotate(_ *testing.T) {
	logger := zlog.Logger().WithLogFile()

	// Test Rotate (should not panic)
	logger.Rotate()
}

func TestLoggerChaining(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Save original flag.Path
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()

	flag.Path = tempDir

	var buffer bytes.Buffer

	// Test method chaining
	logger := zlog.Logger().
		WithConsole().
		WithLogFile().
		With(&buffer).
		SetLevel(zerolog.WarnLevel).
		SetMaxSize(25).
		SetMaxAge(14).
		SetMaxBackups(3).
		SetCompress(true)

	// Verify all operations were applied
	if logger.GetLevel() != zerolog.WarnLevel {
		t.Error("Chained SetLevel() not applied")
	}

	if logger.GetFile() == nil {
		t.Error("Chained WithLogFile() not applied")
	}

	if logger.GetFile().MaxSize != 25 {
		t.Error("Chained SetMaxSize() not applied")
	}

	if logger.GetFile().MaxAge != 14 {
		t.Error("Chained SetMaxAge() not applied")
	}

	if logger.GetFile().MaxBackups != 3 {
		t.Error("Chained SetMaxBackups() not applied")
	}

	if !logger.GetFile().Compress {
		t.Error("Chained SetCompress() not applied")
	}

	// Should have console + file + buffer outputs
	if len(logger.GetOutputs()) < 3 {
		t.Error("Chained With() operations not applied")
	}
}

func TestLoggerImplementsWriter(_ *testing.T) {
	var _ io.Writer = zlog.Logger()
}

// Integration test
func TestLoggerIntegration(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Save original flag.Path
	originalPath := flag.Path
	defer func() { flag.Path = originalPath }()

	flag.Path = tempDir

	// Create a buffer to capture output
	var buffer bytes.Buffer

	// Initialize logger
	logger := zlog.Logger().
		WithConsole().
		WithLogFile().
		With(&buffer).
		SetLevel(zerolog.InfoLevel)

	logger.Init("integration_test.log", zerolog.InfoLevel)

	// Test writing
	testMessage := "integration test message"
	n, err := logger.Write([]byte(testMessage))

	if err != nil {
		t.Errorf("Integration test Write() failed: %v", err)
	}

	if n != len(testMessage) {
		t.Errorf("Integration test Write() returned %d bytes, expected %d", n, len(testMessage))
	}

	// Verify log file was created
	logPath := logger.GetPath()
	expectedPath := filepath.Join(tempDir, "integration_test.log")
	if logPath != expectedPath {
		t.Errorf("Expected log path %s, got %s", expectedPath, logPath)
	}

	// Clean up
	logger.Stop()
}

// Test that the package-level functions exist
func TestPackageLevelFunctions(t *testing.T) {
	// Test that we can import and use the package
	if zlog.Logger() == nil {
		t.Error("Package-level zlog.Logger() function not working")
	}
}

// Test edge cases
func TestLoggerEdgeCases(t *testing.T) {
	logger := zlog.Logger()

	// Test Write with empty slice
	n, err := logger.Write([]byte{})
	if err != nil {
		t.Errorf("Write() with empty slice returned error: %v", err)
	}
	if n != 0 {
		t.Errorf("Write() with empty slice returned %d bytes, expected 0", n)
	}

	// Test Write with whitespace
	n, err = logger.Write([]byte("   \n  "))
	if err != nil {
		t.Errorf("Write() with whitespace returned error: %v", err)
	}
	if n != 6 {
		t.Errorf("Write() with whitespace returned %d bytes, expected 6", n)
	}
}

// Benchmark tests
func BenchmarkLoggerWrite(b *testing.B) {
	logger := zlog.Logger()
	message := []byte("benchmark test message")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := logger.Write(message)
		if err != nil {
			b.Logf("Failed to write log: %v", err)
		}
	}
}

func BenchmarkLoggerWithLevel(b *testing.B) {
	logger := zlog.Logger()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.WithLevel(zerolog.InfoLevel)
	}
}

func BenchmarkLoggerChaining(b *testing.B) {
	var buffer bytes.Buffer

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		zlog.Logger().
			WithConsole().
			WithLogFile().
			With(&buffer).
			SetLevel(zerolog.InfoLevel)
	}
}
