// Package await waits for conditions in tests without fixed sleeps.
package await

import (
	"testing"
	"time"
)

const pollInterval = 5 * time.Millisecond

// Until polls condition until it returns true and fails the test if timeout passes first.
func Until(tb testing.TB, timeout time.Duration, condition func() bool, description string) {
	tb.Helper()

	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			tb.Fatalf("timed out after %v waiting for %s", timeout, description)
		}
		time.Sleep(pollInterval)
	}
}

// Receive returns the next value of ch and fails the test if timeout passes first.
func Receive[T any](tb testing.TB, ch <-chan T, timeout time.Duration, description string) T {
	tb.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(timeout):
		tb.Fatalf("timed out after %v waiting for %s", timeout, description)
		var zero T
		return zero
	}
}
