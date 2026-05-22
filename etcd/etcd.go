// Package etcd provides a thin, opinionated wrapper around the official
// etcd v3 client tailored for go-core applications.
//
// The package bundles four capabilities on top of a single *clientv3.Client:
//
//   - A config.Source implementation that lets the config package read,
//     write and watch configuration values stored in etcd, either as the
//     sole source (etcd-only mode) or as an overlay on top of a local YAML
//     file (hybrid mode).
//   - Service registration with TTL leases and automatic keep-alive plus
//     graceful deregistration.
//   - Service discovery: snapshot queries and change-streaming watches over
//     registered services.
//   - Distributed locks and leader election via the etcd concurrency
//     primitives.
//
// All capabilities share a single Client and use a caller-supplied key
// prefix (Config.Prefix) so multiple independent applications can coexist
// in the same etcd cluster. Endpoints, TLS and credentials must be supplied
// by the application implementor; no flags or environment lookups are
// performed by this package.
//
// Example - distributed config (etcd-only):
//
//	cli, err := etcd.New(ctx, etcd.Config{
//	    Endpoints: []string{"localhost:2379"},
//	    Prefix:    "/apps/myapp",
//	})
//	if err != nil {
//	    return err
//	}
//	defer cli.Close()
//
//	config.Manager().WithName("myapp").WithSource(cli.ConfigSource("myapp"))
//	if err := config.Register(cfg); err != nil { return err }
//	if err := config.Read(); err != nil { return err }
//	_ = config.StartWatch(ctx)
//
// Example - hybrid config (file base, etcd overlay):
//
//	config.Manager().WithName("myapp").WithOverlay(cli.ConfigSource("myapp"))
//	_ = config.Read()
//	_ = config.StartWatch(ctx)
//
// Example - service registration and discovery:
//
//	reg, err := cli.Register(ctx, etcd.ServiceInfo{
//	    Name:    "billing",
//	    ID:      "billing-1",
//	    Address: "10.0.0.5:8080",
//	}, 15*time.Second)
//	defer reg.Deregister(context.Background())
//
//	endpoints, err := cli.Discover(ctx, "billing")
//	cancel, err := cli.WatchService(ctx, "billing", func(ev etcd.ServiceEvent) {
//	    // ...
//	})
//	defer cancel()
//
// Example - distributed lock and election:
//
//	lock, err := cli.Lock(ctx, "/jobs/cleanup", 10*time.Second)
//	defer lock.Unlock(ctx)
//
//	el, err := cli.Campaign(ctx, "/leader/scheduler", "node-1", 10*time.Second)
//	defer el.Resign(context.Background())
package etcd

import (
	"context"
	"crypto/tls"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Config holds the connection parameters for an etcd client.
type Config struct {
	// Endpoints is the list of etcd endpoints (e.g. "host:2379"). At least
	// one endpoint must be provided.
	Endpoints []string
	// Username is the optional username used for authentication.
	Username string
	// Password is the optional password used for authentication.
	Password string
	// DialTimeout is the maximum time to wait for an initial connection.
	// When zero, a sane default (5 seconds) is used.
	DialTimeout time.Duration
	// TLS, when set, enables TLS using the provided configuration.
	TLS *tls.Config
	// Prefix is the key namespace under which all keys written by this
	// client live. It is prepended to every key used by ConfigSource,
	// Register, Discover, Lock, and Campaign. Defaults to "/go-core".
	Prefix string
	// AutoSyncInterval, when non-zero, enables periodic endpoint
	// auto-sync against the etcd cluster.
	AutoSyncInterval time.Duration
}

// Validate ensures the configuration is sufficient for dialing etcd.
func (c *Config) Validate() error {
	if c == nil {
		return apperror.NewError("etcd config is nil")
	}
	if len(c.Endpoints) == 0 {
		return apperror.NewError("etcd config must declare at least one endpoint")
	}
	for _, ep := range c.Endpoints {
		if strings.TrimSpace(ep) == "" {
			return apperror.NewError("etcd config contains an empty endpoint")
		}
	}
	if c.DialTimeout < 0 {
		return apperror.NewError("etcd config dial timeout must be non-negative")
	}
	return nil
}

const defaultPrefix = "/go-core"

// Client wraps a *clientv3.Client together with the resolved key prefix and
// a lifecycle context shared by all helpers in this package.
type Client struct {
	raw    *clientv3.Client
	prefix string
}

// New dials etcd using the provided configuration and returns a Client.
// The caller owns the returned client and must invoke Close to release the
// underlying resources.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, apperror.Wrap(err)
	}

	dialTimeout := cfg.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:        cfg.Endpoints,
		Username:         cfg.Username,
		Password:         cfg.Password,
		DialTimeout:      dialTimeout,
		TLS:              cfg.TLS,
		AutoSyncInterval: cfg.AutoSyncInterval,
		Context:          ctx,
	})
	if err != nil {
		return nil, apperror.NewError("dialing etcd failed").AddError(err)
	}

	prefix := strings.TrimRight(cfg.Prefix, "/")
	if prefix == "" {
		prefix = defaultPrefix
	}

	return &Client{raw: cli, prefix: prefix}, nil
}

// Close releases the underlying etcd client and any associated resources.
func (c *Client) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	if err := c.raw.Close(); err != nil {
		return apperror.NewError("closing etcd client failed").AddError(err)
	}
	return nil
}

// Raw returns the underlying *clientv3.Client for advanced usage that this
// package does not yet wrap. The returned client is owned by Client; do not
// call Close on it directly.
func (c *Client) Raw() *clientv3.Client { return c.raw }

// Prefix returns the configured key prefix.
func (c *Client) Prefix() string { return c.prefix }

// key joins the configured prefix with the given parts using '/' separators
// and normalises adjacent slashes.
func (c *Client) key(parts ...string) string {
	var b strings.Builder
	b.WriteString(c.prefix)
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if p == "" {
			continue
		}
		b.WriteByte('/')
		b.WriteString(p)
	}
	return b.String()
}
