package config_test

import (
	"reflect"
	"testing"

	"github.com/valentin-kaiser/go-core/config"
)

const (
	unmarshalConfigName = "unmarshal-test"
	firstPort           = 8080
	secondPort          = 9090
	timeoutSeconds      = 30
	tagCount            = 2
)

type unmarshalLimits struct {
	Timeout int `yaml:"timeout"`
}

type unmarshalServer struct {
	Host    string           `yaml:"host"`
	Port    int              `yaml:"port"`
	Limits  unmarshalLimits  `yaml:"limits"`
	Backup  *unmarshalLimits `yaml:"backup"`
	Tags    []string         `yaml:"tags"`
	Skipped string           `yaml:"-"`
}

type unmarshalConfig struct {
	Servers    []unmarshalServer          `yaml:"servers"`
	Pointers   []*unmarshalServer         `yaml:"pointers"`
	Ports      []*int                     `yaml:"ports"`
	Names      []string                   `yaml:"names"`
	ByName     map[string]int             `yaml:"by_name"`
	StructsBy  map[string]unmarshalLimits `yaml:"structs_by"`
	PointersBy map[string]*unmarshalLimits
}

func (c *unmarshalConfig) Validate() error { return nil }

func readUnmarshalConfig(t *testing.T, values map[string]any) *unmarshalConfig {
	t.Helper()
	config.Reset()
	t.Cleanup(config.Reset)

	src := newMockSource("mock")
	for k, v := range values {
		src.setValue(k, v)
	}
	err := config.Manager().WithName(unmarshalConfigName).WithSource(src).Register(&unmarshalConfig{})
	if err != nil {
		t.Fatalf("registering configuration failed: %v", err)
	}
	err = config.Read()
	if err != nil {
		t.Fatalf("reading configuration failed: %v", err)
	}
	got, ok := config.Get().(*unmarshalConfig)
	if !ok {
		t.Fatalf("Get returned %T", config.Get())
	}
	return got
}

func TestUnmarshalSliceOfStructs(t *testing.T) {
	got := readUnmarshalConfig(t, map[string]any{
		"servers": []any{
			map[any]any{
				"host":    "generic-keys",
				"port":    firstPort,
				"limits":  map[any]any{"timeout": timeoutSeconds},
				"backup":  map[string]any{"timeout": timeoutSeconds},
				"tags":    []any{"a", "b"},
				"skipped": "ignored",
				"unknown": "ignored",
			},
			map[string]any{"host": "string-keys", "port": secondPort},
			"not a map",
		},
	})

	if len(got.Servers) != 3 {
		t.Fatalf("len(Servers) = %d, want 3", len(got.Servers))
	}
	first := got.Servers[0]
	if first.Host != "generic-keys" || first.Port != firstPort || first.Limits.Timeout != timeoutSeconds {
		t.Fatalf("first server = %+v", first)
	}
	if first.Backup == nil || first.Backup.Timeout != timeoutSeconds {
		t.Fatalf("first server backup = %+v", first.Backup)
	}
	if !reflect.DeepEqual(first.Tags, []string{"a", "b"}) || first.Skipped != "" {
		t.Fatalf("first server tags = %v, skipped = %q", first.Tags, first.Skipped)
	}
	second := got.Servers[1]
	if second.Host != "string-keys" || second.Port != secondPort {
		t.Fatalf("second server = %+v", second)
	}
	if !reflect.DeepEqual(got.Servers[2], unmarshalServer{}) {
		t.Fatalf("third server = %+v, want zero value", got.Servers[2])
	}
}

func TestUnmarshalSliceOfPointers(t *testing.T) {
	got := readUnmarshalConfig(t, map[string]any{
		"pointers": []any{
			map[any]any{"host": "pointer", "port": firstPort},
			map[string]any{"host": "second", "port": secondPort},
		},
		"ports": []any{firstPort, secondPort},
		"names": []any{"x", "y"},
	})

	if len(got.Pointers) != tagCount || got.Pointers[0] == nil || got.Pointers[1] == nil {
		t.Fatalf("Pointers = %v", got.Pointers)
	}
	if got.Pointers[0].Host != "pointer" || got.Pointers[1].Port != secondPort {
		t.Fatalf("Pointers = %+v, %+v", got.Pointers[0], got.Pointers[1])
	}
	if len(got.Ports) != tagCount || *got.Ports[0] != firstPort || *got.Ports[1] != secondPort {
		t.Fatalf("Ports = %v", got.Ports)
	}
	if !reflect.DeepEqual(got.Names, []string{"x", "y"}) {
		t.Fatalf("Names = %v", got.Names)
	}
}

func TestUnmarshalMaps(t *testing.T) {
	got := readUnmarshalConfig(t, map[string]any{
		"by_name":    map[any]any{"one": firstPort, "two": secondPort},
		"structs_by": map[string]any{"fast": map[string]any{"timeout": timeoutSeconds}},
		"pointersby": map[string]any{"slow": map[string]any{"timeout": timeoutSeconds}},
	})

	if got.ByName["one"] != firstPort || got.ByName["two"] != secondPort {
		t.Fatalf("ByName = %v", got.ByName)
	}
	if got.StructsBy["fast"].Timeout != timeoutSeconds {
		t.Fatalf("StructsBy = %v", got.StructsBy)
	}
	if got.PointersBy["slow"] == nil || got.PointersBy["slow"].Timeout != timeoutSeconds {
		t.Fatalf("PointersBy = %v", got.PointersBy)
	}
}
