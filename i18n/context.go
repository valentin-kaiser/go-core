package i18n

import (
	"context"
	"net/http"
)

// ContextKey is the type used for context keys in this package. Using a distinct type helps avoid collisions with other context keys in the application.
type ContextKey string

var (
	// LanguageKey is the context key used to store and retrieve the resolved
	// Language directly. It takes priority over the request key when both are set.
	LanguageKey any = ContextKey("Language")

	// RequestKey is the context key used to store and retrieve an *http.Request.
	// The request's Accept-Language header is parsed to determine the language
	// when no direct Language is found in the context.
	RequestKey any = ContextKey("Request")
)

// WithLanguage returns a new context that carries the given Language.
// Downstream code can retrieve it with [LanguageFromContext].
func WithLanguage(ctx context.Context, lang Language) context.Context {
	return context.WithValue(ctx, LanguageKey, lang)
}

// LanguageFromContext extracts the Language stored by [WithLanguage].
// It returns the language and true if found, or "" and false otherwise.
func LanguageFromContext(ctx context.Context) (Language, bool) {
	lang, ok := ctx.Value(LanguageKey).(Language)
	return lang, ok
}

// WithRequest returns a new context that carries the given *http.Request.
// The [Bundle.TCTX] method will parse the request's Accept-Language header
// as a fallback when no direct Language is present in the context.
func WithRequest(ctx context.Context, r *http.Request) context.Context {
	return context.WithValue(ctx, RequestKey, r)
}

// RequestFromContext extracts the *http.Request stored by [WithRequest].
// It returns the request and true if found, or nil and false otherwise.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(RequestKey).(*http.Request)
	return r, ok
}
