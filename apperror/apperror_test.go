package apperror_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/apperror"
	"github.com/valentin-kaiser/go-core/v2/flag"
)

func TestNewError(t *testing.T) {
	msg := "test error message"
	err := apperror.NewError(msg)

	if err.Message != msg {
		t.Errorf("Expected message '%s', got '%s'", msg, err.Message)
	}

	if len(err.Trace) == 0 {
		t.Error("Expected non-empty trace")
	}

	if len(err.Errors) != 0 {
		t.Error("Expected empty errors slice")
	}
}

func TestNewErrorf(t *testing.T) {
	format := "test error with number %d and string %s"
	err := apperror.NewErrorf(format, 42, "hello")

	expected := "test error with number 42 and string hello"
	if err.Message != expected {
		t.Errorf("Expected message '%s', got '%s'", expected, err.Message)
	}

	if len(err.Trace) == 0 {
		t.Error("Expected non-empty trace")
	}
}

func TestWrap(t *testing.T) {
	// Test wrapping nil
	wrapped := apperror.Wrap(nil)
	if wrapped != nil {
		t.Error("Wrapping nil should return nil")
	}

	// Test wrapping standard error
	originalErr := errors.New("original error")
	wrapped = apperror.Wrap(originalErr)
	if wrapped == nil {
		t.Error("Wrapping error should not return nil")
	}

	appErr, ok := wrapped.(apperror.Error)
	if !ok {
		t.Error("Wrapped error should be of type Error")
	}

	if appErr.Message != originalErr.Error() {
		t.Errorf("Expected message '%s', got '%s'", originalErr.Error(), appErr.Message)
	}

	// Test wrapping Error type
	appError := apperror.NewError("app error")
	wrapped = apperror.Wrap(appError)
	wrappedAppErr, ok := wrapped.(apperror.Error)
	if !ok {
		t.Error("Wrapped Error should be of type Error")
	}

	if wrappedAppErr.Message != appError.Message {
		t.Errorf("Expected message '%s', got '%s'", appError.Message, wrappedAppErr.Message)
	}
}

func TestAddError(t *testing.T) {
	err := apperror.NewError("main error")
	additionalErr := errors.New("additional error")

	newErr := err.AddError(additionalErr)

	if len(newErr.Errors) != 1 {
		t.Errorf("Expected 1 additional error, got %d", len(newErr.Errors))
	}

	if newErr.Errors[0].Error() != additionalErr.Error() {
		t.Errorf("Expected additional error '%s', got '%s'", additionalErr.Error(), newErr.Errors[0].Error())
	}
}

func TestAddErrors(t *testing.T) {
	err := apperror.NewError("main error")
	additionalErrs := []error{
		errors.New("error 1"),
		errors.New("error 2"),
		errors.New("error 3"),
	}

	newErr := err.AddErrors(additionalErrs)

	if len(newErr.Errors) != 3 {
		t.Errorf("Expected 3 additional errors, got %d", len(newErr.Errors))
	}

	for i, addErr := range additionalErrs {
		if newErr.Errors[i].Error() != addErr.Error() {
			t.Errorf("Expected error %d to be '%s', got '%s'", i, addErr.Error(), newErr.Errors[i].Error())
		}
	}
}

func TestAddErrorWithAppError(t *testing.T) {
	mainErr := apperror.NewError("main error")
	appErr := apperror.NewError("app error").AddError(errors.New("nested error"))

	newErr := mainErr.AddError(appErr)

	// Should flatten the nested errors
	if len(newErr.Errors) != 1 {
		t.Errorf("Expected 1 additional error (flattened), got %d", len(newErr.Errors))
	}
}

func TestErrorString(t *testing.T) {
	// Save original debug state
	originalDebug := flag.Debug
	t.Cleanup(func() {
		flag.Debug = originalDebug
		apperror.Anonymous(false)
	})

	// Test without debug mode
	flag.Debug = false
	err := apperror.NewError("test error")

	if err.Error() != "test error" {
		t.Errorf("Expected 'test error', got '%s'", err.Error())
	}

	// Test with additional errors
	err = err.AddError(errors.New("additional error"))
	expected := "test error [additional error]"
	if err.Error() != expected {
		t.Errorf("Expected '%s', got '%s'", expected, err.Error())
	}

	// Test with debug mode
	flag.Debug = true
	apperror.Anonymous(true) // Enable anonymous mode to get function names in trace
	err = apperror.NewError("test error")
	errorStr := err.Error()

	if !strings.Contains(errorStr, "test error") {
		t.Error("Error string should contain the original message")
	}

	if !strings.Contains(errorStr, "TestErrorString") {
		t.Error("Error string should contain trace information in debug mode")
	}
}

func TestSplit(t *testing.T) {
	// Test with standard error
	standardErr := errors.New("standard error")
	msg, trace, errs := apperror.Split(standardErr)

	if msg != "standard error" {
		t.Errorf("Expected message 'standard error', got '%s'", msg)
	}
	if trace != nil {
		t.Error("Expected nil trace for standard error")
	}
	if errs != nil {
		t.Error("Expected nil errors for standard error")
	}

	// Test with app error
	appErr := apperror.NewError("app error").AddError(errors.New("additional error"))
	msg, trace, errs = apperror.Split(appErr)

	if msg != "app error" {
		t.Errorf("Expected message 'app error', got '%s'", msg)
	}
	if trace == nil {
		t.Error("Expected non-nil trace for app error")
	}
	if len(errs) != 1 {
		t.Errorf("Expected 1 additional error, got %d", len(errs))
	}
}

func TestParse(t *testing.T) {
	// Test simple parsing
	err := apperror.Parse("simple error")
	if err.Message != "simple error" {
		t.Errorf("Expected message 'simple error', got '%s'", err.Message)
	}

	// Test parsing with trace
	traceStr := "func1+123 -> func2+456 -> simple error"
	err = apperror.Parse(traceStr)
	if !strings.Contains(err.Error(), "simple error") {
		t.Error("Parsed error should contain the original message")
	}
}

func TestWhere(t *testing.T) {
	where := apperror.Where(2)
	if where == "unknown" {
		t.Error("Where should return caller information")
	}
	if !strings.Contains(where, "TestWhere") {
		t.Error("Where should contain the calling function name")
	}
}

// Test error formatting variables
func TestErrorFormatting(t *testing.T) {
	// Test that formatting variables exist and can be read
	if apperror.TraceDelimiter == "" {
		t.Error("TraceDelimiter should not be empty")
	}
	if apperror.ErrorDelimiter == "" {
		t.Error("ErrorDelimiter should not be empty")
	}
	if apperror.TraceFormat == "" {
		t.Error("TraceFormat should not be empty")
	}
	if apperror.ErrorFormat == "" {
		t.Error("ErrorFormat should not be empty")
	}
	if apperror.ErrorTraceFormat == "" {
		t.Error("ErrorTraceFormat should not be empty")
	}
	if apperror.FullFormat == "" {
		t.Error("FullFormat should not be empty")
	}
}

func TestErrorWithMultipleAdditionalErrors(t *testing.T) {
	err := apperror.NewError("main error")
	err = err.AddError(errors.New("error 1"))
	err = err.AddError(errors.New("error 2"))
	err = err.AddError(errors.New("error 3"))

	errorStr := err.Error()

	if !strings.Contains(errorStr, "error 1") {
		t.Error("Error string should contain first additional error")
	}
	if !strings.Contains(errorStr, "error 2") {
		t.Error("Error string should contain second additional error")
	}
	if !strings.Contains(errorStr, "error 3") {
		t.Error("Error string should contain third additional error")
	}
}

func TestErrorImplementsErrorInterface(t *testing.T) {
	var err error = apperror.NewError("test error")

	if err.Error() != "test error" {
		t.Error("Error should implement the error interface correctly")
	}
}

// Benchmark tests
func BenchmarkNewError(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = apperror.NewError("benchmark error")
	}
}

func BenchmarkWrap(b *testing.B) {
	baseErr := errors.New("base error")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := apperror.Wrap(baseErr)
		if err == nil {
			b.Fatal("Wrap() = nil, want an error")
		}
	}
}

func BenchmarkErrorString(b *testing.B) {
	err := apperror.NewError("benchmark error").AddError(errors.New("additional"))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = err.Error()
	}
}

func TestErrorWithContext(t *testing.T) {
	err := apperror.NewError("context error").
		AddDetail("user_id", "12345").
		AddDetail("operation", "delete").
		AddDetail("timestamp", "2023-01-01T00:00:00Z")

	// Test that all details are preserved
	context := err.GetContext()
	if len(context) != 3 {
		t.Errorf("Expected 3 details, got %d", len(context))
	}

	// Test individual detail retrieval
	userID := err.GetDetail("user_id")
	if userID != "12345" {
		t.Errorf("Expected user_id to be '12345', got %v", userID)
	}

	operation := err.GetDetail("operation")
	if operation != "delete" {
		t.Errorf("Expected operation to be 'delete', got %v", operation)
	}

	// Test non-existent detail
	nonExistent := err.GetDetail("non_existent")
	if nonExistent != nil {
		t.Errorf("Expected non-existent detail to be nil, got %v", nonExistent)
	}
}

func TestErrorChaining(t *testing.T) {
	originalErr := errors.New("original error")
	wrappedErr := apperror.Wrap(originalErr)

	// Test that wrapping preserves the original message
	if !strings.Contains(wrappedErr.Error(), "original error") {
		t.Error("Expected wrapped error to contain original message")
	}

	// Test error chaining
	err1 := apperror.NewError("error1")
	err2 := apperror.NewError("error2").AddError(err1)

	errorString := err2.Error()
	if !strings.Contains(errorString, "error1") {
		t.Error("Expected chained error to contain nested error message")
	}
}

func TestCatchFunction(t *testing.T) {
	original := apperror.CatchHandler
	t.Cleanup(func() { apperror.CatchHandler = original })

	var gotMsg string
	var gotErr error
	apperror.CatchHandler = func(err error, msg string) {
		gotErr = err
		gotMsg = msg
	}

	wantErr := errors.New("test error")
	apperror.Catch(func() error {
		return wantErr
	}, "operation failed")

	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("CatchHandler error = %v, want %v", gotErr, wantErr)
	}
	if gotMsg != "operation failed" {
		t.Fatalf("CatchHandler message = %q, want %q", gotMsg, "operation failed")
	}
}

func TestCatchFunctionDoesNotPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r != nil {
			t.Fatalf("Catch panicked: %v", r)
		}
	}()

	apperror.Catch(func() error {
		return errors.New("test error")
	}, "operation failed")
}

func TestCatchFunctionSuccess(_ *testing.T) {
	// Test successful operation (should not panic)
	apperror.Catch(func() error {
		return nil
	}, "should not fail")
	// If we reach here, the test passed
}

func TestHandle(t *testing.T) {
	// Test Handle with nil error (should not panic)
	apperror.Handle(nil, "no error")

	// Test Handle with error (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected Handle to panic on error")
		}
	}()

	apperror.Handle(errors.New("test error"), "handle test")
}

func TestSplitFunction(t *testing.T) {
	err := apperror.NewError("test message").
		AddError(errors.New("nested error"))

	message, trace, errs := apperror.Split(err)

	if message != "test message" {
		t.Errorf("Expected message 'test message', got '%s'", message)
	}

	if len(trace) == 0 {
		t.Error("Expected non-empty trace")
	}

	if len(errs) != 1 {
		t.Errorf("Expected 1 nested error, got %d", len(errs))
	}

	if errs[0].Error() != "nested error" {
		t.Errorf("Expected nested error 'nested error', got '%s'", errs[0].Error())
	}

	// Test with standard error
	stdErr := errors.New("standard error")
	message2, trace2, errs2 := apperror.Split(stdErr)

	if message2 != "standard error" {
		t.Errorf("Expected message 'standard error', got '%s'", message2)
	}

	if trace2 != nil {
		t.Error("Expected nil trace for standard error")
	}

	if errs2 != nil {
		t.Error("Expected nil errors for standard error")
	}
}

func TestParseFunction(t *testing.T) {
	// Test parsing simple error
	err := apperror.Parse("simple error")
	if err.Message != "simple error" {
		t.Errorf("Expected message 'simple error', got '%s'", err.Message)
	}

	// Test parsing with trace
	complexStr := "func1+10 -> func2+20 -> error message"
	err2 := apperror.Parse(complexStr)
	if err2.Message != "error message" {
		t.Errorf("Expected message 'error message', got '%s'", err2.Message)
	}
}

func TestWhereFunction(t *testing.T) {
	location := apperror.Where(2)
	if location == "" || location == "unknown" {
		t.Error("Expected valid location information")
	}

	// Should contain file and line information
	if !strings.Contains(location, ":") {
		t.Error("Expected location to contain file:line format")
	}
}
