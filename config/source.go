package config

import (
	"context"
	"errors"
	"strings"
)

// ErrNotFound is returned by a Source when the requested configuration does not
// yet exist in the backing store. When Read() encounters this from the base
// source, it bootstraps the source by saving the current default configuration
// and then loads it again.
var ErrNotFound = errors.New("configuration not found")

// ErrReadOnly may be returned by a Source from Save to indicate that the
// underlying store does not support writes. Manager.Write treats this as a
// non-fatal condition for overlays but as fatal for the base source.
var ErrReadOnly = errors.New("configuration source is read-only")

// ErrWatchUnsupported may be returned by a Source from Watch to indicate that
// the underlying store does not support change notifications. StartWatch
// silently skips sources that return this error.
var ErrWatchUnsupported = errors.New("configuration source does not support watch")

// Source abstracts the backing store of a configuration. Built-in sources
// include a YAML file source (the default) and the etcd source provided by the
// etcd package. Custom sources may implement this interface to plug additional
// stores (Consul, Vault, HTTP endpoints, ...).
//
// All methods are invoked from the manager goroutine and may be called
// concurrently with Watch callbacks; implementations must be safe for use by
// multiple goroutines.
type Source interface {
	// Name returns a short, human readable identifier of the source used in
	// log and error messages (e.g. "file", "etcd").
	Name() string

	// Load returns the full set of configuration values as a flat map keyed
	// by lower-case dotted paths (e.g. "server.port"). Implementations must
	// return ErrNotFound if the configuration has not been initialised yet.
	Load(ctx context.Context) (map[string]interface{}, error)

	// Save persists the given configuration. Implementations that are
	// read-only must return ErrReadOnly.
	Save(ctx context.Context, c Config) error

	// Watch subscribes to changes in the source. The callback is invoked with
	// the freshly loaded values on every change. The returned cancel function
	// stops the watch. Implementations that do not support watching must
	// return ErrWatchUnsupported.
	Watch(ctx context.Context, onChange func(map[string]interface{})) (cancel func(), err error)
}

// mergeValues combines several flat dotted-key maps in order, with later maps
// overriding earlier ones on key collision. nil maps are ignored.
func mergeValues(maps ...map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for _, m := range maps {
		for k, v := range m {
			merged[strings.ToLower(k)] = v
		}
	}
	return merged
}
