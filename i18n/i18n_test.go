package i18n

import (
	"testing"
	"testing/fstest"
)

func TestParse(t *testing.T) {
	// Parse depends on the global bundle, set it up first
	Init(WithMap(English, map[string]string{"k": "v"}), WithMap(German, map[string]string{"k": "v"}))
	defer Init()

	tests := []struct {
		input string
		want  Language
	}{
		{"en", English},
		{"EN", English},
		{"de", German},
		{"De", German},
		{" de ", German},
		{"fr", Default}, // not loaded
		{"", Default},
	}
	for _, tt := range tests {
		got := Parse(tt.input)
		if got != tt.want {
			t.Errorf("Parse(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValid(t *testing.T) {
	// Valid depends on the global bundle
	Init(WithMap(English, map[string]string{"k": "v"}), WithMap(German, map[string]string{"k": "v"}))
	defer Init()

	tests := []struct {
		input string
		want  bool
	}{
		{"en", true},
		{"de", true},
		{"DE", true},
		{"fr", false},
		{"", false},
	}
	for _, tt := range tests {
		got := Valid(tt.input)
		if got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSupported(t *testing.T) {
	Init(WithMap(English, map[string]string{"a": "b"}), WithMap(German, map[string]string{"a": "c"}))
	defer Init()

	langs := Supported()
	if len(langs) != 2 {
		t.Fatalf("expected 2 supported languages, got %d", len(langs))
	}
	has := map[Language]bool{}
	for _, l := range langs {
		has[l] = true
	}
	if !has[English] || !has[German] {
		t.Errorf("unexpected supported languages: %v", langs)
	}
}

func TestBundleT(t *testing.T) {
	b := New(
		WithMap(English, map[string]string{
			"hello":   "Hello",
			"goodbye": "Goodbye",
		}),
		WithMap(German, map[string]string{
			"hello": "Hallo",
		}),
	)

	// Direct lookup
	if got := b.T(English, "hello"); got != "Hello" {
		t.Errorf("T(English, hello) = %q, want %q", got, "Hello")
	}
	if got := b.T(German, "hello"); got != "Hallo" {
		t.Errorf("T(German, hello) = %q, want %q", got, "Hallo")
	}

	// Fallback to default (English) when German translation missing
	if got := b.T(German, "goodbye"); got != "Goodbye" {
		t.Errorf("T(German, goodbye) = %q, want %q (fallback)", got, "Goodbye")
	}

	// Return key when no translation exists
	if got := b.T(English, "missing.key"); got != "missing.key" {
		t.Errorf("T(English, missing.key) = %q, want %q", got, "missing.key")
	}
}

func TestBundleTf(t *testing.T) {
	b := New(WithMap(English, map[string]string{
		"greeting": "Hello, %s! You have %d messages.",
	}))

	got := b.Tf(English, "greeting", "Alice", 5)
	want := "Hello, Alice! You have 5 messages."
	if got != want {
		t.Errorf("Tf = %q, want %q", got, want)
	}
}

func TestBundleHas(t *testing.T) {
	b := New(WithMap(English, map[string]string{"key": "val"}))

	if !b.Has(English, "key") {
		t.Error("Has(English, key) = false, want true")
	}
	if b.Has(English, "missing") {
		t.Error("Has(English, missing) = true, want false")
	}
	if b.Has(German, "key") {
		t.Error("Has(German, key) = true, want false")
	}
}

func TestBundleLanguages(t *testing.T) {
	b := New(
		WithMap(English, map[string]string{"a": "b"}),
		WithMap(German, map[string]string{"a": "c"}),
	)
	langs := b.Languages()
	if len(langs) != 2 {
		t.Fatalf("expected 2 languages, got %d", len(langs))
	}
}

func TestBundleRegister(t *testing.T) {
	b := New()
	b.Register(English, map[string]string{"hello": "Hello"})

	if got := b.T(English, "hello"); got != "Hello" {
		t.Errorf("T after Register = %q, want %q", got, "Hello")
	}

	// Register additional keys (merge)
	b.Register(English, map[string]string{"world": "World"})
	if got := b.T(English, "world"); got != "World" {
		t.Errorf("T after second Register = %q, want %q", got, "World")
	}
	// Original key still present
	if got := b.T(English, "hello"); got != "Hello" {
		t.Errorf("T original key after merge = %q, want %q", got, "Hello")
	}
}

func TestBundleRegisterJSON(t *testing.T) {
	b := New()
	err := b.RegisterJSON(English, []byte(`{"hello": "Hello", "world": "World"}`))
	if err != nil {
		t.Fatalf("RegisterJSON failed: %v", err)
	}
	if got := b.T(English, "hello"); got != "Hello" {
		t.Errorf("T = %q, want %q", got, "Hello")
	}

	// Invalid JSON
	err = b.RegisterJSON(English, []byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestWithJSON(t *testing.T) {
	b := New(WithJSON(German, []byte(`{"test": "Test"}`)))
	if got := b.T(German, "test"); got != "Test" {
		t.Errorf("T = %q, want %q", got, "Test")
	}
}

func TestWithFS(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/en.json": &fstest.MapFile{Data: []byte(`{"hello": "Hello"}`)},
		"locales/de.json": &fstest.MapFile{Data: []byte(`{"hello": "Hallo"}`)},
	}

	b := New(WithFS(fsys, "locales"))
	if got := b.T(English, "hello"); got != "Hello" {
		t.Errorf("T(English) = %q, want %q", got, "Hello")
	}
	if got := b.T(German, "hello"); got != "Hallo" {
		t.Errorf("T(German) = %q, want %q", got, "Hallo")
	}
}

func TestBundleLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"i18n/en.json": &fstest.MapFile{Data: []byte(`{"foo": "bar"}`)},
	}

	b := New()
	b.Load(fsys, "i18n")

	if got := b.T(English, "foo"); got != "bar" {
		t.Errorf("T after Load = %q, want %q", got, "bar")
	}
}

func TestGlobalFunctions(t *testing.T) {
	// Set up a global bundle
	Init(WithMap(English, map[string]string{
		"global.key": "Global Value",
		"format.key": "Hello %s",
	}))

	if got := T(English, "global.key"); got != "Global Value" {
		t.Errorf("global T = %q, want %q", got, "Global Value")
	}
	if got := Tf(English, "format.key", "World"); got != "Hello World" {
		t.Errorf("global Tf = %q, want %q", got, "Hello World")
	}

	// Reset to empty to avoid polluting other tests
	Init()
}

func TestSetDefault(t *testing.T) {
	b := New(WithMap(German, map[string]string{"x": "y"}))
	SetDefault(b)

	if got := T(German, "x"); got != "y" {
		t.Errorf("T after SetDefault = %q, want %q", got, "y")
	}

	// GetDefault returns the same bundle
	if GetDefault() != b {
		t.Error("GetDefault did not return the bundle set by SetDefault")
	}

	// Reset
	Init()
}

// ---------------------------------------------------------------------------
// Tests for arbitrary / dynamic language support
// ---------------------------------------------------------------------------

func TestArbitraryLanguage(t *testing.T) {
	b := New(
		WithMap("fr", map[string]string{"hello": "Bonjour"}),
		WithMap("ja", map[string]string{"hello": "こんにちは"}),
		WithMap(English, map[string]string{"hello": "Hello"}),
	)

	if got := b.T("fr", "hello"); got != "Bonjour" {
		t.Errorf("T(fr) = %q, want %q", got, "Bonjour")
	}
	if got := b.T("ja", "hello"); got != "こんにちは" {
		t.Errorf("T(ja) = %q, want %q", got, "こんにちは")
	}
	if !b.HasLanguage("fr") {
		t.Error("HasLanguage(fr) = false, want true")
	}
	if b.HasLanguage("es") {
		t.Error("HasLanguage(es) = true, want false")
	}
}

func TestParseArbitraryLanguage(t *testing.T) {
	Init(
		WithMap(English, map[string]string{"k": "v"}),
		WithMap("fr", map[string]string{"k": "v"}),
		WithMap("zh-cn", map[string]string{"k": "v"}),
	)
	defer Init()

	if got := Parse("fr"); got != "fr" {
		t.Errorf("Parse(fr) = %q, want %q", got, "fr")
	}
	if got := Parse("FR"); got != "fr" {
		t.Errorf("Parse(FR) = %q, want %q", got, "fr")
	}
	if got := Parse("zh-CN"); got != "zh-cn" {
		t.Errorf("Parse(zh-CN) = %q, want %q", got, "zh-cn")
	}
	if got := Parse("unknown"); got != Default {
		t.Errorf("Parse(unknown) = %q, want %q", got, Default)
	}
}

func TestWithFSAutoDiscovery(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/en.json":    &fstest.MapFile{Data: []byte(`{"hello": "Hello"}`)},
		"locales/de.json":    &fstest.MapFile{Data: []byte(`{"hello": "Hallo"}`)},
		"locales/fr.json":    &fstest.MapFile{Data: []byte(`{"hello": "Bonjour"}`)},
		"locales/ja.json":    &fstest.MapFile{Data: []byte(`{"hello": "こんにちは"}`)},
		"locales/zh-cn.json": &fstest.MapFile{Data: []byte(`{"hello": "你好"}`)},
		"locales/readme.txt": &fstest.MapFile{Data: []byte(`not a json locale`)},
	}

	b := New(WithFS(fsys, "locales"))

	expected := map[Language]string{
		"en":    "Hello",
		"de":    "Hallo",
		"fr":    "Bonjour",
		"ja":    "こんにちは",
		"zh-cn": "你好",
	}
	for lang, want := range expected {
		if got := b.T(lang, "hello"); got != want {
			t.Errorf("T(%s) = %q, want %q", lang, got, want)
		}
	}

	// Should have exactly 5 languages (readme.txt ignored)
	if got := len(b.Languages()); got != 5 {
		t.Errorf("len(Languages()) = %d, want 5", got)
	}
}
