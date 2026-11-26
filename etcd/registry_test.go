package etcd_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/etcd"
	"github.com/valentin-kaiser/go-core/v2/internal/await"
)

const (
	billingService   = "billing"
	billingID        = "billing-1"
	billingAddress   = loopbackHost + ":9001"
	billingRegion    = "eu-west"
	regionKey        = "region"
	auditService     = "audit"
	auditID          = "audit-1"
	auditAddress     = "10.0.0.1:1"
	schedulerService = "scheduler"
	schedulerID      = "s-1"
	schedulerAddress = loopbackHost + ":1234"
	probeID          = "probe"
	probeAddress     = loopbackHost + ":1"
	probeRetryEvery  = 250 * time.Millisecond
	probeTimeout     = 10 * time.Second
)

func TestRegisterAndDiscover(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	info := etcd.Descriptor{
		Name:    billingService,
		ID:      billingID,
		Address: billingAddress,
		Metadata: map[string]string{
			regionKey: billingRegion,
		},
	}

	reg, err := cli.Register(ctx, info, leaseTTL)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		err := reg.Deregister(context.Background())
		if err != nil {
			t.Errorf("Deregister: %v", err)
		}
	})

	endpoints, err := cli.Discover(ctx, billingService)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Address != info.Address {
		t.Fatalf("expected address %q, got %q", info.Address, endpoints[0].Address)
	}
	if endpoints[0].Metadata[regionKey] != billingRegion {
		t.Fatalf("metadata not propagated: %+v", endpoints[0].Metadata)
	}
}

func TestDeregisterRemovesEntry(t *testing.T) {
	cli := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	reg, err := cli.Register(ctx, etcd.Descriptor{
		Name:    auditService,
		ID:      auditID,
		Address: auditAddress,
	}, leaseTTL)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	err = reg.Deregister(ctx)
	if err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	endpoints, err := cli.Discover(ctx, auditService)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("expected 0 endpoints after Deregister, got %d", len(endpoints))
	}
}

func TestWatchServiceEvents(t *testing.T) {
	cli := newTestClient(t)

	ctx := t.Context()

	var (
		mu         sync.Mutex
		events     []etcd.ServiceEvent
		got        = make(chan struct{}, 4)
		probeAcked = make(chan struct{}, 4)
	)

	stop, err := cli.WatchService(ctx, schedulerService, func(ev etcd.ServiceEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		signal := got
		if ev.Info.ID == probeID {
			signal = probeAcked
		}
		select {
		case signal <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("WatchService: %v", err)
	}
	defer stop()

	awaitSubscription(t, cli, probeAcked)

	reg, err := cli.Register(ctx, etcd.Descriptor{
		Name:    schedulerService,
		ID:      schedulerID,
		Address: schedulerAddress,
	}, leaseTTL)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	waitFor(t, got, "added event")

	err = reg.Deregister(ctx)
	if err != nil {
		t.Fatalf("Deregister: %v", err)
	}

	waitFor(t, got, "removed event")

	mu.Lock()
	defer mu.Unlock()
	var own []etcd.ServiceEvent
	for _, ev := range events {
		if ev.Info.ID == schedulerID {
			own = append(own, ev)
		}
	}
	if len(own) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(own))
	}
	if own[0].Type != etcd.ServiceAdded {
		t.Fatalf("expected first event ServiceAdded, got %s", own[0].Type)
	}
	sawRemoved := false
	for _, ev := range own {
		if ev.Type == etcd.ServiceRemoved {
			sawRemoved = true
			break
		}
	}
	if !sawRemoved {
		t.Fatal("expected a ServiceRemoved event after Deregister")
	}
}

// awaitSubscription registers a probe instance until the watch reports it. The watch
// subscribes asynchronously and misses changes made before that.
func awaitSubscription(t *testing.T, cli *etcd.Client, observed <-chan struct{}) {
	t.Helper()

	await.Until(t, probeTimeout, func() bool {
		reg, err := cli.Register(t.Context(), etcd.Descriptor{
			Name:    schedulerService,
			ID:      probeID,
			Address: probeAddress,
		}, leaseTTL)
		if err != nil {
			t.Fatalf("Register probe: %v", err)
		}
		seen := false
		select {
		case <-observed:
			seen = true
		case <-time.After(probeRetryEvery):
		}
		err = reg.Deregister(t.Context())
		if err != nil {
			t.Fatalf("Deregister probe: %v", err)
		}
		return seen
	}, "the service watch to subscribe")
}

func waitFor(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	await.Receive(t, c, requestTimeout, what)
}
