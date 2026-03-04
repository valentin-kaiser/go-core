// Package i18n provides a lightweight internationalization (i18n) framework
// for Go applications, supporting translation of strings into multiple languages.
//
// It supports language parsing, validation, translation lookup with fallback,
// formatted translations, and loading translations from embedded filesystems
// or raw JSON data.
//
// Consumers are responsible for supplying their own translation files. The package
// does not embed any locale data itself.
//
// Translation files must be nested JSON objects. Dot-separated lookup keys are
// derived from the nesting hierarchy:
//
//	{
//	    "user": {
//	        "created": "User created successfully",
//	        "not_found": "User not found"
//	    },
//	    "error": {
//	        "internal": "An internal error occurred."
//	    }
//	}
//
// The keys above are accessed as "user.created", "user.not_found",
// and "error.internal" respectively. Flat JSON (e.g.
// {"user.created": "..."}) is no longer supported.
//
// Example translation file (locales/en.json):
//
//	{
//	    "user": {
//	        "created": "User created successfully",
//	        "not_found": "User not found"
//	    },
//	    "error": {
//	        "internal": "An internal error occurred.",
//	        "details": "Error in %s at line %d."
//	    }
//	}
//
// Example usage:
//
//	package main
//
//	import (
//	    "embed"
//	    "fmt"
//
//	    "github.com/valentin-kaiser/go-core/i18n"
//	)
//
//	//go:embed locales/*.json
//	var localesFS embed.FS
//
//	func main() {
//	    // Load translations from an embedded filesystem
//	    bundle, err := i18n.New(i18n.WithFS(localesFS, "locales"))
//	    if err != nil {
//	        panic(err)
//	    }
//
//	    // Translate a key derived from the JSON nesting hierarchy
//	    fmt.Println(bundle.T(i18n.German, "user.created"))
//
//	    // Translate with format arguments
//	    fmt.Println(bundle.Tf(i18n.English, "error.details", "file.txt", 42))
//	}
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Language represents a BCP 47 language code.
type Language string

const (
	// English represents the English language.
	English Language = "en"
	// German represents the German language.
	German Language = "de"
	// Default is the fallback language used when a requested translation is missing.
	Default Language = English
)

// Supported returns all languages that have translations loaded in the global
// default bundle. If no translations have been loaded yet the list may be empty.
func Supported() []Language {
	return globalBundle.Load().Languages()
}

// Valid checks if a language code has translations loaded in the global bundle.
func Valid(lang string) bool {
	return globalBundle.Load().HasLanguage(Language(strings.ToLower(strings.TrimSpace(lang))))
}

// Parse normalizes a language string. If the language has no translations
// loaded in the global bundle, Default is returned.
func Parse(lang string) Language {
	l := Language(strings.ToLower(strings.TrimSpace(lang)))
	if l == "" {
		return Default
	}
	if globalBundle.Load().HasLanguage(l) {
		return l
	}
	return Default
}

// Bundle holds loaded translations for one or more languages and provides
// lookup methods for retrieving translated strings.
type Bundle struct {
	mu           sync.RWMutex
	translations map[Language]map[string]string
}

// Option configures a Bundle during creation.
type Option func(*Bundle) error

// WithFS loads translation files from an embed.FS (or any fs.FS). It scans the
// given directory for JSON files named by language code (e.g. "en.json",
// "fr.json", "ja.json") and loads each one automatically. Any language is
// supported — the language code is derived from the filename.
// Files that cannot be read or parsed are silently skipped.
func WithFS(fsys fs.FS, dir string) Option {
	return func(b *Bundle) error {
		err := loadDir(b, fsys, dir)
		if err != nil {
			return apperror.Wrap(err)
		}
		return nil
	}
}

// WithEmbedFS is an alias for WithFS that explicitly takes an embed.FS.
// It reads translation files from the given directory inside the embedded filesystem.
func WithEmbedFS(fsys embed.FS, dir string) Option {
	return WithFS(fsys, dir)
}

// WithJSON registers translations for a language from raw JSON bytes.
// The JSON must be a nested object; dot-separated lookup keys are derived from
// the nesting hierarchy (e.g. {"page": {"title": "Home"}} is accessed as
// "page.title"). If the JSON data is malformed, an error is returned when the
// bundle is created, as it indicates a programming error that should be caught
// during development.
func WithJSON(lang Language, data []byte) Option {
	return func(b *Bundle) error {
		err := b.loadJSON(lang, data)
		if err != nil {
			return apperror.NewErrorf("failed to load JSON for language %q: %v", lang, err)
		}
		return nil
	}
}

// WithMap registers translations for a language from a Go map.
func WithMap(lang Language, translations map[string]string) Option {
	return func(b *Bundle) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.translations[lang] == nil {
			b.translations[lang] = make(map[string]string)
		}
		for k, v := range translations {
			b.translations[lang][k] = v
		}
		return nil
	}
}

// New creates a new Bundle and applies the given options to load translations.
func New(opts ...Option) (*Bundle, error) {
	b := &Bundle{
		translations: make(map[Language]map[string]string),
	}
	for _, opt := range opts {
		err := opt(b)
		if err != nil {
			return nil, apperror.Wrap(err)
		}
	}
	return b, nil
}

// T translates a key for the given language. If the key is not found in the
// requested language, it falls back to Default. If still not found, the key
// itself is returned unchanged.
func (b *Bundle) T(lang Language, key string) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if msgs, ok := b.translations[lang]; ok {
		if val, ok := msgs[key]; ok {
			return val
		}
	}
	// Fallback to default language.
	if lang != Default {
		if msgs, ok := b.translations[Default]; ok {
			if val, ok := msgs[key]; ok {
				return val
			}
		}
	}
	return key
}

// Tf translates a key and formats the result with the given arguments using
// fmt.Sprintf.
func (b *Bundle) Tf(lang Language, key string, args ...any) string {
	return fmt.Sprintf(b.T(lang, key), args...)
}

// Has reports whether a translation exists for the given key and language.
func (b *Bundle) Has(lang Language, key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if msgs, ok := b.translations[lang]; ok {
		_, found := msgs[key]
		return found
	}
	return false
}

// Register adds translations for a language from a Go map at runtime.
// It merges the new translations with any existing ones for that language.
func (b *Bundle) Register(lang Language, translations map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.translations[lang] == nil {
		b.translations[lang] = make(map[string]string)
	}
	for k, v := range translations {
		b.translations[lang][k] = v
	}
}

// RegisterJSON adds translations for a language from raw JSON bytes at runtime.
// The JSON must be a nested object; keys are flattened to dot-separated strings
// internally (e.g. {"page": {"title": "Home"}} becomes "page.title").
func (b *Bundle) RegisterJSON(lang Language, data []byte) error {
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("i18n: failed to parse JSON for language %q: %w", lang, err)
	}
	m := make(map[string]string)
	flattenJSON("", raw, m)
	b.Register(lang, m)
	return nil
}

// Load loads translations from an fs.FS at runtime. It scans the directory for
// all *.json files and derives the language code from each filename.
func (b *Bundle) Load(fsys fs.FS, dir string) error {
	err := loadDir(b, fsys, dir)
	if err != nil {
		return apperror.Wrap(err)
	}
	return nil
}

// Languages returns all languages that have at least one translation loaded.
func (b *Bundle) Languages() []Language {
	b.mu.RLock()
	defer b.mu.RUnlock()

	langs := make([]Language, 0, len(b.translations))
	for lang := range b.translations {
		langs = append(langs, lang)
	}
	return langs
}

// HasLanguage reports whether the bundle contains translations for the given language.
func (b *Bundle) HasLanguage(lang Language) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.translations[lang]
	return ok
}

// loadJSON parses a nested JSON object, flattens it to dot-separated keys, and
// merges the result into the bundle for the given language.
// Returns an error if the JSON cannot be parsed.
func (b *Bundle) loadJSON(lang Language, data []byte) error {
	raw := make(map[string]any)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m := make(map[string]string)
	flattenJSON("", raw, m)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.translations[lang] == nil {
		b.translations[lang] = make(map[string]string)
	}
	for k, v := range m {
		b.translations[lang][k] = v
	}
	return nil
}

// flattenJSON recursively walks a nested map[string]any and writes dot-separated
// key paths to out. Leaf values are converted to strings via fmt.Sprint.
func flattenJSON(prefix string, raw map[string]any, out map[string]string) {
	for k, v := range raw {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flattenJSON(key, val, out)
		case string:
			out[key] = val
		default:
			out[key] = fmt.Sprint(val)
		}
	}
}

// loadDir scans a directory in an fs.FS for *.json files and loads each one
// into the bundle. The language code is derived from the filename (e.g.
// "en.json" → "en", "zh-CN.json" → "zh-cn").
func loadDir(b *Bundle, fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return apperror.NewErrorf("failed to read directory %q: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if path.Ext(name) != ".json" {
			continue
		}
		lang := Language(strings.ToLower(strings.TrimSuffix(name, ".json")))
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			continue
		}
		_ = b.loadJSON(lang, data) // Silently skip files with parse errors
	}
	return nil
}

// ---------------------------------------------------------------------------
// Global default bundle with package-level convenience functions
// ---------------------------------------------------------------------------

var globalBundle atomic.Pointer[Bundle]

func init() {
	bundle, err := New()
	if err != nil {
		panic(fmt.Sprintf("failed to initialize i18n bundle: %v", err))
	}
	globalBundle.Store(bundle)
}

// SetDefault replaces the global default bundle. It should be called early
// during application startup before any T/Tf calls are made.
// This function is safe for concurrent use.
func SetDefault(b *Bundle) {
	globalBundle.Store(b)
}

// GetDefault returns the global default bundle.
// This function is safe for concurrent use.
func GetDefault() *Bundle {
	return globalBundle.Load()
}

// Init initialises the global default bundle with the given options, replacing
// any previously loaded translations.
// This function is safe for concurrent use.
func Init(opts ...Option) error {
	bundle, err := New(opts...)
	if err != nil {
		return apperror.Wrap(err)
	}
	globalBundle.Store(bundle)
	return nil
}

// T translates a key using the global default bundle.
// This function is safe for concurrent use.
func T(lang Language, key string) string {
	return globalBundle.Load().T(lang, key)
}

// Tf translates and formats a key using the global default bundle.
// This function is safe for concurrent use.
func Tf(lang Language, key string, args ...any) string {
	return globalBundle.Load().Tf(lang, key, args...)
}
