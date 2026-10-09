package i18n_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/valentin-kaiser/go-core/i18n"
)

func resolve(t *testing.T, b *i18n.Bundle, accept string) i18n.Language {
	t.Helper()
	var got i18n.Language
	h := i18n.Middleware(b)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = i18n.LanguageFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", accept)
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// Languages registered after the first request must be matched; the matcher is cached between requests.
func TestMiddlewareSeesLanguagesAddedLater(t *testing.T) {
	b, err := i18n.New(i18n.WithMap(i18n.English, map[string]string{"hello": "Hello"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, b, "de"); got != i18n.English {
		t.Fatalf("before registering German: got %q, want %q", got, i18n.English)
	}

	b.Register(i18n.German, map[string]string{"hello": "Hallo"})
	if got := resolve(t, b, "de"); got != i18n.German {
		t.Fatalf("after Register: got %q, want %q", got, i18n.German)
	}

	if err := b.RegisterJSON("fr", []byte(`{"hello":"Bonjour"}`)); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, b, "fr-CA,fr;q=0.9"); got != "fr" {
		t.Fatalf("after RegisterJSON: got %q, want fr", got)
	}
}

func TestMiddlewareConcurrentRegister(t *testing.T) {
	b, err := i18n.New(i18n.WithMap(i18n.English, map[string]string{"hello": "Hello"}))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			b.Register(i18n.German, map[string]string{"k": "v"})
			b.Register(i18n.Language("fr"), map[string]string{"k": "v"})
		}
	}()
	for i := 0; i < 200; i++ {
		_ = resolve(t, b, "de,fr;q=0.8")
	}
	<-done
	if got := resolve(t, b, "de"); got != i18n.German {
		t.Fatalf("got %q, want %q", got, i18n.German)
	}
}
