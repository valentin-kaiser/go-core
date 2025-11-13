package zlog

import "os"

// ModeDetector is a function type for detecting interactive mode
type ModeDetector func() bool

// detector can be set customly to detect interactive mode
var detector ModeDetector

// Interactive checks if the application is running in interactive mode
// by checking if stdout is available.
func Interactive() bool {
	if detector != nil {
		return detector()
	}

	// Fallback: Try to get file info for stdout
	stat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}

	return (stat.Mode() & os.ModeCharDevice) != 0
}

// SetModeDetector allows the user to provide a custom function to detect interactive mode
func SetModeDetector(d ModeDetector) {
	detector = d
}
