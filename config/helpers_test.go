package config_test

import (
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/flag"
)

const watchTimeout = 5 * time.Second

// setFlagPath sets flag.Path for the test and restores the previous value afterwards.
func setFlagPath(tb testing.TB, path string) {
	tb.Helper()

	original := flag.Path
	flag.Path = path
	tb.Cleanup(func() { flag.Path = original })
}
