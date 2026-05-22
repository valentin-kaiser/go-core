package etcd_test

import (
	"context"
	"testing"
	"time"
)

func TestElectionLeadershipTransfer(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	leader, err := cli.Campaign(ctx, "scheduler", "node-1", 5*time.Second)
	if err != nil {
		t.Fatalf("Campaign node-1: %v", err)
	}

	identity, err := leader.Leader(ctx)
	if err != nil {
		t.Fatalf("Leader: %v", err)
	}
	if identity != "node-1" {
		t.Fatalf("expected node-1 as leader, got %q", identity)
	}

	// Second candidate must block until first resigns.
	type result struct {
		err      error
		acquired time.Time
	}
	got := make(chan result, 1)
	go func() {
		next, err := cli.Campaign(ctx, "scheduler", "node-2", 5*time.Second)
		if err != nil {
			got <- result{err: err}
			return
		}
		got <- result{acquired: time.Now()}
		_ = next.Resign(ctx)
	}()

	select {
	case <-got:
		t.Fatal("node-2 acquired leadership while node-1 still leader")
	case <-time.After(300 * time.Millisecond):
	}

	if err := leader.Resign(ctx); err != nil {
		t.Fatalf("Resign node-1: %v", err)
	}

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("node-2 Campaign: %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("node-2 never acquired leadership after node-1 resigned")
	}
}
