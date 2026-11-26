package etcd

import (
	"context"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"

	"github.com/valentin-kaiser/go-core/v2/apperror"
)

const defaultLockTTL = 30 * time.Second

// Lock represents an acquired distributed mutex. The underlying etcd session
// keeps a lease alive for the lifetime of the lock; if the holding process
// dies, the lock is released automatically once the lease expires.
type Lock struct {
	session *concurrency.Session
	mutex   *concurrency.Mutex
	key     string
}

// Lock acquires a distributed mutex at the given key (resolved under the
// client's configured prefix) and returns a handle the caller can use to
// release it. The call blocks until the lock is acquired or the context is
// cancelled.
//
// A zero ttl falls back to the package default (30 seconds). The minimum
// effective TTL accepted by etcd concurrency sessions is one second.
func (c *Client) Lock(ctx context.Context, key string, ttl time.Duration) (*Lock, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperror.NewError("lock key must not be empty")
	}

	if ttl <= 0 {
		ttl = defaultLockTTL
	}
	seconds := max(int(ttl/time.Second), 1)

	session, err := concurrency.NewSession(c.raw, concurrency.WithTTL(seconds), concurrency.WithContext(ctx))
	if err != nil {
		return nil, apperror.NewError("creating etcd session failed").AddError(err)
	}

	fullKey := c.key(locksKeyGroup, key)
	mutex := concurrency.NewMutex(session, fullKey)
	err = mutex.Lock(ctx)
	if err != nil {
		return nil, closeSession(session, apperror.NewError("acquiring etcd lock failed").AddError(err))
	}

	return &Lock{session: session, mutex: mutex, key: fullKey}, nil
}

// TryLock attempts to acquire the lock without blocking. It returns
// (nil, nil) when the lock is currently held by another holder.
func (c *Client) TryLock(ctx context.Context, key string, ttl time.Duration) (*Lock, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperror.NewError("lock key must not be empty")
	}

	if ttl <= 0 {
		ttl = defaultLockTTL
	}
	seconds := max(int(ttl/time.Second), 1)

	fullKey := c.key(locksKeyGroup, key)

	// A session is a lease plus a keep-alive stream, which costs several milliseconds to set
	// up. When somebody holds the lock there is no point in creating one just to be refused,
	// so look at the lock first. The mutex keeps one key per holder or waiter under key + "/".
	held, err := c.raw.Get(ctx, fullKey+keySeparator, clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		return nil, apperror.NewError("attempting etcd lock failed").AddError(err)
	}
	if held.Count > 0 {
		return nil, nil //nolint:nilnil // documented contract: a nil lock without error means the lock is held elsewhere
	}

	session, err := concurrency.NewSession(c.raw, concurrency.WithTTL(seconds), concurrency.WithContext(ctx))
	if err != nil {
		return nil, apperror.NewError("creating etcd session failed").AddError(err)
	}

	mutex := concurrency.NewMutex(session, fullKey)
	err = mutex.TryLock(ctx)
	if err != nil {
		if err == concurrency.ErrLocked {
			apperror.Catch(session.Close, "closing etcd session failed")
			return nil, nil //nolint:nilnil // documented contract: a nil lock without error means the lock is held elsewhere
		}
		return nil, closeSession(session, apperror.NewError("attempting etcd lock failed").AddError(err))
	}

	return &Lock{session: session, mutex: mutex, key: fullKey}, nil
}

// Key returns the fully qualified key the lock is held on.
func (l *Lock) Key() string { return l.key }

// Unlock releases the lock and closes the underlying session.
func (l *Lock) Unlock(ctx context.Context) error {
	if l == nil {
		return nil
	}
	err := l.mutex.Unlock(ctx)
	closeErr := l.session.Close()
	if err != nil {
		return apperror.NewError("releasing etcd lock failed").AddError(err)
	}
	if closeErr != nil {
		return apperror.NewError("closing etcd session failed").AddError(closeErr)
	}
	return nil
}

// Done returns a channel closed when the underlying session is lost (for
// example because the lease expired due to a network partition). Callers
// holding work-impacting locks should monitor this channel and abort.
func (l *Lock) Done() <-chan struct{} { return l.session.Done() }

// closeSession closes the session of a failed operation and returns cause,
// extended by the close error if closing failed as well.
func closeSession(session *concurrency.Session, cause apperror.Error) error {
	err := session.Close()
	if err != nil {
		return cause.AddError(err)
	}
	return cause
}
