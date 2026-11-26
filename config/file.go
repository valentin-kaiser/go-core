package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/valentin-kaiser/go-core/v2/apperror"

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
func (s *fileSource) Name() string { return fileSourceName }

// configFile returns the absolute path to the YAML file on disk.
func (s *fileSource) configFile() string {
	return filepath.Join(s.path, s.name+configFileExtension)
}

// Load implements Source. It returns ErrNotFound when the YAML file does not
// exist on disk so that the manager can bootstrap defaults.
func (s *fileSource) Load(_ context.Context) (map[string]any, error) {
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

	var yamlData map[string]any
	err = yaml.Unmarshal(data, &yamlData)
	if err != nil {
		return nil, apperror.NewError("unmarshalling configuration file failed").AddError(err)
	}

	values := make(map[string]any)
	flattenYAML(yamlData, "", values)
	return values, nil
}

// Save implements Source. It writes the configuration to the YAML file,
// creating the directory if necessary.
func (s *fileSource) Save(_ context.Context, c Config) error {
	err := os.MkdirAll(s.path, configDirMode)
	if err != nil {
		return apperror.NewError("creating configuration directory failed").AddError(err)
	}

	path, err := filepath.Abs(s.configFile())
	if err != nil {
		return apperror.NewError("building absolute path of configuration file failed").AddError(err)
	}

	file, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE|os.O_TRUNC, configFileMode)
	if err != nil {
		return apperror.NewError("opening configuration file failed").AddError(err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		apperror.Catch(file.Close, "closing configuration file failed")
		return apperror.NewError("marshalling configuration data failed").AddError(err)
	}

	_, err = file.Write(data)
	if err != nil {
		apperror.Catch(file.Close, "closing configuration file failed")
		return apperror.NewError("writing configuration data to file failed").AddError(err)
	}

	err = file.Close()
	if err != nil {
		return apperror.NewError("closing configuration file failed").AddError(err)
	}

	return nil
}

// Watch implements Source. It watches the configuration file's directory for
// filesystem events using fsnotify and reloads whenever the target file is
// written or (re)created. The directory, rather than the file itself, is
// watched so the subscription survives editors that save by renaming a
// temporary file over the original, which would otherwise drop a watch held
// on the old file handle/inode.
func (s *fileSource) Watch(ctx context.Context, onChange func(map[string]any)) (func(), error) {
	if s.name == "" || s.path == "" {
		return nil, apperror.NewError("config name and path must be set")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, apperror.NewError("creating configuration file watcher failed").AddError(err)
	}

	dir := filepath.Dir(s.configFile())
	err = watcher.Add(dir)
	if err != nil {
		apperror.Catch(watcher.Close, "closing configuration file watcher failed")
		return nil, apperror.NewErrorf("watching configuration directory %q failed", dir).AddError(err)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	go s.watchEvents(watchCtx, watcher, filepath.Clean(s.configFile()), onChange)

	return cancel, nil
}

func (s *fileSource) watchEvents(ctx context.Context, watcher *fsnotify.Watcher, target string, onChange func(map[string]any)) {
	defer apperror.Catch(watcher.Close, "closing configuration file watcher failed")

	// Debounce bursts of events from a single save (e.g. a truncate
	// followed by a write, or a rename-based atomic save that fires a
	// remove and a create back to back) into a single reload.
	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	defer debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return
			}
			apperror.Catch(func() error { return watchErr }, "watching configuration file failed")
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != target {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			debounce.Reset(reloadDebounce)
		case <-debounce.C:
			s.reload(ctx, onChange)
		}
	}
}

func (s *fileSource) reload(ctx context.Context, onChange func(map[string]any)) {
	values, err := s.Load(ctx)
	if err != nil {
		apperror.Catch(func() error { return err }, "loading changed configuration file failed")
		return
	}
	onChange(values)
}

// flattenYAML walks a YAML-decoded map and flattens nested maps into dotted
// lower-case keys writing them into out.
func flattenYAML(data map[string]any, prefix string, out map[string]any) {
	for key, value := range data {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + keySeparator + key
		}
		if nested, ok := value.(map[string]any); ok {
			flattenYAML(nested, fullKey, out)
			continue
		}
		if nestedInterface, ok := value.(map[any]any); ok {
			flattenYAML(stringKeyedMap(nestedInterface), fullKey, out)
			continue
		}
		out[strings.ToLower(fullKey)] = value
	}
}
