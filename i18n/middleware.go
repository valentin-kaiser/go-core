package i18n

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/text/language"
)

// Middleware returns an HTTP middleware that resolves the caller's preferred
// language from the request's Accept-Language header, stores the resolved
// [Language] in the request context (retrievable via [LanguageFromContext]),
// and also stores the *http.Request itself (retrievable via [RequestFromContext]).
//
// The resolved language is the best match among the languages loaded in the
// given [Bundle]. If the Accept-Language header is missing or no match is found,
// the [Default] language is used.
//
// Usage with a standard [http.ServeMux]:
//
//	bundle, _ := i18n.New(i18n.WithFS(localesFS, "locales"))
//	mux := http.NewServeMux()
//	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
//	    msg := bundle.TCTX(r.Context(), "greeting")
//	    fmt.Fprintln(w, msg)
//	})
//	http.ListenAndServe(":8080", i18n.Middleware(bundle)(mux))
func Middleware(b *Bundle) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lang := resolveFromRequest(b, r)
			ctx := WithLanguage(r.Context(), lang)
			ctx = WithRequest(ctx, r)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GlobalMiddleware returns an HTTP middleware that uses the global default
// [Bundle]. See [Middleware] for details.
func GlobalMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := globalBundle.Load()
			lang := resolveFromRequest(b, r)
			ctx := WithLanguage(r.Context(), lang)
			ctx = WithRequest(ctx, r)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveFromRequest determines the best matching [Language] from the request's
// Accept-Language header against the languages loaded in the bundle. If no
// match is found or the header is absent, [Default] is returned.
func resolveFromRequest(b *Bundle, r *http.Request) Language {
	accept := r.Header.Get("Accept-Language")
	if accept == "" {
		return Default
	}

	m := b.languageMatcher()
	if m == nil {
		return Default
	}

	// Clients send the same few Accept-Language values over and over
	if cached, ok := m.results.Load(accept); ok {
		if lang, ok := cached.(Language); ok {
			return lang
		}
	}

	lang := m.resolve(b, accept)
	// The header is client controlled, so the number of cached values is bounded
	if m.cached.Load() < maxCachedHeaders {
		if _, loaded := m.results.LoadOrStore(accept, lang); !loaded {
			m.cached.Add(1)
		}
	}
	return lang
}

// maxCachedHeaders bounds the Accept-Language values remembered per language set
const maxCachedHeaders = 256

// resolve parses the header and picks the best supported language, or Default
func (m *languageMatcher) resolve(b *Bundle, accept string) Language {
	tags, _, err := language.ParseAcceptLanguage(accept)
	if err != nil || len(tags) == 0 {
		return Default
	}
	matched, _, _ := m.matcher.Match(tags...)

	// Convert the matched tag back to our Language type.
	// Use the base language to normalize (e.g. "en-US" → "en").
	base, _ := matched.Base()
	lang := Language(strings.ToLower(base.String()))

	if b.HasLanguage(lang) {
		return lang
	}

	return Default
}

// languageMatcher returns the matcher for the languages loaded in the bundle, or
// nil if there are none. Building a matcher parses every supported language and
// is by far the most expensive part of resolving a request, so it is built once
// and kept until a language is added.
func (b *Bundle) languageMatcher() *languageMatcher {
	if m := b.matcher.Load(); m != nil {
		return m
	}

	// Build and publish while holding the read lock. Writers hold the write lock
	// while they add a language and drop the cache, so a matcher built from an
	// older set of languages can not be stored after that.
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.translations) == 0 {
		return nil
	}

	// Place the Default language first so the matcher falls back to it
	// when no requested language matches (the first entry is the matcher's default).
	supported := make([]language.Tag, 0, len(b.translations)+1)
	defaultTag, _ := language.Parse(string(Default))
	supported = append(supported, defaultTag)
	for l := range b.translations {
		if l == Default {
			continue
		}
		tag, err := language.Parse(string(l))
		if err != nil {
			continue
		}
		supported = append(supported, tag)
	}

	m := &languageMatcher{matcher: language.NewMatcher(supported)}
	b.matcher.Store(m)
	return m
}

// languageMatcher wraps language.Matcher so it can be held in an atomic.Pointer
type languageMatcher struct {
	matcher language.Matcher
	// results remembers the language resolved for an Accept-Language value
	results sync.Map
	cached  atomic.Int64
}
