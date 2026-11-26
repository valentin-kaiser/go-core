package etcd_test

import (
	"context"
	"testing"
	"time"
)

const lockOperationTimeout = 10 * time.Second

func TestLockSerializesAccess(t *testing.T) {
	const lockKey = "shared"

	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), lockOperationTimeout)
	defer cancel()

	first, err := cli.Lock(ctx, lockKey, leaseTTL)
	if err != nil {
		t.Fatalf("Lock first: %v", err)
	}

	// Second TryLock must observe the lock as held.
	second, err := cli.TryLock(ctx, lockKey, leaseTTL)
	if err != nil {
		t.Fatalf("TryLock second: %v", err)
	}
	if second != nil {
		err = second.Unlock(ctx)
		if err != nil {
			t.Errorf("Unlock second: %v", err)
		}
		t.Fatal("expected TryLock to fail while first holder is active")
	}

	err = first.Unlock(ctx)
	if err != nil {
		t.Fatalf("Unlock first: %v", err)
	}

	// After Unlock TryLock must succeed.
	third, err := cli.TryLock(ctx, lockKey, leaseTTL)
	if err != nil {
		t.Fatalf("TryLock third: %v", err)
	}
	if third == nil {
		t.Fatal("expected TryLock to succeed after Unlock")
	}
	err = third.Unlock(ctx)
	if err != nil {
		t.Fatalf("Unlock third: %v", err)
	}
}

func TestLockBlocksUntilReleased(t *testing.T) {
	const (
		lockKey     = "queued"
		blockedWait = 300 * time.Millisecond
	)

	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), lockOperationTimeout)
	defer cancel()

	held, err := cli.Lock(ctx, lockKey, leaseTTL)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	acquired := make(chan struct{})
	unlocked := make(chan error, 1)
	go func() {
		l, err := cli.Lock(ctx, lockKey, leaseTTL)
		if err != nil {
			t.Errorf("Lock queued: %v", err)
			return
		}
		close(acquired)
		unlocked <- l.Unlock(ctx)
	}()

	select {
	case <-acquired:
		t.Fatal("second Lock acquired while first was still held")
	case <-time.After(blockedWait):
	}

	err = held.Unlock(ctx)
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	select {
	case <-acquired:
	case <-time.After(requestTimeout):
		t.Fatal("second Lock never acquired after first was released")
	}

	select {
	case err := <-unlocked:
		if err != nil {
			t.Fatalf("Unlock queued: %v", err)
		}
	case <-time.After(requestTimeout):
		t.Fatal("second Lock never unlocked")
	}
}
