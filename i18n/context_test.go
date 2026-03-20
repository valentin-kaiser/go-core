package i18n_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/valentin-kaiser/go-core/i18n"
)

// helper that creates a bundle with English and German translations.
func newTestBundle(t *testing.T) *i18n.Bundle {
	t.Helper()
	b, err := i18n.New(
		i18n.WithMap(i18n.English, map[string]string{
			"greeting": "Hello",
			"farewell": "Goodbye",
			"fmt":      "Hello %s, you have %d messages",
		}),
		i18n.WithMap(i18n.German, map[string]string{
			"greeting": "Hallo",
			"farewell": "Auf Wiedersehen",
			"fmt":      "Hallo %s, du hast %d Nachrichten",
		}),
	)
	if err != nil {
		t.Fatalf("failed to create test bundle: %v", err)
	}
	return b
}

func TestWithLanguageRoundTrip(t *testing.T) {
	ctx := i18n.WithLanguage(context.Background(), i18n.German)
	lang, ok := i18n.LanguageFromContext(ctx)
	if !ok {
		t.Fatal("expected language in context")
	}
	if lang != i18n.German {
		t.Fatalf("got %q, want %q", lang, i18n.German)
	}
}

func TestLanguageFromContext_Empty(t *testing.T) {
	_, ok := i18n.LanguageFromContext(context.Background())
	if ok {
		t.Fatal("expected no language in empty context")
	}
}

func TestWithRequestRoundTrip(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	ctx := i18n.WithRequest(context.Background(), r)
	got, ok := i18n.RequestFromContext(ctx)
	if !ok {
		t.Fatal("expected request in context")
	}
	if got != r {
		t.Fatal("request mismatch")
	}
}

func TestRequestFromContext_Empty(t *testing.T) {
	_, ok := i18n.RequestFromContext(context.Background())
	if ok {
		t.Fatal("expected no request in empty context")
	}
}

func TestTCTX_DirectLanguage(t *testing.T) {
	b := newTestBundle(t)
	ctx := i18n.WithLanguage(context.Background(), i18n.German)

	got := b.TCTX(ctx, "greeting")
	if got != "Hallo" {
		t.Fatalf("TCTX with direct German language: got %q, want %q", got, "Hallo")
	}
}

func TestTCTX_FromRequest(t *testing.T) {
	b := newTestBundle(t)
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	ctx := i18n.WithRequest(context.Background(), r)

	got := b.TCTX(ctx, "greeting")
	if got != "Hallo" {
		t.Fatalf("TCTX via Accept-Language: got %q, want %q", got, "Hallo")
	}
}

func TestTCTX_Priority_LanguageOverRequest(t *testing.T) {
	b := newTestBundle(t)

	// Request says German, but direct language says English.
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "de")
	ctx := i18n.WithRequest(context.Background(), r)
	ctx = i18n.WithLanguage(ctx, i18n.English)

	got := b.TCTX(ctx, "greeting")
	if got != "Hello" {
		t.Fatalf("TCTX priority: got %q, want %q (direct language should win)", got, "Hello")
	}
}

func TestTCTX_Fallback(t *testing.T) {
	b := newTestBundle(t)
	// Empty context, no language or request.
	got := b.TCTX(context.Background(), "greeting")
	if got != "Hello" {
		t.Fatalf("TCTX fallback: got %q, want %q", got, "Hello")
	}
}

func TestTCTX_UnsupportedLanguage(t *testing.T) {
	b := newTestBundle(t)
	// Accept-Language for a language not in the bundle.
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "ja")
	ctx := i18n.WithRequest(context.Background(), r)

	got := b.TCTX(ctx, "greeting")
	// Should fall back to Default (English).
	if got != "Hello" {
		t.Fatalf("TCTX unsupported language: got %q, want %q", got, "Hello")
	}
}

func TestTfCTX(t *testing.T) {
	b := newTestBundle(t)
	ctx := i18n.WithLanguage(context.Background(), i18n.German)

	got := b.TfCTX(ctx, "fmt", "Max", 5)
	want := "Hallo Max, du hast 5 Nachrichten"
	if got != want {
		t.Fatalf("TfCTX: got %q, want %q", got, want)
	}
}

func TestGlobalTCTX(t *testing.T) {
	err := i18n.Init(
		i18n.WithMap(i18n.English, map[string]string{"greeting": "Hello"}),
		i18n.WithMap(i18n.German, map[string]string{"greeting": "Hallo"}),
	)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

	ctx := i18n.WithLanguage(context.Background(), i18n.German)
	got := i18n.TCTX(ctx, "greeting")
	if got != "Hallo" {
		t.Fatalf("global TCTX: got %q, want %q", got, "Hallo")
	}
}

func TestGlobalTfCTX(t *testing.T) {
	err := i18n.Init(
		i18n.WithMap(i18n.English, map[string]string{"fmt": "Hello %s"}),
		i18n.WithMap(i18n.German, map[string]string{"fmt": "Hallo %s"}),
	)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

	ctx := i18n.WithLanguage(context.Background(), i18n.German)
	got := i18n.TfCTX(ctx, "fmt", "World")
	want := "Hallo World"
	if got != want {
		t.Fatalf("global TfCTX: got %q, want %q", got, want)
	}
}

func TestTCTX_DirectLanguageNotInBundle(t *testing.T) {
	b := newTestBundle(t)
	// Set a language that isn't loaded in the bundle.
	ctx := i18n.WithLanguage(context.Background(), i18n.Language("fr"))

	got := b.TCTX(ctx, "greeting")
	// Should fall back to Default (English).
	if got != "Hello" {
		t.Fatalf("TCTX with unloaded direct language: got %q, want %q", got, "Hello")
	}
}

func TestTCTX_EmptyAcceptLanguage(t *testing.T) {
	b := newTestBundle(t)
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	// No Accept-Language header set.
	ctx := i18n.WithRequest(context.Background(), r)

	got := b.TCTX(ctx, "greeting")
	if got != "Hello" {
		t.Fatalf("TCTX with empty Accept-Language: got %q, want %q", got, "Hello")
	}
}
