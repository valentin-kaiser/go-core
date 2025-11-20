// Package interruption provides a simple mechanism for recovering from panics
// in both the main application and concurrently running goroutines.
//
// It ensures that unexpected runtime panics do not crash the application silently,
// by logging detailed error messages and stack traces.
//
// Usage:
// Call `defer interruption.Handle()` at the beginning of the `main` function
// and in every goroutine to catch and log panics.
//
// Example:
//
//	package main
//
//	import (
//		"github.com/valentin-kaiser/go-core/interruption"
//		"fmt"
//	)
//
//	func main() {
//		defer interruption.Handle()
//		fmt.Println("Application started")
//		// Your application logic here
//
//		ctx := interruption.OnSignal([]func() error{
//			func() error {
//				fmt.Println("Received interrupt signal, shutting down gracefully")
//				return nil
//			},
//		}, os.Interrupt, syscall.SIGTERM)
//
//		<-ctx.Done() // Wait for the signal handler to complete
//	}
//
// In debug mode, a full stack trace is logged to aid in debugging.
// In production mode, only the panic message and caller information are logged
// to avoid cluttering logs with excessive detail.
package interruption

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/flag"
	"github.com/valentin-kaiser/go-core/logging"
)

var (
	logger    = logging.GetPackageLogger("interruption")
	Directory = filepath.Join(flag.Path, "panics")
	Format    = "2006-01-02_15-04-05"
)

func init() {
	_ = os.MkdirAll(Directory, os.ModePerm)
}

// Catch recovers from panics in the application and logs detailed error information.
// It captures the panic value, caller information, and stack trace for debugging.
// When debug mode is enabled, it logs the full stack trace; otherwise, it logs only
// the essential error details to avoid cluttering production logs.
//
// This function should be called with defer at the beginning of main() and any goroutines:
//
//	func main() {
//		defer interruption.Catch()
//		// application logic
//	}
func Catch() {
	err := recover()
	if err != nil {
		caller := "unknown"
		line := 0
		_, file, line, ok := runtime.Caller(3)
		if ok {
			caller = fmt.Sprintf("%s/%s", filepath.Base(filepath.Dir(file)), strings.Trim(filepath.Base(file), filepath.Ext(file)))
		}

		if !logger.Enabled() {
			if flag.Debug {
				fmt.Fprintf(os.Stderr, "%v code: %v => %v \n %v", caller, line, err, string(debug.Stack()))
				return
			}
			fmt.Fprintf(os.Stderr, "%v code: %v => %v", caller, line, err)
			return
		}

		if flag.Debug {
			logger.Error().Msgf("%v code: %v => %v \n %v", caller, line, err, string(debug.Stack()))
			return
		}
		logger.Error().Msgf("%v code: %v => %v", caller, line, err)

		timestamp := time.Now().Format(Format)
		name := filepath.Join(Directory, fmt.Sprintf("%s.log", timestamp))
		content := fmt.Sprintf("Caller: %v\nLine: %v\nError: %v\n", caller, line, err)
		if flag.Debug {
			content = fmt.Sprintf("Caller: %v\nLine: %v\nError: %v\n\nStack Trace:\n%s\n", caller, line, err, string(debug.Stack()))
		}
		werr := os.WriteFile(name, []byte(content), 0644)
		if werr != nil {
			fmt.Fprintf(os.Stderr, "failed to write panic: %v", apperror.Wrap(werr))
		}
	}
}

// OnSignal registers a handler function to be called when the specified signals are received.
// It allows graceful shutdown or cleanup operations when the application receives termination signals.
// The returned context is canceled when the handler function returns, allowing the application to wait for cleanup operations to complete.
func OnSignal(handlers []func() error, signals ...os.Signal) context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	// Create a channel to receive signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, signals...)

	go func() {
		defer func() {
			signal.Stop(sigChan)
			cancel()
		}()

		if len(handlers) == 0 {
			return
		}

		// Wait for signal
		<-sigChan

		// Execute all handlers with panic recovery
		for _, handler := range handlers {
			func() {
				defer Catch()
				err := handler()
				if err != nil {
					logger.Error().Err(err).Msgf("handler failed")
				}
			}()
		}
	}()
	return ctx
}

// WaitForShutdown waits for the provided context to be done, enabling graceful shutdown.
// This function is designed to be used with defer to ensure the application waits for
// signal handlers to complete before exiting.
//
// Example usage:
//
//	func main() {
//		defer interruption.Handle()
//
//		ctx := interruption.OnSignal([]func() error{
//			func() error {
//				log.Info().Msg("shutting down gracefully...")
//				return nil
//			},
//		}, os.Interrupt, syscall.SIGTERM)
//
//		defer interruption.WaitForShutdown(ctx)
//
//		// Your application logic here
//		log.Info().Msg("application running...")
//
//		// Application will wait for signal and graceful shutdown when function exits
//	}
//
// WaitForShutdown blocks until the provided context is cancelled, typically used
// to wait for graceful shutdown signal handlers to complete their cleanup operations.
// If the context is nil, the function returns immediately.
//
// This function is commonly used in conjunction with OnSignal to implement graceful shutdown:
//
//	ctx := interruption.OnSignal(handlers, os.Interrupt, syscall.SIGTERM)
//	defer interruption.WaitForShutdown(ctx)
func WaitForShutdown(ctx context.Context) {
	if ctx == nil {
		return
	}
	<-ctx.Done()
}

// SetupGracefulShutdown is a convenience function that combines OnSignal and WaitForShutdown.
// It sets up signal handlers and returns a function that should be called with defer
// to wait for graceful shutdown. This is the recommended way to add graceful shutdown to your application.
//
// Example usage:
//
//	func main() {
//		defer interruption.Handle()
//
//		defer interruption.SetupGracefulShutdown([]func() error{
//			func() error {
//				log.Info().Msg("database disconnecting...")
//				// database.Disconnect()
//				return nil
//			},
//			func() error {
//				log.Info().Msg("web server stopping...")
//				// web.Instance().Stop()
//				return nil
//			},
//		}, os.Interrupt, syscall.SIGTERM)
//
//		// Your application logic here
//		log.Info().Msg("application running...")
//
//		// Application will automatically wait for graceful shutdown when function exits
//	}
func SetupGracefulShutdown(handlers []func() error, signals ...os.Signal) func() {
	ctx := OnSignal(handlers, signals...)
	return func() {
		WaitForShutdown(ctx)
	}
}
