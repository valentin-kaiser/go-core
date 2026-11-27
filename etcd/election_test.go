package etcd_test

import (
	"context"
	"testing"
	"time"
)

func TestElectionLeadershipTransfer(t *testing.T) {
	const (
		electionTimeout = 15 * time.Second
		electionName    = "scheduler"
		firstNode       = "node-1"
		secondNode      = "node-2"
		blockedWait     = 300 * time.Millisecond
	)

	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), electionTimeout)
	defer cancel()

	leader, err := cli.Campaign(ctx, electionName, firstNode, leaseTTL)
	if err != nil {
		t.Fatalf("Campaign %s: %v", firstNode, err)
	}

	identity, err := leader.Leader(ctx)
	if err != nil {
		t.Fatalf("Leader: %v", err)
	}
	if identity != firstNode {
		t.Fatalf("expected %s as leader, got %q", firstNode, identity)
	}

	// Second candidate must block until first resigns.
	type result struct {
		err      error
		acquired time.Time
	}
	got := make(chan result, 1)
	resigned := make(chan error, 1)
	go func() {
		next, err := cli.Campaign(ctx, electionName, secondNode, leaseTTL)
		if err != nil {
			got <- result{err: err}
			return
		}
		got <- result{acquired: time.Now()}
		resigned <- next.Resign(ctx)
	}()

	select {
	case <-got:
		t.Fatalf("%s acquired leadership while %s still leader", secondNode, firstNode)
	case <-time.After(blockedWait):
	}

	err = leader.Resign(ctx)
	if err != nil {
		t.Fatalf("Resign %s: %v", firstNode, err)
	}

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("%s Campaign: %v", secondNode, r.err)
		}
	case <-time.After(requestTimeout):
		t.Fatalf("%s never acquired leadership after %s resigned", secondNode, firstNode)
	}

	select {
	case err := <-resigned:
		if err != nil {
			t.Fatalf("Resign %s: %v", secondNode, err)
		}
	case <-time.After(requestTimeout):
		t.Fatalf("%s never finished resigning", secondNode)
	}
}
