package cache_test

import "testing"

func closeOnCleanup(tb testing.TB, name string, c interface{ Close() error }) {
	tb.Helper()

	tb.Cleanup(func() {
		err := c.Close()
		if err != nil {
			tb.Errorf("closing %s failed: %v", name, err)
		}
	})
}
