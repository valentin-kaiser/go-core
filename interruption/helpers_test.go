package interruption_test

import (
	"testing"

	"github.com/valentin-kaiser/go-core/flag"
)

// setDebug sets flag.Debug for the test and restores the previous value afterwards.
func setDebug(tb testing.TB, debug bool) {
	tb.Helper()

	original := flag.Debug
	flag.Debug = debug
	tb.Cleanup(func() { flag.Debug = original })
}
