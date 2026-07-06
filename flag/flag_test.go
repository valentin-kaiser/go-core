package flag_test

import (
	"os"
	"testing"

	"github.com/spf13/pflag"
	"github.com/valentin-kaiser/go-core/flag"
)

func TestDefaultFlags(t *testing.T) {
	// Test that default flags are properly initialized
	if flag.Path != flag.DefaultDataPath() {
		t.Errorf("Expected default Path to be '%s', got '%s'", flag.DefaultDataPath(), flag.Path)
	}

	if flag.Help != false {
		t.Errorf("Expected default Help to be false, got %v", flag.Help)
	}

	if flag.Version != false {
		t.Errorf("Expected default Version to be false, got %v", flag.Version)
	}

	if flag.Debug != false {
		t.Errorf("Expected default Debug to be false, got %v", flag.Debug)
	}
}

func TestRegisterFlag(_ *testing.T) {
	// Test registering a string flag
	var stringFlag string
	flag.Register("test-string", &stringFlag, "A test string flag")

	// Test registering a bool flag
	var boolFlag bool
	flag.Register("test-bool", &boolFlag, "A test bool flag")

	// Test registering an int flag
	var intFlag int
	flag.Register("test-int", &intFlag, "A test int flag")

	// Test registering various numeric types
	var int8Flag int8
	flag.Register("test-int8", &int8Flag, "A test int8 flag")

	var int16Flag int16
	flag.Register("test-int16", &int16Flag, "A test int16 flag")

	var int32Flag int32
	flag.Register("test-int32", &int32Flag, "A test int32 flag")

	var int64Flag int64
	flag.Register("test-int64", &int64Flag, "A test int64 flag")

	var uintFlag uint
	flag.Register("test-uint", &uintFlag, "A test uint flag")

	var uint8Flag uint8
	flag.Register("test-uint8", &uint8Flag, "A test uint8 flag")

	var uint16Flag uint16
	flag.Register("test-uint16", &uint16Flag, "A test uint16 flag")

	var uint32Flag uint32
	flag.Register("test-uint32", &uint32Flag, "A test uint32 flag")

	var uint64Flag uint64
	flag.Register("test-uint64", &uint64Flag, "A test uint64 flag")

	var float32Flag float32
	flag.Register("test-float32", &float32Flag, "A test float32 flag")

	var float64Flag float64
	flag.Register("test-float64", &float64Flag, "A test float64 flag")
}

func TestRegisterFlagPanics(t *testing.T) {
	// Test that registering a duplicate flag panics
	var testFlag string
	flag.Register("unique-flag", &testFlag, "A unique flag")

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when registering duplicate flag")
		}
	}()
	flag.Register("unique-flag", &testFlag, "A duplicate flag")
}

func TestRegisterFlagNonPointer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when registering non-pointer flag")
		}
	}()
	var testFlag string
	flag.Register("non-pointer", testFlag, "A non-pointer flag")
}

func TestRegisterFlagNilPointer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when registering nil pointer flag")
		}
	}()
	var testFlag *string
	flag.Register("nil-pointer", testFlag, "A nil pointer flag")
}

func TestRegisterFlagUnsupportedType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when registering unsupported type")
		}
	}()
	var testFlag []string
	flag.Register("unsupported", &testFlag, "An unsupported type flag")
}

func TestInit(_ *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test normal initialization
	os.Args = []string{"program"}
	flag.Init()
	// Should not panic or exit

	// Test with help flag (we can't easily test the exit behavior)
	// This is mainly to ensure the function runs without error
	flag.Help = false // Reset to false
	flag.Init()
}

// Test flag registration with default values
func TestRegisterFlagWithDefaults(_ *testing.T) {
	var stringFlag = "default"
	flag.Register("default-string", &stringFlag, "A string flag with default")

	var intFlag = 42
	flag.Register("default-int", &intFlag, "An int flag with default")

	var boolFlag = true
	flag.Register("default-bool", &boolFlag, "A bool flag with default")

	var float64Flag = 3.14
	flag.Register("default-float64", &float64Flag, "A float64 flag with default")
}

// Test integration with actual command line parsing
func TestCommandLineIntegration(_ *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test with command line arguments
	var testString string
	var testInt int
	var testBool bool

	flag.Register("integration-string", &testString, "Integration test string")
	flag.Register("integration-int", &testInt, "Integration test int")
	flag.Register("integration-bool", &testBool, "Integration test bool")

	// Simulate command line arguments
	os.Args = []string{
		"program",
		"--integration-string=hello",
		"--integration-int=123",
		"--integration-bool=true",
	}

	flag.Init()

	// Note: The actual parsing depends on pflag being properly set up
	// These tests mainly ensure the registration doesn't break
}

// Test that flags are properly bound to pflag
func TestFlagBinding(_ *testing.T) {
	var testFlag string
	flag.Register("binding-test", &testFlag, "A binding test flag")

	// This mainly tests that the function completes without error
	// Actual binding verification would require more complex setup
}

func TestOverrideFlag(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// First register a flag
	var originalFlag string = "original"
	flag.Register("override-test", &originalFlag, "Original description")

	// Override it with new variable, value and description
	var newFlag string = "new_default"
	flag.Override("override-test", &newFlag, "New description")

	// Test that the flag was overridden
	overriddenFlag := pflag.Lookup("override-test")
	if overriddenFlag == nil {
		t.Error("Expected overridden flag to exist")
		return
	}

	if overriddenFlag.Usage != "New description" {
		t.Errorf("Expected usage to be 'New description', got '%s'", overriddenFlag.Usage)
	}

	if overriddenFlag.DefValue != "new_default" {
		t.Errorf("Expected default value to be 'new_default', got '%s'", overriddenFlag.DefValue)
	}
}

func TestOverrideFlagPanics(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// Test that overriding a non-existent flag panics
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when overriding non-existent flag")
		}
	}()
	var testFlag string
	flag.Override("non-existent-flag", &testFlag, "A non-existent flag")
}

func TestOverrideFlagNonPointer(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// First register a flag to override
	var originalFlag string
	flag.Register("override-non-pointer", &originalFlag, "Original flag")

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when overriding with non-pointer value")
		}
	}()
	var testFlag string
	flag.Override("override-non-pointer", testFlag, "A non-pointer override")
}

func TestOverrideFlagNilPointer(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// First register a flag to override
	var originalFlag string
	flag.Register("override-nil-pointer", &originalFlag, "Original flag")

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when overriding with nil pointer")
		}
	}()
	var testFlag *string
	flag.Override("override-nil-pointer", testFlag, "A nil pointer override")
}

func TestOverrideFlagAfterParse(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// Register a flag and parse
	var originalFlag string
	flag.Register("override-after-parse", &originalFlag, "Original flag")
	flag.Init() // This will parse the flags

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when overriding after parsing")
		}
	}()

	// Try to override after parsing - should panic
	var newFlag string = "new"
	flag.Override("override-after-parse", &newFlag, "New description")
}

func TestOverrideUnsupportedType(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// First register a flag to override
	var originalFlag string
	flag.Register("override-unsupported", &originalFlag, "Original flag")

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when overriding with unsupported type")
		}
	}()
	var testSlice []string
	flag.Override("override-unsupported", &testSlice, "An unsupported type override")
}

func TestUnregisterFlag(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// Register a flag to unregister
	var testFlag string
	flag.Register("unregister-test", &testFlag, "A flag to unregister")

	// Verify the flag exists
	if pflag.Lookup("unregister-test") == nil {
		t.Error("Expected flag to be registered")
		return
	}

	// Unregister the flag
	flag.Unregister("unregister-test")

	// Verify the flag no longer exists
	if pflag.Lookup("unregister-test") != nil {
		t.Error("Expected flag to be unregistered")
	}
}

func TestUnregisterFlagPanics(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// Test that unregistering a non-existent flag panics
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when unregistering non-existent flag")
		}
	}()
	flag.Unregister("non-existent-flag")
}

func TestUnregisterFlagAfterParse(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// Register a flag and parse
	var testFlag string
	flag.Register("unregister-after-parse", &testFlag, "A flag to unregister after parse")
	flag.Init() // This will parse the flags

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when unregistering after parsing")
		}
	}()

	// Try to unregister after parsing - should panic
	flag.Unregister("unregister-after-parse")
}

func TestUnregisterMultipleFlags(t *testing.T) {
	// Save and restore the original command line
	originalCommandLine := pflag.CommandLine
	defer func() { pflag.CommandLine = originalCommandLine }()

	// Create a fresh command line for this test
	pflag.CommandLine = pflag.NewFlagSet("", pflag.ContinueOnError)

	// Register multiple flags
	var flag1, flag2, flag3 string
	flag.Register("unregister-multi-1", &flag1, "First flag")
	flag.Register("unregister-multi-2", &flag2, "Second flag")
	flag.Register("unregister-multi-3", &flag3, "Third flag")

	// Verify all flags exist
	if pflag.Lookup("unregister-multi-1") == nil {
		t.Error("Expected flag1 to be registered")
	}
	if pflag.Lookup("unregister-multi-2") == nil {
		t.Error("Expected flag2 to be registered")
	}
	if pflag.Lookup("unregister-multi-3") == nil {
		t.Error("Expected flag3 to be registered")
	}

	// Unregister the middle flag
	flag.Unregister("unregister-multi-2")

	// Verify only the middle flag is unregistered
	if pflag.Lookup("unregister-multi-1") == nil {
		t.Error("Expected flag1 to still be registered")
	}
	if pflag.Lookup("unregister-multi-2") != nil {
		t.Error("Expected flag2 to be unregistered")
	}
	if pflag.Lookup("unregister-multi-3") == nil {
		t.Error("Expected flag3 to still be registered")
	}
}
