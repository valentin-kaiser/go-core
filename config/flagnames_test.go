package config_test

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/valentin-kaiser/go-core/config"
)

type flagNameServer struct {
	ARCHost string `yaml:"ARCHost" usage:"host of the arc"`
}

type flagNameConfig struct {
	ApplicationName string         `yaml:"ApplicationName" usage:"name of the application"`
	HTTPPort        int            `yaml:"HTTPPort" usage:"port of the http server"`
	Server          flagNameServer `yaml:"Server"`
}

func (c *flagNameConfig) Validate() error { return nil }

func TestFlagNamesAreKebabCase(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)

	err := config.Manager().WithName("flag-names").Register(&flagNameConfig{})
	if err != nil {
		t.Fatalf("registering configuration failed: %v", err)
	}

	for _, name := range []string{"application-name", "http-port", "server.arc-host"} {
		if pflag.Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
}
