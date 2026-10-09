package logging

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

	// Fallback: Try to get file info for stdout. Besides terminals, stdout may be a
	// pipe or socket (docker without tty, air, process managers) or a redirected
	// file; console output is still wanted there.
	stat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}

	return stat.Mode()&(os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket) != 0
}

// terminal reports whether stdout is an actual terminal (supports colors)
func terminal() bool {
	stat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

// SetModeDetector allows the user to provide a custom function to detect interactive mode
func SetModeDetector(d ModeDetector) {
	detector = d
}
