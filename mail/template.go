package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Template function constants.
const (
	// defaultDateFormat is the layout used by the date function when none is given.
	defaultDateFormat = "2006-01-02 15:04:05"
	// dictPairSize is the number of values forming one key/value pair in dict.
	dictPairSize = 2
)

// TemplateManager implements the TemplateManager interface
type TemplateManager struct {
	config    TemplateConfig
	templates map[string]*template.Template
	funcs     template.FuncMap
	mutex     sync.RWMutex
	Error     error
}

// NewTemplateManager creates a new template manager
func NewTemplateManager(config TemplateConfig) *TemplateManager {
	tm := &TemplateManager{
		config:    config,
		templates: make(map[string]*template.Template),
	}

	if config.GlobalFuncs != nil {
		for key, fn := range config.GlobalFuncs {
			tm.WithTemplateFunc(key, fn)
		}
	}

	if config.WithDefaultFuncs {
		tm.WithDefaultFuncs()
	}

	if config.FileSystem != nil || config.TemplatesPath != "" {
		err := tm.ReloadTemplates()
		if err != nil {
			tm.Error = apperror.NewError("reloading templates failed").AddError(err)
		}
	}

	return tm
}

// WithFS configures the template manager to load templates from a filesystem
func (tm *TemplateManager) WithFS(filesystem fs.FS) *TemplateManager {
	if tm.Error != nil {
		return tm
	}

	tm.config.FileSystem = filesystem
	tm.config.TemplatesPath = ""

	err := tm.ReloadTemplates()
	if err != nil {
		tm.Error = apperror.NewError("reloading templates failed").AddError(err)
	}

	return tm
}

// WithFileServer configures the template manager to load templates from a file path
func (tm *TemplateManager) WithFileServer(templatesPath string) *TemplateManager {
	if tm.Error != nil {
		return tm
	}

	if templatesPath != "" {
		_, err := os.Stat(templatesPath)
		if os.IsNotExist(err) {
			tm.Error = apperror.NewError("templates path does not exist").AddDetail("path", templatesPath).AddError(err)
			return tm
		}
	}

	tm.config.TemplatesPath = templatesPath
	tm.config.FileSystem = nil

	err := tm.ReloadTemplates()
	if err != nil {
		tm.Error = apperror.NewError("reloading templates failed").AddError(err)
	}

	return tm
}

// WithTemplateFunc adds a template function to the manager
func (tm *TemplateManager) WithTemplateFunc(key string, fn any) *TemplateManager {
	if tm.Error != nil {
		return tm
	}

	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	if tm.funcs == nil {
		tm.funcs = make(template.FuncMap)
	}

	_, exists := tm.funcs[key]
	if exists {
		tm.Error = apperror.NewError("template function already exists").AddDetail("key", key)
		return tm
	}

	tm.funcs[key] = fn
	return tm
}

// LoadTemplate loads a template by name
func (tm *TemplateManager) LoadTemplate(name string) (*template.Template, error) {
	if tm.Error != nil {
		return nil, tm.Error
	}

	tm.mutex.RLock()
	tmpl, exists := tm.templates[name]
	tm.mutex.RUnlock()

	if exists {
		return tmpl, nil
	}

	tmpl, err := tm.loadTemplateFromDisk(name)
	if err != nil {
		return nil, apperror.NewErrorf("loading template %s from disk failed", name).AddError(err)
	}

	return tmpl, nil
}

// RenderTemplate renders a template with the given data and optional custom functions
func (tm *TemplateManager) RenderTemplate(name string, data any, funcs ...template.FuncMap) (string, error) {
	if tm.Error != nil {
		return "", tm.Error
	}

	content, err := tm.readTemplateSource(name)
	if err != nil {
		return "", apperror.NewError("reading template source failed").AddError(err)
	}

	tmpl, err := template.New(name).Funcs(tm.mergedFuncs(funcs)).Parse(string(content))
	if err != nil {
		return "", apperror.NewError("parsing template failed").AddError(err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", apperror.NewError("executing template failed").AddError(err)
	}

	return buf.String(), nil
}

// readTemplateSource reads the template from TemplatesPath and falls back to the FileSystem
func (tm *TemplateManager) readTemplateSource(name string) ([]byte, error) {
	if tm.config.TemplatesPath != "" {
		content, found, err := tm.readTemplateFromPath(name)
		if err != nil || found {
			return content, err
		}
	}

	if tm.config.FileSystem != nil {
		return tm.readTemplateFromFileSystem(name)
	}

	return nil, apperror.NewError("no template source configured")
}

// readTemplateFromPath reads a template below TemplatesPath and reports whether the file exists
func (tm *TemplateManager) readTemplateFromPath(name string) ([]byte, bool, error) {
	// Normalize the name to a clean relative OS path and reject traversal attempts.
	cleanName := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || cleanName == ".." {
		return nil, false, apperror.NewError("invalid template name: path traversal detected").AddDetail("name", name)
	}

	// Build and clean a single path used for both Stat and ReadFile.
	customPath := filepath.Join(tm.config.TemplatesPath, cleanName)

	// Verify the resolved path is within TemplatesPath.
	root, err := filepath.Abs(tm.config.TemplatesPath)
	if err != nil {
		return nil, false, apperror.NewError("resolving templates root path failed").AddError(err)
	}
	absCustom, err := filepath.Abs(customPath)
	if err != nil {
		return nil, false, apperror.NewError("resolving template path failed").AddError(err)
	}
	if !strings.HasPrefix(absCustom, root+string(filepath.Separator)) && absCustom != root {
		return nil, false, apperror.NewError("invalid template name: path escapes templates root").AddDetail("name", name)
	}

	_, err = os.Stat(absCustom)
	if err != nil {
		return nil, false, nil
	}

	content, err := os.ReadFile(absCustom) //nolint:gosec // absCustom was verified to be inside TemplatesPath above
	if err != nil {
		return nil, false, apperror.NewError("reading template file from TemplatesPath failed").AddError(err)
	}
	return content, true, nil
}

// readTemplateFromFileSystem reads a template from the configured FileSystem, which requires valid, relative, forward-slash paths
func (tm *TemplateManager) readTemplateFromFileSystem(name string) ([]byte, error) {
	fsPath := path.Clean(filepath.ToSlash(name))
	if !fs.ValidPath(fsPath) {
		return nil, apperror.NewError("invalid template name: not a valid fs.FS path").AddDetail("name", name)
	}

	content, err := fs.ReadFile(tm.config.FileSystem, fsPath)
	if err != nil {
		return nil, apperror.NewError("reading template file from FileSystem failed").AddError(err)
	}
	return content, nil
}

// mergedFuncs combines the global functions with the custom functions, later ones override earlier ones
func (tm *TemplateManager) mergedFuncs(funcs []template.FuncMap) template.FuncMap {
	funcMap := make(template.FuncMap)

	tm.mutex.RLock()
	maps.Copy(funcMap, tm.funcs)
	tm.mutex.RUnlock()

	for _, customFunc := range funcs {
		maps.Copy(funcMap, customFunc)
	}
	return funcMap
}

// RenderTemplateContent renders template content directly with the given data and optional custom functions
func (tm *TemplateManager) RenderTemplateContent(content string, data any, funcs ...template.FuncMap) (string, error) {
	if tm.Error != nil {
		return "", tm.Error
	}

	if content == "" {
		return "", apperror.NewError("template content cannot be empty")
	}

	tmpl, err := template.New("inline-template").Funcs(tm.mergedFuncs(funcs)).Parse(content)
	if err != nil {
		return "", apperror.NewError("parsing template content failed").AddError(err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", apperror.NewError("executing template content failed").AddError(err)
	}

	return buf.String(), nil
}

// ReloadTemplates reloads all templates
func (tm *TemplateManager) ReloadTemplates() error {
	if tm.Error != nil {
		return tm.Error
	}

	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	tm.templates = make(map[string]*template.Template)
	if tm.config.FileSystem != nil {
		err := tm.loadTemplatesFromFS(tm.config.FileSystem)
		if err != nil {
			return apperror.NewError("loading templates from filesystem failed").AddError(err)
		}
	}

	if tm.config.TemplatesPath != "" {
		err := tm.loadTemplatesFromPath(tm.config.TemplatesPath)
		if err != nil {
			return apperror.NewError("loading templates from path failed").AddError(err)
		}
	}

	return nil
}

// loadTemplatesFromFS loads templates from a filesystem
func (tm *TemplateManager) loadTemplatesFromFS(filesystem fs.FS) error {
	if tm.Error != nil {
		return tm.Error
	}

	return fs.WalkDir(filesystem, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return apperror.NewError("walking templates filesystem failed").AddError(err)
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		content, err := fs.ReadFile(filesystem, path)
		if err != nil {
			return apperror.NewError("reading template file failed").AddError(err)
		}
		tmpl, err := tm.parseTemplate(path, string(content))
		if err != nil {
			return apperror.NewErrorf("parsing template %s failed", path).AddError(err)
		}
		logger.Trace().Field("template", path).Msg("loaded template from filesystem")
		tm.templates[path] = tmpl
		return nil
	})
}

// loadTemplatesFromPath loads templates from the specified file path
func (tm *TemplateManager) loadTemplatesFromPath(templatesPath string) error {
	if tm.Error != nil {
		return tm.Error
	}

	_, err := os.Stat(templatesPath)
	if os.IsNotExist(err) {
		return apperror.NewError("templates path does not exist").AddError(err)
	}

	root, err := os.OpenRoot(templatesPath)
	if err != nil {
		return apperror.NewError("opening templates path failed").AddError(err)
	}
	defer apperror.Catch(root.Close, "closing templates path failed")

	return filepath.Walk(templatesPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return apperror.NewError("walking templates path failed").AddError(err)
		}
		if info.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		relPath, err := filepath.Rel(templatesPath, path)
		if err != nil {
			return apperror.NewErrorf("resolving relative path of %s failed", path).AddError(err)
		}
		// Reading through the root keeps symlinks from leading out of the templates path.
		content, err := root.ReadFile(relPath)
		if err != nil {
			return apperror.NewError("reading template file failed").AddError(err)
		}
		tmpl, err := tm.parseTemplate(relPath, string(content))
		if err != nil {
			return apperror.NewErrorf("parsing template %s failed", relPath).AddError(err)
		}
		logger.Trace().Field("template", relPath).Msg("loaded template from path")
		tm.templates[relPath] = tmpl
		return nil
	})
}

// loadTemplateFromDisk loads a single template from the configured source
func (tm *TemplateManager) loadTemplateFromDisk(name string) (*template.Template, error) {
	if tm.Error != nil {
		return nil, tm.Error
	}

	var content []byte
	var err error

	switch {
	case tm.config.FileSystem != nil:
		content, err = fs.ReadFile(tm.config.FileSystem, name)
		if err != nil {
			return nil, apperror.NewError("template not found in filesystem").AddError(err)
		}
	case tm.config.TemplatesPath != "":
		customPath := filepath.Clean(filepath.Join(tm.config.TemplatesPath, name))
		_, statErr := os.Stat(customPath)
		if statErr != nil {
			return nil, apperror.NewError("template not found in templates path").AddError(statErr)
		}
		content, err = os.ReadFile(customPath)
		if err != nil {
			return nil, apperror.NewError("reading template file failed").AddError(err)
		}
	default:
		return nil, apperror.NewError("no template source configured - use WithFS or WithFileServer")
	}

	tmpl, err := tm.parseTemplate(name, string(content))
	if err != nil {
		return nil, apperror.NewErrorf("parsing template %s failed", name).AddError(err)
	}

	tm.mutex.Lock()
	tm.templates[name] = tmpl
	tm.mutex.Unlock()

	return tmpl, nil
}

// parseTemplate parses a template with common functions
func (tm *TemplateManager) parseTemplate(name, content string) (*template.Template, error) {
	if tm.funcs == nil {
		tm.funcs = make(template.FuncMap)
	}

	tmpl, err := template.New(name).Funcs(tm.funcs).Parse(content)
	if err != nil {
		return nil, apperror.NewError("parsing template failed").AddError(err)
	}

	return tmpl, nil
}

// WithDefaultFuncs adds default template functions to the manager
func (tm *TemplateManager) WithDefaultFuncs() *TemplateManager {
	if tm.Error != nil {
		return tm
	}

	if tm.funcs == nil {
		tm.funcs = make(template.FuncMap)
	}

	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	for key, fn := range DefaultFuncs() {
		if _, exists := tm.funcs[key]; !exists {
			tm.funcs[key] = fn
		}
	}
	return tm
}

// DefaultFuncs returns the template functions that are available to every template.
func DefaultFuncs() template.FuncMap {
	return template.FuncMap{
		// Math functions
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"mul": func(a, b int) int {
			return a * b
		},
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"mod": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a % b
		},
		// String functions
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": func(s string, langCode string) string {
			lang, err := language.Parse(langCode)
			if err != nil {
				lang = language.English
			}
			return cases.Title(lang, cases.NoLower).String(s)
		},
		"trim":      strings.TrimSpace,
		"replace":   strings.ReplaceAll,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"join":      strings.Join,
		"split":     strings.Split,
		// Utility functions
		"default": func(defaultValue, value any) any {
			if value == nil || value == "" {
				return defaultValue
			}
			return value
		},
		"dict": func(values ...any) map[string]any {
			if len(values)%dictPairSize != 0 {
				return nil
			}
			dict := make(map[string]any)
			for i := 0; i < len(values); i += dictPairSize {
				key, ok := values[i].(string)
				if !ok {
					continue
				}
				dict[key] = values[i+1]
			}
			return dict
		},
		// Time functions
		"now":  time.Now,
		"date": formatDate,
		"unix": func(t int64, nsec int) time.Time {
			return time.Unix(t, int64(nsec))
		},
		// JSON functions
		"unmarshal": func(object string) (any, error) {
			var data map[string]any
			err := json.Unmarshal([]byte(object), &data)
			return data, err
		},
		"marshal": func(v any) (string, error) {
			bytes, err := json.Marshal(v)
			return string(bytes), err
		},
		"marshalIndent": func(v any, prefix, indent string) (string, error) {
			bytes, err := json.MarshalIndent(v, prefix, indent)
			return string(bytes), err
		},
		// Debugging functions
		"debug": func(v any) template.HTML {
			bytes, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return template.HTML(html.EscapeString(fmt.Sprintf("error: %v", err))) //nolint:gosec // the text is escaped
			}
			escaped := html.EscapeString(string(bytes))
			return template.HTML(fmt.Sprintf("<pre>%s</pre>", escaped)) //nolint:gosec // escaped was passed through html.EscapeString
		},
		"print":  fmt.Sprint,
		"printf": fmt.Sprintf,
	}
}

// formatDate formats a time, a time pointer or an RFC 3339 string and returns every other value as text.
func formatDate(format string, date any) string {
	if format == "" {
		format = defaultDateFormat
	}

	switch v := date.(type) {
	case time.Time:
		return v.Format(format)
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.Format(format)
	case string:
		t, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return v
		}
		return t.Format(format)
	default:
		return fmt.Sprintf("%v", date)
	}
}
