package etcd

import (
	"context"
	"encoding/json"
	"strings"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

const (
	// ServiceAdded indicates a new service instance was registered.
	ServiceAdded ServiceEventType = iota + 1
	// ServiceModified indicates an existing service instance was updated.
	ServiceModified
	// ServiceRemoved indicates a service instance was deregistered or its
	// lease expired.
	ServiceRemoved
)

// ServiceEventType enumerates the kinds of service-registry change events.
type ServiceEventType int

// String returns a human-readable name for the event type.
func (t ServiceEventType) String() string {
	switch t {
	case ServiceAdded:
		return eventNameAdded
	case ServiceModified:
		return eventNameModified
	case ServiceRemoved:
		return eventNameRemoved
	}
	return eventNameUnknown
}

// ServiceEvent describes a single change in the service registry.
type ServiceEvent struct {
	Type ServiceEventType
	Info Descriptor
}

// Discover returns a snapshot of all currently registered instances of the
// given service name.
func (c *Client) Discover(ctx context.Context, name string) ([]Descriptor, error) {
	if strings.TrimSpace(name) == "" {
		return nil, apperror.NewError("service name must not be empty")
	}
	prefix := c.key(servicesKeyGroup, name) + keySeparator
	resp, err := c.raw.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, apperror.NewError("listing services from etcd failed").AddError(err)
	}
	out := make([]Descriptor, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var info Descriptor
		err := json.Unmarshal(kv.Value, &info)
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// WatchService subscribes to changes for the given service name. The handler
// is invoked from a single goroutine; long-running work should be dispatched
// elsewhere. The returned cancel function stops the watch goroutine.
func (c *Client) WatchService(ctx context.Context, name string, handler func(ServiceEvent)) (func(), error) {
	if strings.TrimSpace(name) == "" {
		return nil, apperror.NewError("service name must not be empty")
	}
	if handler == nil {
		return nil, apperror.NewError("service watch handler must not be nil")
	}

	prefix := c.key(servicesKeyGroup, name) + keySeparator
	watchCtx, cancel := context.WithCancel(ctx)
	ch := c.raw.Watch(watchCtx, prefix, clientv3.WithPrefix(), clientv3.WithPrevKV())

	go watchServiceEvents(watchCtx, ch, handler)

	return cancel, nil
}

func watchServiceEvents(ctx context.Context, ch clientv3.WatchChan, handler func(ServiceEvent)) {
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-ch:
			if !ok || resp.Canceled {
				return
			}
			for _, ev := range resp.Events {
				emit(ev, handler)
			}
		}
	}
}

func emit(ev *clientv3.Event, handler func(ServiceEvent)) {
	switch ev.Type {
	case mvccpb.PUT:
		var info Descriptor
		err := json.Unmarshal(ev.Kv.Value, &info)
		if err != nil {
			return
		}
		t := ServiceAdded
		if ev.PrevKv != nil {
			t = ServiceModified
		}
		handler(ServiceEvent{Type: t, Info: info})
	case mvccpb.DELETE:
		var info Descriptor
		if ev.PrevKv != nil {
			err := json.Unmarshal(ev.PrevKv.Value, &info)
			if err == nil {
				handler(ServiceEvent{Type: ServiceRemoved, Info: info})
				return
			}
		}
		// Fall back to the key for identification when prev-kv is absent.
		segments := strings.Split(string(ev.Kv.Key), keySeparator)
		if len(segments) >= minKeySegments {
			info.ID = segments[len(segments)-1]
			info.Name = segments[len(segments)-minKeySegments]
		}
		handler(ServiceEvent{Type: ServiceRemoved, Info: info})
	}
}
