package i18n_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/valentin-kaiser/go-core/i18n"
)

func benchBundle(b *testing.B) *i18n.Bundle {
	b.Helper()
	bundle, err := i18n.New(
		i18n.WithMap(i18n.English, map[string]string{"hello": "Hello", "greeting": "Hello, %s! You have %d messages."}),
		i18n.WithMap(i18n.German, map[string]string{"hello": "Hallo"}),
	)
	if err != nil {
		b.Fatal(err)
	}
	return bundle
}

func BenchmarkBundleT(b *testing.B) {
	bundle := benchBundle(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bundle.T(i18n.German, "hello")
	}
}

func BenchmarkBundleTFallback(b *testing.B) {
	bundle := benchBundle(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bundle.T(i18n.German, "greeting")
	}
}

func BenchmarkBundleTParallel(b *testing.B) {
	bundle := benchBundle(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = bundle.T(i18n.German, "hello")
		}
	})
}

func BenchmarkBundleTf(b *testing.B) {
	bundle := benchBundle(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bundle.Tf(i18n.English, "greeting", "Ann", i)
	}
}

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = i18n.Parse("de-DE")
	}
}

// Middleware resolves the language from Accept-Language and stores it in the context.
func BenchmarkMiddleware(b *testing.B) {
	bundle := benchBundle(b)
	h := i18n.Middleware(bundle)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_ = i18n.TCTX(r.Context(), "hello")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, req)
	}
}

func BenchmarkContextRoundTrip(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = i18n.LanguageFromContext(i18n.WithLanguage(ctx, i18n.German))
	}
}
