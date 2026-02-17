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
// Translation files should be flat JSON objects with dot-separated keys:
//
//	{
//	    "user.created": "User created successfully",
//	    "user.not_found": "User not found",
//	    "error.internal": "An internal error occurred."
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
//	    bundle := i18n.New(i18n.WithFS(localesFS, "locales"))
//
//	    // Translate a key
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

// Supported returns all built-in supported language codes.
func Supported() []Language {
	return []Language{English, German}
}

// Valid checks if a language code maps to a built-in supported language.
func Valid(lang string) bool {
	switch Language(strings.ToLower(lang)) {
	case English, German:
		return true
	}
	return false
}

// Parse normalizes and validates a language string. If the input does not match
// a known language code, Default is returned.
func Parse(lang string) Language {
	l := Language(strings.ToLower(strings.TrimSpace(lang)))
	switch l {
	case English, German:
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
type Option func(*Bundle)

// WithFS loads translation files from an embed.FS (or any fs.FS). It expects
// flat JSON files named by language code (e.g. "en.json", "de.json") inside the
// given directory. Files that cannot be read or parsed are silently skipped.
func WithFS(fsys fs.FS, dir string) Option {
	return func(b *Bundle) {
		for _, lang := range Supported() {
			p := path.Join(dir, fmt.Sprintf("%s.json", lang))
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				continue
			}
			b.loadJSON(lang, data)
		}
	}
}

// WithEmbedFS is an alias for WithFS that explicitly takes an embed.FS.
// It reads translation files from the given directory inside the embedded filesystem.
func WithEmbedFS(fsys embed.FS, dir string) Option {
	return WithFS(fsys, dir)
}

// WithJSON registers translations for a language from raw JSON bytes.
// The JSON must be a flat object mapping string keys to string values.
func WithJSON(lang Language, data []byte) Option {
	return func(b *Bundle) {
		b.loadJSON(lang, data)
	}
}

// WithMap registers translations for a language from a Go map.
func WithMap(lang Language, translations map[string]string) Option {
	return func(b *Bundle) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.translations[lang] == nil {
			b.translations[lang] = make(map[string]string)
		}
		for k, v := range translations {
			b.translations[lang][k] = v
		}
	}
}

// New creates a new Bundle and applies the given options to load translations.
func New(opts ...Option) *Bundle {
	b := &Bundle{
		translations: make(map[Language]map[string]string),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
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
func (b *Bundle) RegisterJSON(lang Language, data []byte) error {
	m := make(map[string]string)
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("i18n: failed to parse JSON for language %q: %w", lang, err)
	}
	b.Register(lang, m)
	return nil
}

// Load loads translations from an fs.FS at runtime, looking for files named
// by language code (e.g. "en.json", "de.json") inside the given directory.
func (b *Bundle) Load(fsys fs.FS, dir string) {
	for _, lang := range Supported() {
		p := path.Join(dir, fmt.Sprintf("%s.json", lang))
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		b.loadJSON(lang, data)
	}
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

// loadJSON parses a flat JSON object and merges it into the bundle for the given language.
func (b *Bundle) loadJSON(lang Language, data []byte) {
	m := make(map[string]string)
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.translations[lang] == nil {
		b.translations[lang] = make(map[string]string)
	}
	for k, v := range m {
		b.translations[lang][k] = v
	}
}

// ---------------------------------------------------------------------------
// Global default bundle with package-level convenience functions
// ---------------------------------------------------------------------------

var global = New()

// SetDefault replaces the global default bundle. It should be called early
// during application startup before any T/Tf calls are made.
func SetDefault(b *Bundle) {
	global = b
}

// GetDefault returns the global default bundle.
func GetDefault() *Bundle {
	return global
}

// Init initialises the global default bundle with the given options, replacing
// any previously loaded translations.
func Init(opts ...Option) {
	global = New(opts...)
}

// T translates a key using the global default bundle.
func T(lang Language, key string) string {
	return global.T(lang, key)
}

// Tf translates and formats a key using the global default bundle.
func Tf(lang Language, key string, args ...any) string {
	return global.Tf(lang, key, args...)
}
