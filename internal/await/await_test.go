package await_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/internal/await"
)

const (
	generousTimeout = 5 * time.Second
	receivedValue   = 42
)

func TestUntilReturnsWhenConditionHolds(t *testing.T) {
	var ready atomic.Bool
	go ready.Store(true)

	await.Until(t, generousTimeout, ready.Load, "the flag to be set")
}

func TestReceiveReturnsSentValue(t *testing.T) {
	ch := make(chan int, 1)
	ch <- receivedValue

	got := await.Receive(t, ch, generousTimeout, "a value")
	if got != receivedValue {
		t.Fatalf("Receive() = %d, want %d", got, receivedValue)
	}
}
