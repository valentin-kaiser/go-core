package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"

	"gopkg.in/yaml.v2"
)

// fileSource implements Source against a YAML file on disk. It is the default
// base source used by the manager when no explicit source is configured.
type fileSource struct {
	name string
	path string
}

func newFileSource(name, path string) *fileSource {
	return &fileSource{name: name, path: path}
}

// Name implements Source.
func (s *fileSource) Name() string { return "file" }

// configFile returns the absolute path to the YAML file on disk.
func (s *fileSource) configFile() string {
	return filepath.Join(s.path, s.name+".yaml")
}

// Load implements Source. It returns ErrNotFound when the YAML file does not
// exist on disk so that the manager can bootstrap defaults.
func (s *fileSource) Load(_ context.Context) (map[string]interface{}, error) {
	if s.name == "" || s.path == "" {
		return nil, apperror.NewError("config name and path must be set")
	}

	data, err := os.ReadFile(filepath.Clean(s.configFile()))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, apperror.NewError("reading configuration file failed").AddError(err)
	}

	var yamlData map[string]interface{}
	err = yaml.Unmarshal(data, &yamlData)
	if err != nil {
		return nil, apperror.NewError("unmarshalling configuration file failed").AddError(err)
	}

	values := make(map[string]interface{})
	flattenYAML(yamlData, "", values)
	return values, nil
}

// Save implements Source. It writes the configuration to the YAML file,
// creating the directory if necessary.
func (s *fileSource) Save(_ context.Context, c Config) error {
	err := os.MkdirAll(s.path, 0750)
	if err != nil {
		return apperror.NewError("creating configuration directory failed").AddError(err)
	}

	path, err := filepath.Abs(s.configFile())
	if err != nil {
		return apperror.NewError("building absolute path of configuration file failed").AddError(err)
	}

	file, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return apperror.NewError("opening configuration file failed").AddError(err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		_ = file.Close()
		return apperror.NewError("marshalling configuration data failed").AddError(err)
	}

	_, err = file.Write(data)
	if err != nil {
		_ = file.Close()
		return apperror.NewError("writing configuration data to file failed").AddError(err)
	}

	err = file.Close()
	if err != nil {
		return apperror.NewError("closing configuration file failed").AddError(err)
	}

	return nil
}

// Watch implements Source. The file source does not currently watch the YAML
// file for changes; users that need live reload can do so via a custom Source.
func (s *fileSource) Watch(_ context.Context, _ func(map[string]interface{})) (func(), error) {
	return nil, ErrWatchUnsupported
}

// flattenYAML walks a YAML-decoded map and flattens nested maps into dotted
// lower-case keys writing them into out.
func flattenYAML(data map[string]interface{}, prefix string, out map[string]interface{}) {
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		if nested, ok := value.(map[string]interface{}); ok {
			flattenYAML(nested, fullKey, out)
			continue
		}

		if nestedInterface, ok := value.(map[interface{}]interface{}); ok {
			nestedString := make(map[string]interface{})
			for k, v := range nestedInterface {
				if keyStr, ok := k.(string); ok {
					nestedString[keyStr] = v
				}
			}
			flattenYAML(nestedString, fullKey, out)
			continue
		}

		out[strings.ToLower(fullKey)] = value
	}
}
