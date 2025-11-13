package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/valentin-kaiser/go-core/apperror"

	"gopkg.in/yaml.v2"
)

func (m *manager) read() error {
	mutex.Lock()
	defer mutex.Unlock()

	if m.name == "" || m.path == "" {
		return apperror.NewError("config name and path must be set")
	}

	configFile := filepath.Join(m.path, m.name+".yaml")

	data, err := os.ReadFile(filepath.Clean(configFile))
	if err != nil {
		return apperror.NewError("reading configuration file failed").AddError(err)
	}

	var yamlData map[string]interface{}
	err = yaml.Unmarshal(data, &yamlData)
	if err != nil {
		return apperror.NewError("unmarshalling configuration file failed").AddError(err)
	}

	m.values = make(map[string]interface{})
	m.flatten(yamlData, "")
	return nil
}

// save saves the configuration to the file
// If the file does not exist, it creates a new one with the default values
func (m *manager) save(c Config) error {
	// Ensure the directory exists before trying to create the file
	err := os.MkdirAll(m.path, 0750)
	if err != nil {
		return apperror.NewError("creating configuration directory failed").AddError(err)
	}

	path, err := filepath.Abs(filepath.Join(m.path, m.name+".yaml"))
	if err != nil {
		return apperror.NewError("building absolute path of configuration file failed").AddError(err)
	}
	file, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return apperror.NewError("opening configuration file failed").AddError(err)
	}

	mutex.RLock()
	defer mutex.RUnlock()
	data, err := yaml.Marshal(c)
	if err != nil {
		return apperror.NewError("marshalling configuration data failed").AddError(err)
	}

	_, err = file.Write(data)
	if err != nil {
		return apperror.NewError("writing configuration data to file failed").AddError(err)
	}

	err = file.Close()
	if err != nil {
		return apperror.NewError("closing configuration file failed").AddError(err)
	}

	return nil
}

func (m *manager) flatten(data map[string]interface{}, prefix string) {
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		// Handle map[string]interface{}
		if nested, ok := value.(map[string]interface{}); ok {
			m.flatten(nested, fullKey)
			continue
		}

		// Handle map[interface{}]interface{} (common with YAML unmarshaling)
		if nestedInterface, ok := value.(map[interface{}]interface{}); ok {
			nestedString := make(map[string]interface{})
			for k, v := range nestedInterface {
				if keyStr, ok := k.(string); ok {
					nestedString[keyStr] = v
				}
			}
			m.flatten(nestedString, fullKey)
			continue
		}

		m.values[strings.ToLower(fullKey)] = value
	}
}
