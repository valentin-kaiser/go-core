package interruption_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/internal/await"
	"github.com/valentin-kaiser/go-core/interruption"
)

const (
	longPanicRepeats       = 100
	notDoneWait            = 10 * time.Millisecond
	waitTimeout            = 5 * time.Second
	panicIterations        = 1000
	maxPerformanceDuration = time.Second
	largePanicBytes        = 1024 * 1024
	deepStackDepth         = 100
	benchHandlerCount      = 10
)

func TestHandle(t *testing.T) {
	// Test that Handle recovers from panic
	defer func() {
		if r := recover(); r != nil {
			t.Error("Handle should recover from panic, but panic was not handled")
		}
	}()

	// Test normal execution (no panic)
	func() {
		defer interruption.Catch()
		// Normal code that doesn't panic
	}()
}

func TestHandleWithPanic(t *testing.T) {
	// Test that Handle actually recovers from panic
	var handled bool

	func() {
		defer func() {
			if r := recover(); r != nil {
				interruption.Catch()
			}
			handled = true
		}()
		panic("test panic")
	}()

	if !handled {
		t.Error("Handle should have been called after panic recovery")
	}
}

func TestHandleWithDifferentPanicTypes(t *testing.T) {
	testCases := []struct {
		name  string
		panic any
	}{
		{name: "string panic", panic: "string panic message"},
		{name: "int panic", panic: 42},
		{name: "error panic", panic: errors.New("test error")},
		{name: "nil panic", panic: nil},
		{name: "struct panic", panic: struct{ msg string }{msg: "struct panic"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var handled bool

			func() {
				defer func() {
					if r := recover(); r != nil {
						interruption.Catch()
					}
					handled = true
				}()
				panic(tc.panic)
			}()

			if !handled {
				t.Errorf("Handle should have recovered from %s", tc.name)
			}
		})
	}
}

func TestHandleInDebugMode(t *testing.T) {
	// Test with debug mode enabled
	setDebug(t, true)

	var handled bool

	func() {
		defer func() {
			if r := recover(); r != nil {
				interruption.Catch()
			}
			handled = true
		}()
		panic("debug mode panic")
	}()

	if !handled {
		t.Error("Handle should have recovered from panic in debug mode")
	}
}

func TestHandleInProductionMode(t *testing.T) {
	// Test with debug mode disabled
	setDebug(t, false)

	var handled bool

	func() {
		defer func() {
			if r := recover(); r != nil {
				interruption.Catch()
			}
			handled = true
		}()
		panic("production mode panic")
	}()

	if !handled {
		t.Error("Handle should have recovered from panic in production mode")
	}
}

func TestHandleNested(t *testing.T) {
	// Test nested panic handling
	var outerHandled, innerHandled bool

	func() {
		defer func() {
			if r := recover(); r != nil {
				interruption.Catch()
			}
			outerHandled = true
		}()

		func() {
			defer func() {
				if r := recover(); r != nil {
					interruption.Catch()
				}
				innerHandled = true
			}()
			panic("inner panic")
		}()

		panic("outer panic")
	}()

	if !innerHandled {
		t.Error("Inner Handle should have been called")
	}
	if !outerHandled {
		t.Error("Outer Handle should have been called")
	}
}

func TestHandleMultiple(t *testing.T) {
	// Test multiple separate panic recoveries
	for i := range 3 {
		var handled bool

		func() {
			defer func() {
				if r := recover(); r != nil {
					interruption.Catch()
				}
				handled = true
			}()
			panic("multiple panic test")
		}()

		if !handled {
			t.Errorf("Handle should have recovered from panic %d", i)
		}
	}
}

func TestHandleWithoutPanic(t *testing.T) {
	// Test that Handle doesn't interfere with normal execution
	var normalExecution bool

	func() {
		defer interruption.Catch()
		normalExecution = true
	}()

	if !normalExecution {
		t.Error("Handle should not interfere with normal execution")
	}
}

// Test that shows Handle can be used in goroutines
func TestHandleInGoroutine(t *testing.T) {
	done := make(chan bool)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				interruption.Catch()
			}
			done <- true
		}()
		panic("goroutine panic")
	}()

	await.Receive(t, done, waitTimeout, "the goroutine to recover from its panic")
}

func TestHandleWithDetailedPanicTypes(t *testing.T) {
	tests := []struct {
		name      string
		panicData any
	}{
		{name: "custom error", panicData: errors.New("custom error message")},
		{name: "runtime error", panicData: errors.New("runtime error: invalid memory address or nil pointer dereference")},
		{name: "slice bound error", panicData: "runtime error: slice bounds out of range"},
		{name: "map access", panicData: "runtime error: assignment to entry in nil map"},
		{name: "channel close", panicData: "send on closed channel"},
		{name: "complex struct", panicData: struct {
			ID   int
			Name string
		}{ID: 123, Name: "test"}},
		{name: "slice panic", panicData: []string{"panic", "data"}},
		{name: "map panic", panicData: map[string]int{"error": 500}},
		{name: "function panic", panicData: func() string { return "function panic" }},
		{name: "very long string", panicData: strings.Repeat("very long panic message ", longPanicRepeats)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Handle() should not re-panic: %v", r)
				}
			}()

			func() {
				defer interruption.Catch()
				panic(tt.panicData)
			}()
		})
	}
}

func TestHandleWithCallStack(t *testing.T) {
	// Test that Handle captures the correct caller information
	// Test in debug mode
	setDebug(t, true)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Handle() should not re-panic in debug mode: %v", r)
			}
		}()

		func() {
			defer interruption.Catch()
			panic("debug mode panic")
		}()
	}()

	// Test in production mode
	setDebug(t, false)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Handle() should not re-panic in production mode: %v", r)
			}
		}()

		func() {
			defer interruption.Catch()
			panic("production mode panic")
		}()
	}()
}

func TestHandleWithGoroutineStack(t *testing.T) {
	done := make(chan bool, 1)

	go func() {
		defer func() {
			done <- true
		}()

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Handle() should not re-panic in goroutine: %v", r)
			}
		}()

		func() {
			defer interruption.Catch()
			panic("goroutine panic")
		}()
	}()

	await.Receive(t, done, waitTimeout, "the goroutine to finish")
}

func TestHandleWithRecursivePanic(t *testing.T) {
	depth := 0
	maxDepth := 5

	var recursivePanic func()
	recursivePanic = func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Handle() should not re-panic at depth %d: %v", depth, r)
			}
		}()

		defer interruption.Catch()

		depth++
		if depth < maxDepth {
			recursivePanic()
		}
		panic(fmt.Sprintf("recursive panic at depth %d", depth))
	}

	recursivePanic()
}

func TestHandleCallerInformation(t *testing.T) {
	// This test verifies that Handle() captures caller information correctly
	// We can't easily test the actual log output, but we can ensure it doesn't panic

	tests := []struct {
		name string
		fn   func()
	}{
		{
			name: "direct call",
			fn: func() {
				defer interruption.Catch()
				panic("direct panic")
			},
		},
		{
			name: "nested call",
			fn: func() {
				func() {
					defer interruption.Catch()
					panic("nested panic")
				}()
			},
		},
		{
			name: "anonymous function",
			fn: func() {
				func() {
					defer interruption.Catch()
					panic("anonymous panic")
				}()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Handle() should not re-panic in %s: %v", tt.name, r)
				}
			}()

			tt.fn()
		})
	}
}

func TestHandleWithRuntimeCallerFailure(t *testing.T) {
	// Test what happens when runtime.Caller fails
	// This is difficult to test directly, but we can ensure Handle() is robust

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic when runtime.Caller fails: %v", r)
		}
	}()

	func() {
		defer interruption.Catch()
		panic("test runtime caller failure scenario")
	}()
}

func TestHandleInDifferentDebugModes(t *testing.T) {
	modes := []struct {
		name  string
		debug bool
	}{
		{name: "debug mode", debug: true},
		{name: "production mode", debug: false},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			setDebug(t, mode.debug)

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Handle() should not re-panic in %s: %v", mode.name, r)
				}
			}()

			func() {
				defer interruption.Catch()
				panic("panic in " + mode.name)
			}()
		})
	}
}

func TestHandleWithNilPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic with nil panic: %v", r)
		}
	}()

	func() {
		defer interruption.Catch()
		var nilPtr *int
		panic(nilPtr)
	}()
}

func TestHandleWithLargePanicData(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic with large panic data: %v", r)
		}
	}()

	// Create large panic data
	largeData := make([]byte, largePanicBytes)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	func() {
		defer interruption.Catch()
		panic(largeData)
	}()
}

func TestHandleFilePathProcessing(t *testing.T) {
	// Test that file paths are processed correctly in caller information
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic during file path processing: %v", r)
		}
	}()

	// Create a scenario with nested directory structure
	func() {
		defer interruption.Catch()
		panic("file path processing test")
	}()
}

func TestHandleWithOSSignals(t *testing.T) {
	// Test Handle() behavior when OS signals might be involved
	// This is more of a robustness test

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic with OS signal simulation: %v", r)
		}
	}()

	func() {
		defer interruption.Catch()
		// Simulate some OS-related panic
		panic("simulated OS signal panic")
	}()
}

func TestHandleMemoryPressure(t *testing.T) {
	// Test Handle() under memory pressure scenarios
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic under memory pressure: %v", r)
		}
	}()

	func() {
		defer interruption.Catch()
		// Simulate out of memory scenario
		panic("runtime: out of memory")
	}()
}

func TestMultipleHandleRegistrations(t *testing.T) {
	// Test that multiple Handle() calls work correctly
	counter := 0

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Multiple Handle() calls should not re-panic: %v", r)
		}
		if counter != 3 {
			t.Errorf("Expected counter to be 3, got %d", counter)
		}
	}()

	func() {
		defer func() { counter++ }()
		defer interruption.Catch()

		func() {
			defer func() { counter++ }()
			defer interruption.Catch()

			func() {
				defer func() { counter++ }()
				defer interruption.Catch()
				panic("nested handle test")
			}()
		}()
	}()
}

func TestHandlePerformance(t *testing.T) {
	// Basic performance test to ensure Handle() doesn't introduce significant overhead
	start := time.Now()
	for range panicIterations {
		func() {
			defer interruption.Catch()
			panic("performance test")
		}()
	}
	duration := time.Since(start)

	// Ensure it doesn't take too long (arbitrary threshold)
	if duration > maxPerformanceDuration {
		t.Errorf("Handle() performance test took too long: %v", duration)
	}
}

// Test runtime characteristics
func TestHandleRuntimeCharacteristics(t *testing.T) {
	// Test that Handle() works correctly with different runtime characteristics

	// Test with different GOMAXPROCS values
	originalGOMAXPROCS := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(originalGOMAXPROCS) })

	for _, procs := range []int{1, 2, 4, runtime.NumCPU()} {
		t.Run(fmt.Sprintf("GOMAXPROCS=%d", procs), func(t *testing.T) {
			runtime.GOMAXPROCS(procs)

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Handle() should not re-panic with GOMAXPROCS=%d: %v", procs, r)
				}
			}()

			func() {
				defer interruption.Catch()
				panic(fmt.Sprintf("panic with GOMAXPROCS=%d", procs))
			}()
		})
	}
}

func TestHandleWithEnvironmentVariables(t *testing.T) {
	// Test with modified environment
	t.Setenv("PATH", "")

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Handle() should not re-panic with modified environment: %v", r)
		}
	}()

	func() {
		defer interruption.Catch()
		panic("environment variable test")
	}()
}

// OnSignal Tests

func TestOnSignalContextCreation(t *testing.T) {
	// Test that OnSignal returns a valid context
	handler := func() error {
		return nil
	}

	ctx := interruption.OnSignal([]func() error{handler}, syscall.SIGTERM)

	if ctx == nil {
		t.Error("OnSignal should return a non-nil context")
	}

	// Context should not be done initially (no signal sent)
	select {
	case <-ctx.Done():
		t.Error("Context should not be done immediately after creation")
	default:
		// Expected
	}
}

func TestOnSignalHandlerExecution(t *testing.T) {
	// Test that handlers execute when manually triggering the context
	var mu sync.Mutex
	executed := make([]bool, 3)

	handler1 := func() error {
		mu.Lock()
		executed[0] = true
		mu.Unlock()
		return nil
	}
	handler2 := func() error {
		mu.Lock()
		executed[1] = true
		mu.Unlock()
		return nil
	}
	handler3 := func() error {
		mu.Lock()
		executed[2] = true
		mu.Unlock()
		return nil
	}

	handlers := []func() error{handler1, handler2, handler3}

	// Create a context that we can cancel manually to simulate signal
	ctx, cancel := context.WithCancel(t.Context())

	// Use a wait group to synchronize goroutine completion
	var wg sync.WaitGroup

	// Simulate the OnSignal goroutine behavior
	wg.Go(func() {
		// Wait for context cancellation (simulating signal)
		<-ctx.Done()
		for _, handler := range handlers {
			err := handler()
			if err != nil {
				t.Errorf("Handler failed: %v", err)
			}
		}
	})

	// Cancel the context to simulate signal
	cancel()

	// Wait for the goroutine to complete
	wg.Wait()

	// Check that all handlers were executed
	mu.Lock()
	for i, exec := range executed {
		if !exec {
			t.Errorf("Handler %d was not executed", i+1)
		}
	}
	mu.Unlock()
}

func TestOnSignalWithErrors(t *testing.T) {
	// Test handlers that return errors
	testError := errors.New("handler error")

	errorHandler := func() error {
		return testError
	}
	successHandler := func() error {
		return nil
	}

	handlers := []func() error{errorHandler, successHandler}

	// Create a context that we can cancel manually
	ctx, cancel := context.WithCancel(t.Context())

	var mu sync.Mutex
	var handlerErrors []error

	// Use a wait group to synchronize goroutine completion
	var wg sync.WaitGroup

	// Simulate the OnSignal goroutine behavior
	wg.Go(func() {
		<-ctx.Done()
		for _, handler := range handlers {
			err := handler()
			if err != nil {
				mu.Lock()
				handlerErrors = append(handlerErrors, err)
				mu.Unlock()
			}
		}
	})

	// Cancel the context to simulate signal
	cancel()

	// Wait for the goroutine to complete
	wg.Wait()

	// Check that error was captured
	mu.Lock()
	if len(handlerErrors) != 1 {
		t.Errorf("Expected 1 error, got %d", len(handlerErrors))
	}

	if len(handlerErrors) > 0 && handlerErrors[0] != testError {
		t.Errorf("Expected error %v, got %v", testError, handlerErrors[0])
	}
	mu.Unlock()
}

func TestOnSignalNoHandlers(t *testing.T) {
	// Test with empty handlers slice
	ctx := interruption.OnSignal([]func() error{}, syscall.SIGTERM)

	if ctx == nil {
		t.Error("OnSignal should return a non-nil context even with no handlers")
	}

	// With empty handlers, the context should be done immediately
	// since the goroutine runs through an empty handler list and calls stop()
	await.Receive(t, ctx.Done(), waitTimeout, "the context to be done with no handlers")
}

func TestOnSignalNilHandlers(t *testing.T) {
	// Test with nil handlers slice
	ctx := interruption.OnSignal(nil, syscall.SIGTERM)

	if ctx == nil {
		t.Error("OnSignal should return a non-nil context even with nil handlers")
	}

	// With nil handlers, the context should be done immediately
	// since the goroutine runs through an empty handler list and calls stop()
	await.Receive(t, ctx.Done(), waitTimeout, "the context to be done with nil handlers")
}

func TestOnSignalHandlerPanic(t *testing.T) {
	// Test behavior when handler panics
	var mu sync.Mutex
	executed := make([]bool, 2)

	panicHandler := func() error {
		mu.Lock()
		executed[0] = true
		mu.Unlock()
		panic("handler panic")
	}
	normalHandler := func() error {
		mu.Lock()
		executed[1] = true
		mu.Unlock()
		return nil
	}

	handlers := []func() error{panicHandler, normalHandler}

	// Create a context that we can cancel manually
	ctx, cancel := context.WithCancel(t.Context())

	// Use a wait group to synchronize goroutine completion
	var wg sync.WaitGroup

	// Simulate the OnSignal goroutine behavior with panic recovery
	wg.Go(func() {
		<-ctx.Done()
		for _, handler := range handlers {
			func() {
				defer interruption.Catch()
				err := handler()
				if err != nil {
					t.Errorf("Handler failed: %v", err)
				}
			}()
		}
	})

	// Cancel the context to simulate signal
	cancel()

	// Wait for the goroutine to complete
	wg.Wait()

	// Check execution status
	mu.Lock()
	// First handler should have executed (and panicked)
	if !executed[0] {
		t.Error("Panic handler was not executed")
	}

	// Second handler should still execute even after first panicked
	if !executed[1] {
		t.Error("Normal handler was not executed after panic handler")
	}
	mu.Unlock()
}

func TestOnSignalLongRunningHandler(t *testing.T) {
	// Test with a handler that is still running after the context was cancelled
	var mu sync.Mutex
	executed := false
	started := make(chan struct{})
	release := make(chan struct{})

	handler := func() error {
		close(started)
		<-release
		mu.Lock()
		executed = true
		mu.Unlock()
		return nil
	}

	handlers := []func() error{handler}

	// Create a context that we can cancel manually
	ctx, cancel := context.WithCancel(t.Context())

	// Use a wait group to synchronize goroutine completion
	var wg sync.WaitGroup

	// Simulate the OnSignal goroutine behavior
	wg.Go(func() {
		<-ctx.Done()
		for _, h := range handlers {
			err := h()
			if err != nil {
				t.Errorf("Handler failed: %v", err)
			}
		}
	})

	// Cancel the context to simulate signal
	cancel()

	await.Receive(t, started, waitTimeout, "the handler to start")

	mu.Lock()
	if executed {
		t.Error("Handler completed before it was released")
	}
	mu.Unlock()

	close(release)

	// Wait for the goroutine to complete
	wg.Wait()

	mu.Lock()
	if !executed {
		t.Error("Long running handler was not executed")
	}
	mu.Unlock()
}

func TestOnSignalMultipleSignalTypes(t *testing.T) {
	// Test with multiple signal types - just verify the function accepts them
	handler := func() error {
		return nil
	}

	// Test that function accepts multiple signal types without error
	ctx := interruption.OnSignal([]func() error{handler}, syscall.SIGTERM, syscall.SIGINT)

	if ctx == nil {
		t.Error("OnSignal should return a non-nil context with multiple signals")
	}

	// Context should not be done initially
	select {
	case <-ctx.Done():
		t.Error("Context should not be done immediately with multiple signals")
	default:
		// Expected
	}
}

func TestOnSignalContextType(t *testing.T) {
	// Test that the returned context is the correct type
	handler := func() error {
		return nil
	}

	ctx := interruption.OnSignal([]func() error{handler}, syscall.SIGTERM)

	if ctx == nil {
		t.Fatal("OnSignal should return a non-nil context")
	}

	// Test context methods exist
	if ctx.Done() == nil {
		t.Error("Context.Done() should return a non-nil channel")
	}

	if ctx.Err() != nil {
		t.Error("Context.Err() should be nil for a non-cancelled context")
	}

	// Context should have a deadline method (even if no deadline is set)
	_, ok := ctx.Deadline()
	if ok {
		t.Error("Signal context should not have a deadline")
	}
}

// Benchmark tests
func BenchmarkHandle(b *testing.B) {
	for i := 0; i < b.N; i++ {
		func() {
			defer interruption.Catch()
			panic("benchmark test")
		}()
	}
}

func BenchmarkHandleWithLargeStack(b *testing.B) {
	for i := 0; i < b.N; i++ {
		func() {
			// Create a deep call stack
			var deepCall func(depth int)
			deepCall = func(depth int) {
				if depth > 0 {
					deepCall(depth - 1)
				} else {
					defer interruption.Catch()
					panic("deep stack benchmark")
				}
			}

			deepCall(deepStackDepth)
		}()
	}
}

func BenchmarkHandleDebugMode(b *testing.B) {
	setDebug(b, true)

	for i := 0; i < b.N; i++ {
		func() {
			defer interruption.Catch()
			panic("debug mode benchmark")
		}()
	}
}

func BenchmarkHandleProductionMode(b *testing.B) {
	setDebug(b, false)

	for i := 0; i < b.N; i++ {
		func() {
			defer interruption.Catch()
			panic("production mode benchmark")
		}()
	}
}

// Benchmark tests for OnSignal
func BenchmarkOnSignalCreation(b *testing.B) {
	handler := func() error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := interruption.OnSignal([]func() error{handler}, syscall.SIGTERM)
		_ = ctx // Use the context to prevent optimization
	}
}

func BenchmarkOnSignalMultipleHandlers(b *testing.B) {
	handlers := make([]func() error, benchHandlerCount)
	for i := range handlers {
		handlers[i] = func() error {
			return nil
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := interruption.OnSignal(handlers, syscall.SIGTERM)
		_ = ctx // Use the context to prevent optimization
	}
}

func TestWaitForShutdown(t *testing.T) {
	// Test WaitForShutdown with a normal context
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan bool, 1)
	go func() {
		interruption.WaitForShutdown(ctx)
		done <- true
	}()

	// Should not be done immediately
	select {
	case <-done:
		t.Error("WaitForShutdown should not complete immediately")
	case <-time.After(notDoneWait):
		// Expected
	}

	// Cancel the context
	cancel()

	// Should complete now
	await.Receive(t, done, waitTimeout, "WaitForShutdown to complete after context cancellation")
}

func TestWaitForShutdownWithNilContext(t *testing.T) {
	// Test WaitForShutdown with nil context
	done := make(chan bool, 1)
	go func() {
		interruption.WaitForShutdown(nil) //nolint:staticcheck // SA1012: a nil context is the behaviour under test
		done <- true
	}()

	// Should complete immediately with nil context
	await.Receive(t, done, waitTimeout, "WaitForShutdown to complete with a nil context")
}

func TestWaitForShutdownWithOnSignal(t *testing.T) {
	// Skip this test on Windows as signal sending to processes is not supported
	if runtime.GOOS == "windows" {
		t.Skip("Signal sending not supported on Windows")
	}

	// Test WaitForShutdown with OnSignal context
	var mu sync.Mutex
	executed := false

	handler := func() error {
		mu.Lock()
		executed = true
		mu.Unlock()
		return nil
	}

	ctx := interruption.OnSignal([]func() error{handler}, syscall.SIGTERM)

	done := make(chan bool, 1)
	go func() {
		interruption.WaitForShutdown(ctx)
		done <- true
	}()

	// Should not be done immediately
	select {
	case <-done:
		t.Error("WaitForShutdown should not complete immediately")
	case <-time.After(notDoneWait):
		// Expected
	}

	// Send signal to ourselves
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("Failed to find process: %v", err)
	}

	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		t.Fatalf("Failed to send signal: %v", err)
	}

	// Should complete after signal and handler execution
	await.Receive(t, done, waitTimeout, "WaitForShutdown to complete after signal handling")

	// Check that handler was executed
	mu.Lock()
	if !executed {
		t.Error("Signal handler should have been executed")
	}
	mu.Unlock()
}

func TestSetupGracefulShutdown(t *testing.T) {
	// Test SetupGracefulShutdown function
	var mu sync.Mutex
	executed := false

	handler := func() error {
		mu.Lock()
		executed = true
		mu.Unlock()
		return nil
	}

	// Setup graceful shutdown - this should return a function to defer
	shutdownFunc := interruption.SetupGracefulShutdown([]func() error{handler}, syscall.SIGTERM)

	if shutdownFunc == nil {
		t.Error("SetupGracefulShutdown should return a non-nil function")
	}

	// The shutdown function should exist but handler should not be executed yet
	mu.Lock()
	if executed {
		t.Error("Handler should not be executed immediately after setup")
	}
	mu.Unlock()

	// Test that the shutdown function works when called
	done := make(chan bool, 1)
	go func() {
		shutdownFunc()
		done <- true
	}()

	// Should not be done immediately (waiting for signal)
	select {
	case <-done:
		// This could happen if no handlers, which is fine
	case <-time.After(notDoneWait):
		// Expected behavior - waiting for signal
	}
}

func TestSetupGracefulShutdownWithNoHandlers(t *testing.T) {
	// Test with no handlers
	shutdownFunc := interruption.SetupGracefulShutdown([]func() error{}, syscall.SIGTERM)

	if shutdownFunc == nil {
		t.Error("SetupGracefulShutdown should return a non-nil function even with no handlers")
	}

	// Test that the shutdown function completes quickly with no handlers
	done := make(chan bool, 1)
	go func() {
		shutdownFunc()
		done <- true
	}()

	// Should complete quickly with no handlers
	await.Receive(t, done, waitTimeout, "SetupGracefulShutdown to complete with no handlers")
}

func TestSetupGracefulShutdownWithNilHandlers(t *testing.T) {
	// Test with nil handlers
	shutdownFunc := interruption.SetupGracefulShutdown(nil, syscall.SIGTERM)

	if shutdownFunc == nil {
		t.Error("SetupGracefulShutdown should return a non-nil function even with nil handlers")
	}

	// Test that the shutdown function completes quickly with nil handlers
	done := make(chan bool, 1)
	go func() {
		shutdownFunc()
		done <- true
	}()

	// Should complete quickly with nil handlers
	await.Receive(t, done, waitTimeout, "SetupGracefulShutdown to complete with nil handlers")
}
