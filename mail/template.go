package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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

	// Add global functions if provided
	if config.GlobalFuncs != nil {
		for key, fn := range config.GlobalFuncs {
			tm.WithTemplateFunc(key, fn)
		}
	}

	if config.WithDefaultFuncs {
		tm.WithDefaultFuncs()
	}

	// Load templates on initialization if filesystem is configured
	if config.FileSystem != nil || config.TemplatesPath != "" {
		err := tm.ReloadTemplates()
		if err != nil {
			tm.Error = apperror.Wrap(err)
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

	// Reload templates from the new filesystem
	err := tm.ReloadTemplates()
	if err != nil {
		tm.Error = apperror.Wrap(err)
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

	// Reload templates from the new path
	err := tm.ReloadTemplates()
	if err != nil {
		tm.Error = apperror.Wrap(err)
	}

	return tm
}

// WithTemplateFunc adds a template function to the manager
func (tm *TemplateManager) WithTemplateFunc(key string, fn interface{}) *TemplateManager {
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
		return nil, apperror.Wrap(err)
	}

	return tmpl, nil
}

// RenderTemplate renders a template with the given data and optional custom functions
func (tm *TemplateManager) RenderTemplate(name string, data interface{}, funcs ...template.FuncMap) (string, error) {
	if tm.Error != nil {
		return "", tm.Error
	}

	// Load template content from source
	content, err := func() ([]byte, error) {
		path := filepath.Clean(name)
		// Try reading from TemplatesPath first
		if tm.config.TemplatesPath != "" {
			customPath := filepath.Join(tm.config.TemplatesPath, path)
			_, err := os.Stat(customPath)
			if err == nil {
				content, err := os.ReadFile(filepath.Clean(customPath))
				if err != nil {
					return nil, apperror.NewError("failed to read template file from TemplatesPath").AddError(err)
				}
				return content, nil
			}
		}

		// Fallback to FileSystem if configured
		if tm.config.FileSystem != nil {
			content, err := fs.ReadFile(tm.config.FileSystem, path)
			if err != nil {
				return nil, apperror.NewError("failed to read template file from FileSystem").AddError(err)
			}
			return content, nil
		}

		// Neither TemplatesPath nor FileSystem configured
		return nil, apperror.NewError("no template source configured")
	}()
	if err != nil {
		return "", apperror.Wrap(err)
	}

	// Build function map: start with global functions, then apply custom functions
	funcMap := make(template.FuncMap)

	// Thread-safe access to global functions
	tm.mutex.RLock()
	if tm.funcs != nil {
		for key, fn := range tm.funcs {
			funcMap[key] = fn
		}
	}
	tm.mutex.RUnlock()

	// Apply custom functions (later ones override earlier ones)
	for _, customFunc := range funcs {
		for key, fn := range customFunc {
			funcMap[key] = fn
		}
	}

	// Parse and execute template
	tmpl, err := template.New(name).Funcs(funcMap).Parse(string(content))
	if err != nil {
		return "", apperror.NewError("failed to parse template").AddError(err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", apperror.NewError("failed to execute template").AddError(err)
	}

	return buf.String(), nil
}

// RenderTemplateContent renders template content directly with the given data and optional custom functions
func (tm *TemplateManager) RenderTemplateContent(content string, data interface{}, funcs ...template.FuncMap) (string, error) {
	if tm.Error != nil {
		return "", tm.Error
	}

	if content == "" {
		return "", apperror.NewError("template content cannot be empty")
	}

	// Build function map: start with global functions, then apply custom functions
	funcMap := make(template.FuncMap)

	// Thread-safe access to global functions
	tm.mutex.RLock()
	if tm.funcs != nil {
		for key, fn := range tm.funcs {
			funcMap[key] = fn
		}
	}
	tm.mutex.RUnlock()

	// Apply custom functions (later ones override earlier ones)
	for _, customFunc := range funcs {
		for key, fn := range customFunc {
			funcMap[key] = fn
		}
	}

	// Parse and execute template
	tmpl, err := template.New("inline-template").Funcs(funcMap).Parse(content)
	if err != nil {
		return "", apperror.NewError("failed to parse template content").AddError(err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", apperror.NewError("failed to execute template content").AddError(err)
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
			return apperror.Wrap(err)
		}
	}

	if tm.config.TemplatesPath != "" {
		err := tm.loadTemplatesFromPath(tm.config.TemplatesPath)
		if err != nil {
			return apperror.Wrap(err)
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
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}

		// Read template content
		content, err := fs.ReadFile(filesystem, path)
		if err != nil {
			return apperror.NewError("failed to read template file").AddError(err)
		}

		// Parse template
		tmpl, err := tm.parseTemplate(path, string(content))
		if err != nil {
			return apperror.Wrap(err)
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

	return filepath.Walk(templatesPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}

		// Get template name relative to templates path
		relPath, err := filepath.Rel(templatesPath, path)
		if err != nil {
			return err
		}

		// Load template content
		content, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return apperror.NewError("failed to read template file").AddError(err)
		}

		// Parse template
		tmpl, err := tm.parseTemplate(relPath, string(content))
		if err != nil {
			return apperror.Wrap(err)
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

	// Try to load from filesystem first
	switch {
	case tm.config.FileSystem != nil:
		content, err = fs.ReadFile(tm.config.FileSystem, name)
		if err != nil {
			return nil, apperror.NewError("template not found in filesystem").AddError(err)
		}
	case tm.config.TemplatesPath != "":
		// Try to load from custom path
		customPath := filepath.Clean(filepath.Join(tm.config.TemplatesPath, name))
		_, statErr := os.Stat(customPath)
		if statErr != nil {
			return nil, apperror.NewError("template not found in templates path").AddError(statErr)
		}

		content, err = os.ReadFile(customPath)
		if err != nil {
			return nil, apperror.NewError("failed to read template file").AddError(err)
		}
	default:
		return nil, apperror.NewError("no template source configured - use WithFS or WithFileServer")
	}

	// Parse template
	tmpl, err := tm.parseTemplate(name, string(content))
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	// Cache the template
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

	// Parse template with functions
	tmpl, err := template.New(name).Funcs(tm.funcs).Parse(content)
	if err != nil {
		return nil, apperror.NewError("failed to parse template").AddError(err)
	}

	return tmpl, nil
}

// WithDefaultFuncs adds default template functions to the manager
func (tm *TemplateManager) WithDefaultFuncs() *TemplateManager {
	if tm.Error != nil {
		return tm
	}

	// Add default functions if not already set
	if tm.funcs == nil {
		tm.funcs = make(template.FuncMap)
	}

	funcs := template.FuncMap{
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
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"title": func(s string, langCode string) string {
			lang, err := language.Parse(langCode)
			if err != nil {
				// Default to English if parsing fails
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
		"default": func(defaultValue, value interface{}) interface{} {
			if value == nil || value == "" {
				return defaultValue
			}
			return value
		},
		"dict": func(values ...interface{}) map[string]interface{} {
			if len(values)%2 != 0 {
				return nil
			}
			dict := make(map[string]interface{})
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					continue
				}
				dict[key] = values[i+1]
			}
			return dict
		},
		"now": time.Now,
		"date": func(format string, date interface{}) string {
			// Handle different date types and formats
			switch v := date.(type) {
			case time.Time:
				if format == "" {
					format = "2006-01-02 15:04:05"
				}
				return v.Format(format)
			case *time.Time:
				if v == nil {
					return ""
				}
				if format == "" {
					format = "2006-01-02 15:04:05"
				}
				return v.Format(format)
			case string:
				// Try to parse string as time
				t, parseErr := time.Parse(time.RFC3339, v)
				if parseErr == nil {
					if format == "" {
						format = "2006-01-02 15:04:05"
					}
					return t.Format(format)
				}
				return v
			default:
				return fmt.Sprintf("%v", date)
			}
		},
		"unix": func(t int64, nsec int) time.Time {
			return time.Unix(t, int64(nsec))
		},
		"unmarshal": func(object string) (interface{}, error) {
			var data map[string]interface{}
			err := json.Unmarshal([]byte(object), &data)
			return data, err
		},
		"marshal": func(v interface{}) (string, error) {
			bytes, err := json.Marshal(v)
			return string(bytes), err
		},
		"marshalIndent": func(v interface{}, prefix, indent string) (string, error) {
			bytes, err := json.MarshalIndent(v, prefix, indent)
			return string(bytes), err
		},
		"debug": func(v interface{}) template.HTML {
			bytes, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return template.HTML(fmt.Sprintf("error: %v", err))
			}
			escaped := html.EscapeString(string(bytes))
			return template.HTML(fmt.Sprintf("<pre>%s</pre>", escaped))
		},
		"print":  fmt.Sprint,
		"printf": fmt.Sprintf,
	}

	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	for key, fn := range funcs {
		if _, exists := tm.funcs[key]; !exists {
			tm.funcs[key] = fn
		}
	}
	return tm
}
