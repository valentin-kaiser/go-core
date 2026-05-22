package etcd_test

import (
	"context"
	"testing"
	"time"
)

func TestLockSerializesAccess(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first, err := cli.Lock(ctx, "shared", 5*time.Second)
	if err != nil {
		t.Fatalf("Lock first: %v", err)
	}

	// Second TryLock must observe the lock as held.
	second, err := cli.TryLock(ctx, "shared", 5*time.Second)
	if err != nil {
		t.Fatalf("TryLock second: %v", err)
	}
	if second != nil {
		_ = second.Unlock(ctx)
		t.Fatal("expected TryLock to fail while first holder is active")
	}

	if err := first.Unlock(ctx); err != nil {
		t.Fatalf("Unlock first: %v", err)
	}

	// After Unlock TryLock must succeed.
	third, err := cli.TryLock(ctx, "shared", 5*time.Second)
	if err != nil {
		t.Fatalf("TryLock third: %v", err)
	}
	if third == nil {
		t.Fatal("expected TryLock to succeed after Unlock")
	}
	if err := third.Unlock(ctx); err != nil {
		t.Fatalf("Unlock third: %v", err)
	}
}

func TestLockBlocksUntilReleased(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	held, err := cli.Lock(ctx, "queued", 5*time.Second)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		l, err := cli.Lock(ctx, "queued", 5*time.Second)
		if err != nil {
			return
		}
		close(acquired)
		_ = l.Unlock(ctx)
	}()

	select {
	case <-acquired:
		t.Fatal("second Lock acquired while first was still held")
	case <-time.After(300 * time.Millisecond):
	}

	if err := held.Unlock(ctx); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second Lock never acquired after first was released")
	}
}
