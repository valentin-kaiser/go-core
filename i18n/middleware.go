package i18n

import (
	"net/http"
	"strings"

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

	tags, _, err := language.ParseAcceptLanguage(accept)
	if err != nil || len(tags) == 0 {
		return Default
	}

	langs := b.Languages()
	if len(langs) == 0 {
		return Default
	}

	// Build a matcher from the bundle's supported languages.
	supported := make([]language.Tag, 0, len(langs))
	for _, l := range langs {
		tag, err := language.Parse(string(l))
		if err != nil {
			continue
		}
		supported = append(supported, tag)
	}
	if len(supported) == 0 {
		return Default
	}

	matcher := language.NewMatcher(supported)
	matched, _, _ := matcher.Match(tags...)

	// Convert the matched tag back to our Language type.
	// Use the base language to normalize (e.g. "en-US" → "en").
	base, _ := matched.Base()
	lang := Language(strings.ToLower(base.String()))

	if b.HasLanguage(lang) {
		return lang
	}

	return Default
}
