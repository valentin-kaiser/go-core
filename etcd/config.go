package etcd

import (
	"context"
	"strings"

	clientv3 "go.etcd.io/etcd/client/v3"
	"gopkg.in/yaml.v2"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/config"
)

// source is a config.Source backed by etcd. Each leaf configuration
// value is stored as its own key under "{client-prefix}/config/{name}/{dotted.path}".
// The leaf value is YAML-encoded so that primitive types, slices and maps
// round-trip cleanly through the existing config.unmarshal logic.
type source struct {
	cli       *Client
	name      string
	keyPrefix string
}

// ConfigSource returns a config.Source that reads, writes and watches the
// configuration named "name" from etcd. Plug the returned source into the
// config package via config.Manager().WithSource(...) for etcd-only mode or
// config.Manager().WithOverlay(...) for a hybrid setup.
func (c *Client) ConfigSource(name string) config.Source {
	name = strings.Trim(name, "/")
	if name == "" {
		name = "config"
	}
	return &source{
		cli:       c,
		name:      name,
		keyPrefix: c.key("config", name) + "/",
	}
}

// Name implements config.Source.
func (s *source) Name() string { return "etcd" }

// Load implements config.Source.
func (s *source) Load(ctx context.Context) (map[string]interface{}, error) {
	resp, err := s.cli.raw.Get(ctx, s.keyPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, apperror.NewError("loading configuration from etcd failed").AddError(err)
	}
	if len(resp.Kvs) == 0 {
		return nil, config.ErrNotFound
	}

	values := make(map[string]interface{}, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		key := strings.TrimPrefix(string(kv.Key), s.keyPrefix)
		if key == "" {
			continue
		}
		var v interface{}
		if err := yaml.Unmarshal(kv.Value, &v); err != nil {
			return nil, apperror.NewErrorf("decoding etcd value at %q failed", string(kv.Key)).AddError(err)
		}
		values[strings.ToLower(key)] = v
	}
	return values, nil
}

// Save implements config.Source. It marshals the configuration to a flat
// dotted-key map and writes every leaf as an individual etcd key in a single
// transaction so observers receive a consistent snapshot.
func (s *source) Save(ctx context.Context, c config.Config) error {
	flat, err := flattenConfig(c)
	if err != nil {
		return apperror.Wrap(err)
	}

	// Wipe any keys that no longer exist before writing the new set so the
	// snapshot in etcd matches the in-memory configuration exactly. The
	// delete is issued outside the transaction because etcd rejects txns
	// whose ranges overlap with mutated keys.
	if _, err := s.cli.raw.Delete(ctx, s.keyPrefix, clientv3.WithPrefix()); err != nil {
		return apperror.NewError("clearing existing configuration in etcd failed").AddError(err)
	}

	ops := make([]clientv3.Op, 0, len(flat))
	for k, v := range flat {
		data, err := yaml.Marshal(v)
		if err != nil {
			return apperror.NewErrorf("encoding value for key %q failed", k).AddError(err)
		}
		ops = append(ops, clientv3.OpPut(s.keyPrefix+strings.ToLower(k), strings.TrimRight(string(data), "\n")))
	}

	if len(ops) == 0 {
		return nil
	}

	_, err = s.cli.raw.Txn(ctx).Then(ops...).Commit()
	if err != nil {
		return apperror.NewError("writing configuration to etcd failed").AddError(err)
	}
	return nil
}

// Watch implements config.Source. The returned cancel function stops the
// underlying watch goroutine.
func (s *source) Watch(ctx context.Context, onChange func(map[string]interface{})) (func(), error) {
	watchCtx, cancel := context.WithCancel(ctx)
	ch := s.cli.raw.Watch(watchCtx, s.keyPrefix, clientv3.WithPrefix())

	go func() {
		for {
			select {
			case <-watchCtx.Done():
				return
			case resp, ok := <-ch:
				if !ok {
					return
				}
				if resp.Canceled || len(resp.Events) == 0 {
					if resp.Canceled {
						return
					}
					continue
				}
				values, err := s.Load(watchCtx)
				if err != nil {
					// Either the keys were deleted or another transient
					// issue occurred; emit an empty map so the manager
					// can re-bootstrap if needed.
					values = map[string]interface{}{}
				}
				onChange(values)
			}
		}
	}()

	return cancel, nil
}

// flattenConfig marshals a Config to YAML and returns its flat dotted-key
// representation, mirroring the encoding used by the file source so the
// resulting map can be consumed directly by config.unmarshal.
func flattenConfig(c config.Config) (map[string]interface{}, error) {
	if c == nil {
		return nil, apperror.NewError("configuration is nil")
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return nil, apperror.NewError("marshalling configuration to yaml failed").AddError(err)
	}
	var decoded map[string]interface{}
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		return nil, apperror.NewError("unmarshalling configuration yaml failed").AddError(err)
	}
	out := make(map[string]interface{})
	flatten(decoded, "", out)
	return out, nil
}

// flatten mirrors the file source flattening so that nested maps become
// dotted-key entries while slices and scalars are kept as-is.
func flatten(data map[string]interface{}, prefix string, out map[string]interface{}) {
	for k, v := range data {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch nested := v.(type) {
		case map[string]interface{}:
			flatten(nested, key, out)
		case map[interface{}]interface{}:
			conv := make(map[string]interface{}, len(nested))
			for nk, nv := range nested {
				if ks, ok := nk.(string); ok {
					conv[ks] = nv
				}
			}
			flatten(conv, key, out)
		default:
			out[strings.ToLower(key)] = v
		}
	}
}
