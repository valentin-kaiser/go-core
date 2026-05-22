package etcd_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/etcd"
)

func TestRegisterAndDiscover(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info := etcd.Descriptor{
		Name:    "billing",
		ID:      "billing-1",
		Address: "127.0.0.1:9001",
		Metadata: map[string]string{
			"region": "eu-west",
		},
	}

	reg, err := cli.Register(ctx, info, 5*time.Second)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer func() { _ = reg.Deregister(context.Background()) }()

	endpoints, err := cli.Discover(ctx, "billing")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Address != info.Address {
		t.Fatalf("expected address %q, got %q", info.Address, endpoints[0].Address)
	}
	if endpoints[0].Metadata["region"] != "eu-west" {
		t.Fatalf("metadata not propagated: %+v", endpoints[0].Metadata)
	}
}

func TestDeregisterRemovesEntry(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg, err := cli.Register(ctx, etcd.Descriptor{
		Name:    "audit",
		ID:      "audit-1",
		Address: "10.0.0.1:1",
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := reg.Deregister(ctx); err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	endpoints, err := cli.Discover(ctx, "audit")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("expected 0 endpoints after Deregister, got %d", len(endpoints))
	}
}

func TestWatchServiceEvents(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu     sync.Mutex
		events []etcd.ServiceEvent
		got    = make(chan struct{}, 4)
	)

	stop, err := cli.WatchService(ctx, "scheduler", func(ev etcd.ServiceEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		select {
		case got <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("WatchService: %v", err)
	}
	defer stop()

	// Give the watch time to subscribe.
	time.Sleep(200 * time.Millisecond)

	reg, err := cli.Register(ctx, etcd.Descriptor{
		Name:    "scheduler",
		ID:      "s-1",
		Address: "127.0.0.1:1234",
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	waitFor(t, got, "added event")

	if err := reg.Deregister(ctx); err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	waitFor(t, got, "removed event")

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}
	if events[0].Type != etcd.ServiceAdded {
		t.Fatalf("expected first event ServiceAdded, got %s", events[0].Type)
	}
	sawRemoved := false
	for _, ev := range events {
		if ev.Type == etcd.ServiceRemoved {
			sawRemoved = true
			break
		}
	}
	if !sawRemoved {
		t.Fatal("expected a ServiceRemoved event after Deregister")
	}
}

func waitFor(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}
