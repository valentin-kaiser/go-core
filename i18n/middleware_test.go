package i18n_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/valentin-kaiser/go-core/i18n"
)

func TestMiddleware_German(t *testing.T) {
	b := newTestBundle(t)

	var captured string
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = b.TCTX(r.Context(), "greeting")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "Hallo" {
		t.Fatalf("Middleware German: got %q, want %q", captured, "Hallo")
	}
}

func TestMiddleware_English(t *testing.T) {
	b := newTestBundle(t)

	var captured string
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = b.TCTX(r.Context(), "greeting")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "Hello" {
		t.Fatalf("Middleware English: got %q, want %q", captured, "Hello")
	}
}

func TestMiddleware_NoAcceptLanguage(t *testing.T) {
	b := newTestBundle(t)

	var captured string
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = b.TCTX(r.Context(), "greeting")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "Hello" {
		t.Fatalf("Middleware no Accept-Language: got %q, want %q", captured, "Hello")
	}
}

func TestMiddleware_UnsupportedFallback(t *testing.T) {
	b := newTestBundle(t)

	var captured string
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = b.TCTX(r.Context(), "greeting")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "ja,zh;q=0.9")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Should fall back to English (Default).
	if captured != "Hello" {
		t.Fatalf("Middleware unsupported: got %q, want %q", captured, "Hello")
	}
}

func TestMiddleware_LanguageFromContext(t *testing.T) {
	b := newTestBundle(t)

	var lang i18n.Language
	var ok bool
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang, ok = i18n.LanguageFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de-DE")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !ok {
		t.Fatal("expected language in context after middleware")
	}
	if lang != i18n.German {
		t.Fatalf("LanguageFromContext after middleware: got %q, want %q", lang, i18n.German)
	}
}

func TestMiddleware_RequestFromContext(t *testing.T) {
	b := newTestBundle(t)

	var req *http.Request
	var ok bool
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, ok = i18n.RequestFromContext(r.Context())
	}))

	inReq := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(httptest.NewRecorder(), inReq)

	if !ok {
		t.Fatal("expected request in context after middleware")
	}
	if req == nil {
		t.Fatal("request in context is nil")
	}
}

func TestGlobalMiddleware(t *testing.T) {
	err := i18n.Init(
		i18n.WithMap(i18n.English, map[string]string{"greeting": "Hello"}),
		i18n.WithMap(i18n.German, map[string]string{"greeting": "Hallo"}),
	)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer i18n.Init()

	var captured string
	handler := i18n.GlobalMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = i18n.TCTX(r.Context(), "greeting")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "Hallo" {
		t.Fatalf("GlobalMiddleware: got %q, want %q", captured, "Hallo")
	}
}

func TestMiddleware_QualityWeightOrdering(t *testing.T) {
	b := newTestBundle(t)

	var captured string
	handler := i18n.Middleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = b.TCTX(r.Context(), "greeting")
	}))

	// Prefer English (q=0.9) over German (q=0.8).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de;q=0.8, en;q=0.9")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "Hello" {
		t.Fatalf("Middleware quality weight: got %q, want %q", captured, "Hello")
	}
}
