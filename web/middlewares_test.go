package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// noopHandler is a simple handler that writes a 200 OK response.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestDefaultCORSHeaders(t *testing.T) {
	handler := corsHeaderMiddleware(noopHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertHeader(t, rec, "Access-Control-Allow-Origin", "*")
	assertHeader(t, rec, "Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	assertHeader(t, rec, "Access-Control-Allow-Headers", "Content-Type, Authorization, X-Real-IP")

	// Default CORS should NOT include credentials (wildcard + credentials is invalid)
	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("default CORS should not set Allow-Credentials, got %q", v)
	}
}

func TestCORSConfigNil(t *testing.T) {
	// nil config should use the same default middleware
	handler := corsHeaderMiddlewareWithConfig(nil)(noopHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertHeader(t, rec, "Access-Control-Allow-Origin", "*")
	assertHeader(t, rec, "Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
}

func TestCORSConfigDefaults(t *testing.T) {
	// Empty config should apply sensible defaults
	handler := corsHeaderMiddlewareWithConfig(&CORSConfig{})(noopHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertHeader(t, rec, "Access-Control-Allow-Origin", "*")
	assertHeader(t, rec, "Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	assertHeader(t, rec, "Access-Control-Allow-Headers", "Content-Type, Authorization, X-Real-IP")

	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("empty config should not set Allow-Credentials, got %q", v)
	}
	if v := rec.Header().Get("Access-Control-Max-Age"); v != "" {
		t.Errorf("empty config should not set Max-Age, got %q", v)
	}
	if v := rec.Header().Get("Access-Control-Expose-Headers"); v != "" {
		t.Errorf("empty config should not set Expose-Headers, got %q", v)
	}
}

func TestCORSConfigCustomValues(t *testing.T) {
	config := &CORSConfig{
		AllowOrigin:  "https://example.com",
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"X-Custom"},
		MaxAge:       3600,
		ExposeHeaders: []string{"X-Request-Id", "X-Trace-Id"},
	}
	handler := corsHeaderMiddlewareWithConfig(config)(noopHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertHeader(t, rec, "Access-Control-Allow-Origin", "https://example.com")
	assertHeader(t, rec, "Access-Control-Allow-Methods", "GET, POST")
	assertHeader(t, rec, "Access-Control-Allow-Headers", "X-Custom")
	assertHeader(t, rec, "Access-Control-Max-Age", "3600")
	assertHeader(t, rec, "Access-Control-Expose-Headers", "X-Request-Id, X-Trace-Id")

	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("credentials should not be set when AllowCredentials=false, got %q", v)
	}
}

func TestCORSConfigCredentialsEchoesOrigin(t *testing.T) {
	config := &CORSConfig{
		AllowOrigin:      "https://fallback.example.com",
		AllowMethods:     []string{"GET"},
		AllowCredentials: true,
	}
	handler := corsHeaderMiddlewareWithConfig(config)(noopHandler)

	t.Run("with Origin header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Should echo the request Origin, not the configured AllowOrigin
		assertHeader(t, rec, "Access-Control-Allow-Origin", "https://app.example.com")
		assertHeader(t, rec, "Access-Control-Allow-Credentials", "true")
		assertHeader(t, rec, "Vary", "Origin")
	})

	t.Run("without Origin header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Should fall back to configured AllowOrigin
		assertHeader(t, rec, "Access-Control-Allow-Origin", "https://fallback.example.com")
		assertHeader(t, rec, "Access-Control-Allow-Credentials", "true")
		assertHeader(t, rec, "Vary", "Origin")
	})
}

func TestCORSConfigCredentialsDefaultOrigin(t *testing.T) {
	// When AllowCredentials=true and AllowOrigin is empty, the fallback should
	// still be "*" but the request Origin is preferred when present.
	config := &CORSConfig{
		AllowCredentials: true,
	}
	handler := corsHeaderMiddlewareWithConfig(config)(noopHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://test.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertHeader(t, rec, "Access-Control-Allow-Origin", "https://test.example.com")
	assertHeader(t, rec, "Access-Control-Allow-Credentials", "true")
}

func TestCORSConfigExposeHeadersOmittedWhenEmpty(t *testing.T) {
	config := &CORSConfig{
		AllowOrigin:   "https://example.com",
		ExposeHeaders: []string{},
	}
	handler := corsHeaderMiddlewareWithConfig(config)(noopHandler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Expose-Headers"); v != "" {
		t.Errorf("Expose-Headers should be omitted for empty slice, got %q", v)
	}
}

func assertHeader(t *testing.T, rec *httptest.ResponseRecorder, key, want string) {
	t.Helper()
	got := rec.Header().Get(key)
	if got != want {
		t.Errorf("header %q = %q, want %q", key, got, want)
	}
}
