package etcd

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

const defaultRegisterTTL = 15 * time.Second

// Descriptor describes a single service instance registered in etcd. Name
// groups related instances; ID uniquely identifies one instance within a
// name. Address is the dial target (typically host:port). Metadata holds
// arbitrary string tags such as version, region or capabilities.
type Descriptor struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Address  string            `json:"address"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate ensures the service information is sufficient for registration.
func (s Descriptor) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return apperror.NewError("service info name must not be empty")
	}
	if strings.TrimSpace(s.ID) == "" {
		return apperror.NewError("service info id must not be empty")
	}
	if strings.TrimSpace(s.Address) == "" {
		return apperror.NewError("service info address must not be empty")
	}
	return nil
}

// Registration represents an active service registration. It keeps the
// associated etcd lease alive in the background until Deregister is called
// or the context used to create it is cancelled.
type Registration struct {
	cli     *Client
	info    Descriptor
	leaseID clientv3.LeaseID
	key     string

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	deregErr error
	closed   bool
}

// Register stores the service info in etcd under a freshly granted lease and
// starts a background keep-alive routine. The lease is automatically revoked
// when Deregister is called or when the parent context is cancelled.
//
// A zero ttl falls back to the package default (15 seconds). The minimum
// effective TTL accepted by etcd is one second; smaller values are raised.
func (c *Client) Register(ctx context.Context, info Descriptor, ttl time.Duration) (*Registration, error) {
	err := info.Validate()
	if err != nil {
		return nil, apperror.NewError("validating service descriptor failed").AddError(err)
	}

	if ttl <= 0 {
		ttl = defaultRegisterTTL
	}
	seconds := max(int64(ttl/time.Second), 1)

	leaseResp, err := c.raw.Grant(ctx, seconds)
	if err != nil {
		return nil, apperror.NewError("granting etcd lease failed").AddError(err)
	}

	payload, err := json.Marshal(info)
	if err != nil {
		return nil, apperror.NewError("encoding service info failed").AddError(err)
	}

	key := c.key(servicesKeyGroup, info.Name, info.ID)
	_, err = c.raw.Put(ctx, key, string(payload), clientv3.WithLease(leaseResp.ID))
	if err != nil {
		return nil, c.abortRegistration(ctx, leaseResp.ID, apperror.NewError("writing service registration failed").AddError(err))
	}

	// The keep-alive must outlive the caller context: its cancellation is handled below by deregistering.
	keepAliveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ka, err := c.raw.KeepAlive(keepAliveCtx, leaseResp.ID)
	if err != nil {
		cancel()
		return nil, c.abortRegistration(ctx, leaseResp.ID, apperror.NewError("starting etcd keep-alive failed").AddError(err))
	}

	reg := &Registration{
		cli:     c,
		info:    info,
		leaseID: leaseResp.ID,
		key:     key,
		cancel:  cancel,
		done:    make(chan struct{}),
	}

	go reg.keepAlive(ctx, keepAliveCtx, ka)

	return reg, nil
}

// Info returns the service info backing this registration.
func (r *Registration) Info() Descriptor { return r.info }

// LeaseID returns the etcd lease id underpinning this registration.
func (r *Registration) LeaseID() clientv3.LeaseID { return r.leaseID }

// Deregister revokes the etcd lease, deletes the registration key and stops
// the background keep-alive routine. It is safe to call more than once;
// subsequent calls return the result of the first call.
func (r *Registration) Deregister(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return r.deregErr
	}
	r.closed = true
	r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
	}

	_, err := r.cli.raw.Revoke(ctx, r.leaseID)
	if err != nil {
		r.deregErr = apperror.NewError("revoking etcd lease failed").AddError(err)
	}
	return r.deregErr
}

// keepAlive drains the keep-alive responses until the lease is lost, and deregisters the
// service when the parent context is cancelled.
func (r *Registration) keepAlive(ctx, keepAliveCtx context.Context, ka <-chan *clientv3.LeaseKeepAliveResponse) {
	defer close(r.done)
	for {
		select {
		case <-keepAliveCtx.Done():
			return
		case <-ctx.Done():
			apperror.Catch(func() error { return r.Deregister(context.WithoutCancel(ctx)) }, "deregistering service after context cancellation failed")
			return
		case _, ok := <-ka:
			if !ok {
				return
			}
		}
	}
}

// abortRegistration revokes the lease of a registration that did not complete and
// returns cause, extended by the revoke error if revoking failed as well.
func (c *Client) abortRegistration(ctx context.Context, leaseID clientv3.LeaseID, cause apperror.Error) error {
	_, err := c.raw.Revoke(context.WithoutCancel(ctx), leaseID)
	if err != nil {
		return cause.AddError(err)
	}
	return cause
}
