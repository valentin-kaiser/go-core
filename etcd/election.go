package etcd

import (
	"context"
	"strings"
	"time"

	"go.etcd.io/etcd/client/v3/concurrency"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Election represents an ongoing leader election. The election runs in the
// background until Resign is called or the underlying session is lost.
type Election struct {
	session  *concurrency.Session
	election *concurrency.Election
	identity string
	key      string

	campaignCtx    context.Context
	campaignCancel context.CancelFunc
	campaignErr    chan error
}

const defaultElectionTTL = 15 * time.Second

// Campaign begins campaigning for leadership of the given election key. The
// returned Election immediately attempts to become leader; callers can use
// Leader, Observe and Resign to interact with it. Campaign blocks until
// either leadership is acquired, the context is cancelled, or the underlying
// session is lost.
//
// identity is the value advertised to observers; it is typically a hostname,
// instance id, or any unique string callers want to expose.
func (c *Client) Campaign(ctx context.Context, key, identity string, ttl time.Duration) (*Election, error) {
	if strings.TrimSpace(key) == "" {
		return nil, apperror.NewError("election key must not be empty")
	}
	if strings.TrimSpace(identity) == "" {
		return nil, apperror.NewError("election identity must not be empty")
	}

	if ttl <= 0 {
		ttl = defaultElectionTTL
	}
	seconds := int(ttl / time.Second)
	if seconds < 1 {
		seconds = 1
	}

	session, err := concurrency.NewSession(c.raw, concurrency.WithTTL(seconds), concurrency.WithContext(ctx))
	if err != nil {
		return nil, apperror.NewError("creating etcd session failed").AddError(err)
	}

	fullKey := c.key("elections", key)
	el := concurrency.NewElection(session, fullKey)

	if err := el.Campaign(ctx, identity); err != nil {
		_ = session.Close()
		return nil, apperror.NewError("campaigning for leadership failed").AddError(err)
	}

	return &Election{
		session:  session,
		election: el,
		identity: identity,
		key:      fullKey,
	}, nil
}

// Identity returns the value advertised by this campaigner.
func (e *Election) Identity() string { return e.identity }

// Key returns the fully qualified election key.
func (e *Election) Key() string { return e.key }

// Leader returns the identity of the current leader of this election.
// An empty string with no error indicates the election currently has no
// leader (e.g. all participants resigned).
func (e *Election) Leader(ctx context.Context) (string, error) {
	resp, err := e.election.Leader(ctx)
	if err != nil {
		if err == concurrency.ErrElectionNoLeader {
			return "", nil
		}
		return "", apperror.NewError("reading election leader failed").AddError(err)
	}
	if len(resp.Kvs) == 0 {
		return "", nil
	}
	return string(resp.Kvs[0].Value), nil
}

// Observe returns a channel that emits the identity of the current leader
// every time it changes. The channel is closed when ctx is cancelled or the
// underlying session is lost.
func (e *Election) Observe(ctx context.Context) <-chan string {
	out := make(chan string, 1)
	source := e.election.Observe(ctx)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-e.session.Done():
				return
			case resp, ok := <-source:
				if !ok {
					return
				}
				if len(resp.Kvs) == 0 {
					continue
				}
				select {
				case out <- string(resp.Kvs[0].Value):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// Resign relinquishes leadership (if held) and closes the underlying
// session. After Resign the Election must not be used again.
func (e *Election) Resign(ctx context.Context) error {
	if e == nil {
		return nil
	}
	resignErr := e.election.Resign(ctx)
	closeErr := e.session.Close()
	if resignErr != nil {
		return apperror.NewError("resigning leadership failed").AddError(resignErr)
	}
	if closeErr != nil {
		return apperror.NewError("closing etcd session failed").AddError(closeErr)
	}
	return nil
}

// Done returns a channel closed when the underlying session is lost.
func (e *Election) Done() <-chan struct{} { return e.session.Done() }
