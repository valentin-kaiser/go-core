package i18n_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/valentin-kaiser/go-core/i18n"
)

func TestParse(t *testing.T) {
	// Parse depends on the global bundle, set it up first
	if err := i18n.Init(i18n.WithMap(i18n.English, map[string]string{"k": "v"}), i18n.WithMap(i18n.German, map[string]string{"k": "v"})); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

	tests := []struct {
		input string
		want  i18n.Language
	}{
		{"en", i18n.English},
		{"EN", i18n.English},
		{"de", i18n.German},
		{"De", i18n.German},
		{" de ", i18n.German},
		{"fr", i18n.Default}, // not loaded
		{"", i18n.Default},
	}
	for _, tt := range tests {
		got := i18n.Parse(tt.input)
		if got != tt.want {
			t.Errorf("Parse(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValid(t *testing.T) {
	// Valid depends on the global bundle
	if err := i18n.Init(i18n.WithMap(i18n.English, map[string]string{"k": "v"}), i18n.WithMap(i18n.German, map[string]string{"k": "v"})); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

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
		got := i18n.Valid(tt.input)
		if got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSupported(t *testing.T) {
	if err := i18n.Init(i18n.WithMap(i18n.English, map[string]string{"a": "b"}), i18n.WithMap(i18n.German, map[string]string{"a": "c"})); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

	langs := i18n.Supported()
	if len(langs) != 2 {
		t.Fatalf("expected 2 supported languages, got %d", len(langs))
	}
	has := map[i18n.Language]bool{}
	for _, l := range langs {
		has[l] = true
	}
	if !has[i18n.English] || !has[i18n.German] {
		t.Errorf("unexpected supported languages: %v", langs)
	}
}

func TestBundleT(t *testing.T) {
	b, err := i18n.New(
		i18n.WithMap(i18n.English, map[string]string{
			"hello":   "Hello",
			"goodbye": "Goodbye",
		}),
		i18n.WithMap(i18n.German, map[string]string{
			"hello": "Hallo",
		}),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Direct lookup
	if got := b.T(i18n.English, "hello"); got != "Hello" {
		t.Errorf("T(English, hello) = %q, want %q", got, "Hello")
	}
	if got := b.T(i18n.German, "hello"); got != "Hallo" {
		t.Errorf("T(German, hello) = %q, want %q", got, "Hallo")
	}

	// Fallback to default (English) when German translation missing
	if got := b.T(i18n.German, "goodbye"); got != "Goodbye" {
		t.Errorf("T(German, goodbye) = %q, want %q (fallback)", got, "Goodbye")
	}

	// Return key when no translation exists
	if got := b.T(i18n.English, "missing.key"); got != "missing.key" {
		t.Errorf("T(English, missing.key) = %q, want %q", got, "missing.key")
	}
}

func TestBundleTf(t *testing.T) {
	b, err := i18n.New(i18n.WithMap(i18n.English, map[string]string{
		"greeting": "Hello, %s! You have %d messages.",
	}))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	got := b.Tf(i18n.English, "greeting", "Alice", 5)
	want := "Hello, Alice! You have 5 messages."
	if got != want {
		t.Errorf("Tf = %q, want %q", got, want)
	}
}

func TestBundleHas(t *testing.T) {
	b, err := i18n.New(i18n.WithMap(i18n.English, map[string]string{"key": "val"}))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if !b.Has(i18n.English, "key") {
		t.Error("Has(English, key) = false, want true")
	}
	if b.Has(i18n.English, "missing") {
		t.Error("Has(English, missing) = true, want false")
	}
	if b.Has(i18n.German, "key") {
		t.Error("Has(German, key) = true, want false")
	}
}

func TestBundleLanguages(t *testing.T) {
	b, err := i18n.New(
		i18n.WithMap(i18n.English, map[string]string{"a": "b"}),
		i18n.WithMap(i18n.German, map[string]string{"a": "c"}),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	langs := b.Languages()
	if len(langs) != 2 {
		t.Fatalf("expected 2 languages, got %d", len(langs))
	}
}

func TestBundleRegister(t *testing.T) {
	b, err := i18n.New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	b.Register(i18n.English, map[string]string{"hello": "Hello"})

	if got := b.T(i18n.English, "hello"); got != "Hello" {
		t.Errorf("T after Register = %q, want %q", got, "Hello")
	}

	// Register additional keys (merge)
	b.Register(i18n.English, map[string]string{"world": "World"})
	if got := b.T(i18n.English, "world"); got != "World" {
		t.Errorf("T after second Register = %q, want %q", got, "World")
	}
	// Original key still present
	if got := b.T(i18n.English, "hello"); got != "Hello" {
		t.Errorf("T original key after merge = %q, want %q", got, "Hello")
	}
}

func TestBundleRegisterJSON(t *testing.T) {
	b, err := i18n.New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	err = b.RegisterJSON(i18n.English, []byte(`{"greetings": {"hello": "Hello", "world": "World"}}`))
	if err != nil {
		t.Fatalf("RegisterJSON failed: %v", err)
	}
	if got := b.T(i18n.English, "greetings.hello"); got != "Hello" {
		t.Errorf("T = %q, want %q", got, "Hello")
	}
	if got := b.T(i18n.English, "greetings.world"); got != "World" {
		t.Errorf("T = %q, want %q", got, "World")
	}

	// Invalid JSON
	err = b.RegisterJSON(i18n.English, []byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestWithJSON(t *testing.T) {
	b, err := i18n.New(i18n.WithJSON(i18n.German, []byte(`{"page": {"test": "Test"}}`)))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if got := b.T(i18n.German, "page.test"); got != "Test" {
		t.Errorf("T = %q, want %q", got, "Test")
	}
}

func TestWithJSONReturnsErrorOnMalformedJSON(t *testing.T) {
	_, err := i18n.New(i18n.WithJSON(i18n.English, []byte(`{invalid json}`)))
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestWithFS(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/en.json": &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "Hello"}}`)},
		"locales/de.json": &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "Hallo"}}`)},
	}

	b, err := i18n.New(i18n.WithFS(fsys, "locales"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if got := b.T(i18n.English, "greeting.hello"); got != "Hello" {
		t.Errorf("T(English) = %q, want %q", got, "Hello")
	}
	if got := b.T(i18n.German, "greeting.hello"); got != "Hallo" {
		t.Errorf("T(German) = %q, want %q", got, "Hallo")
	}
}

func TestWithFSSkipsMalformedJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/en.json":      &fstest.MapFile{Data: []byte(`{"status": {"valid": "Valid"}}`)},
		"locales/invalid.json": &fstest.MapFile{Data: []byte(`{malformed json}`)},
		"locales/de.json":      &fstest.MapFile{Data: []byte(`{"status": {"valid": "Gültig"}}`)},
	}

	// Should not error; should silently skip invalid.json
	b, err := i18n.New(i18n.WithFS(fsys, "locales"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if got := b.T(i18n.English, "status.valid"); got != "Valid" {
		t.Errorf("T(English) = %q, want %q", got, "Valid")
	}
	if got := b.T(i18n.German, "status.valid"); got != "Gültig" {
		t.Errorf("T(German) = %q, want %q", got, "Gültig")
	}

	// Invalid language should not be loaded
	langs := b.Languages()
	for _, lang := range langs {
		if lang == "invalid" {
			t.Error("Malformed JSON file should not have been loaded")
		}
	}
}

func TestBundleLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"i18n/en.json": &fstest.MapFile{Data: []byte(`{"item": {"foo": "bar"}}`)},
	}

	b, err := i18n.New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := b.Load(fsys, "i18n"); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got := b.T(i18n.English, "item.foo"); got != "bar" {
		t.Errorf("T after Load = %q, want %q", got, "bar")
	}
}

func TestGlobalFunctions(t *testing.T) {
	// Set up a global bundle
	if err := i18n.Init(i18n.WithMap(i18n.English, map[string]string{
		"global.key": "Global Value",
		"format.key": "Hello %s",
	})); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if got := i18n.T(i18n.English, "global.key"); got != "Global Value" {
		t.Errorf("global T = %q, want %q", got, "Global Value")
	}
	if got := i18n.Tf(i18n.English, "format.key", "World"); got != "Hello World" {
		t.Errorf("global Tf = %q, want %q", got, "Hello World")
	}

	// Reset to empty to avoid polluting other tests
	i18n.Init()
}

func TestSetDefault(t *testing.T) {
	b, err := i18n.New(i18n.WithMap(i18n.German, map[string]string{"x": "y"}))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	i18n.SetDefault(b)

	if got := i18n.T(i18n.German, "x"); got != "y" {
		t.Errorf("T after SetDefault = %q, want %q", got, "y")
	}

	// GetDefault returns the same bundle
	if i18n.GetDefault() != b {
		t.Error("GetDefault did not return the bundle set by SetDefault")
	}

	// Reset
	i18n.Init()
}

// ---------------------------------------------------------------------------
// Tests for arbitrary / dynamic language support
// ---------------------------------------------------------------------------

func TestArbitraryLanguage(t *testing.T) {
	b, err := i18n.New(
		i18n.WithMap("fr", map[string]string{"hello": "Bonjour"}),
		i18n.WithMap("ja", map[string]string{"hello": "こんにちは"}),
		i18n.WithMap(i18n.English, map[string]string{"hello": "Hello"}),
	)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

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
	if err := i18n.Init(
		i18n.WithMap(i18n.English, map[string]string{"k": "v"}),
		i18n.WithMap("fr", map[string]string{"k": "v"}),
		i18n.WithMap("zh-cn", map[string]string{"k": "v"}),
	); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

	if got := i18n.Parse("fr"); got != "fr" {
		t.Errorf("Parse(fr) = %q, want %q", got, "fr")
	}
	if got := i18n.Parse("FR"); got != "fr" {
		t.Errorf("Parse(FR) = %q, want %q", got, "fr")
	}
	if got := i18n.Parse("zh-CN"); got != "zh-cn" {
		t.Errorf("Parse(zh-CN) = %q, want %q", got, "zh-cn")
	}
	if got := i18n.Parse("unknown"); got != i18n.Default {
		t.Errorf("Parse(unknown) = %q, want %q", got, i18n.Default)
	}
}

func TestWithFSAutoDiscovery(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/en.json":    &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "Hello"}}`)},
		"locales/de.json":    &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "Hallo"}}`)},
		"locales/fr.json":    &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "Bonjour"}}`)},
		"locales/ja.json":    &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "こんにちは"}}`)},
		"locales/zh-cn.json": &fstest.MapFile{Data: []byte(`{"greeting": {"hello": "你好"}}`)},
		"locales/readme.txt": &fstest.MapFile{Data: []byte(`not a json locale`)},
	}

	b, err := i18n.New(i18n.WithFS(fsys, "locales"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	expected := map[i18n.Language]string{
		"en":    "Hello",
		"de":    "Hallo",
		"fr":    "Bonjour",
		"ja":    "こんにちは",
		"zh-cn": "你好",
	}
	for lang, want := range expected {
		if got := b.T(lang, "greeting.hello"); got != want {
			t.Errorf("T(%s) = %q, want %q", lang, got, want)
		}
	}

	// Should have exactly 5 languages (readme.txt ignored)
	if got := len(b.Languages()); got != 5 {
		t.Errorf("len(Languages()) = %d, want 5", got)
	}
}

func TestNestedJSON(t *testing.T) {
	// Verify multi-level nesting resolves to dot-separated keys.
	b, err := i18n.New(i18n.WithJSON(i18n.English, []byte(`{
		"page": {
			"login": {
				"title": "Login",
				"subtitle": "Welcome back"
			},
			"register": {
				"title": "Register"
			}
		},
		"error": {
			"internal": "An internal error occurred."
		}
	}`)))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	cases := []struct {
		key  string
		want string
	}{
		{"page.login.title", "Login"},
		{"page.login.subtitle", "Welcome back"},
		{"page.register.title", "Register"},
		{"error.internal", "An internal error occurred."},
	}
	for _, tc := range cases {
		if got := b.T(i18n.English, tc.key); got != tc.want {
			t.Errorf("T(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests for duplicate flattened key detection
// ---------------------------------------------------------------------------

func TestWithJSON_DuplicateFlattenedKey(t *testing.T) {
	// Both nested "a.b" and explicit "a.b" exist – must produce an error.
	data := []byte(`{"a":{"b":"nested"},"a.b":"flat"}`)
	_, err := i18n.New(i18n.WithJSON(i18n.English, data))
	if err == nil {
		t.Fatal("expected error for duplicate flattened key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate flattened key") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRegisterJSON_DuplicateFlattenedKey(t *testing.T) {
	b, err := i18n.New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	data := []byte(`{"x":{"y":"nested"},"x.y":"flat"}`)
	err = b.RegisterJSON(i18n.English, data)
	if err == nil {
		t.Fatal("expected error for duplicate flattened key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate flattened key") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestWithFS_DuplicateFlattenedKeySkipsFile(t *testing.T) {
	// A file with colliding keys should be skipped, like malformed JSON.
	fsys := fstest.MapFS{
		"locales/en.json": &fstest.MapFile{Data: []byte(`{"a":{"b":"nested"},"a.b":"flat"}`)},
		"locales/de.json": &fstest.MapFile{Data: []byte(`{"greeting":{"hello":"Hallo"}}`)},
	}
	b, err := i18n.New(i18n.WithFS(fsys, "locales"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	// en.json should have been skipped
	if b.HasLanguage(i18n.English) {
		t.Error("English should not be loaded (duplicate key collision)")
	}
	// de.json should still load fine
	if got := b.T(i18n.German, "greeting.hello"); got != "Hallo" {
		t.Errorf("T(German) = %q, want %q", got, "Hallo")
	}
}
