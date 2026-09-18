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
		AllowOrigin:   "https://example.com",
		AllowMethods:  []string{"GET", "POST"},
		AllowHeaders:  []string{"X-Custom"},
		MaxAge:        3600,
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
		AllowOrigin:      "https://app.example.com",
		AllowMethods:     []string{"GET"},
		AllowCredentials: true,
	}
	handler := corsHeaderMiddlewareWithConfig(config)(noopHandler)

	t.Run("matching Origin is reflected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		assertHeader(t, rec, "Access-Control-Allow-Origin", "https://app.example.com")
		assertHeader(t, rec, "Access-Control-Allow-Credentials", "true")
		assertHeader(t, rec, "Vary", "Origin")
	})

	t.Run("non-matching Origin does not get credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://evil.example.com")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Origin should NOT be reflected for a non-matching origin
		if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("non-matching Origin should not set Allow-Origin, got %q", v)
		}
		if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
			t.Errorf("non-matching Origin should not set Allow-Credentials, got %q", v)
		}
		// Vary: Origin must still be present (caching correctness)
		assertHeader(t, rec, "Vary", "Origin")
	})

	t.Run("missing Origin does not get credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// No Origin header means no credentialed CORS headers
		if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
			t.Errorf("missing Origin should not set Allow-Origin, got %q", v)
		}
		if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
			t.Errorf("missing Origin should not set Allow-Credentials, got %q", v)
		}
		assertHeader(t, rec, "Vary", "Origin")
	})
}

func TestCORSConfigCredentialsWildcardOriginRejected(t *testing.T) {
	// When AllowCredentials=true and AllowOrigin is empty (defaults to "*"),
	// no origin should be reflected because * + credentials is invalid.
	config := &CORSConfig{
		AllowCredentials: true,
	}
	handler := corsHeaderMiddlewareWithConfig(config)(noopHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://test.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("wildcard origin with credentials should not set Allow-Origin, got %q", v)
	}
	if v := rec.Header().Get("Access-Control-Allow-Credentials"); v != "" {
		t.Errorf("wildcard origin with credentials should not set Allow-Credentials, got %q", v)
	}
	// Vary: Origin should still be set
	assertHeader(t, rec, "Vary", "Origin")
}

func TestCORSVaryHeaderDedup(t *testing.T) {
	config := &CORSConfig{
		AllowOrigin:      "https://example.com",
		AllowCredentials: true,
	}
	handler := corsHeaderMiddlewareWithConfig(config)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate another middleware/handler setting Vary: Origin
		w.Header().Set("Vary", "Origin")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Vary should contain Origin exactly once, not duplicated
	varyValues := rec.Header().Values("Vary")
	count := 0
	for _, v := range varyValues {
		if v == "Origin" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Vary should contain 'Origin' exactly once, got %d occurrences in %v", count, varyValues)
	}
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

func TestRouteCacheControlOverridesServerWide(t *testing.T) {
	server := New()
	server.WithCacheControl("public, max-age=3600")
	server.WithRouteCacheControl("/api/", "no-store")
	if server.Error != nil {
		t.Fatalf("unexpected error: %v", server.Error)
	}

	handler := securityHeaderMiddlewareWithServer(server)(noopHandler)

	// Route-specific pattern should win over the server-wide default
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertHeader(t, rec, "Cache-Control", "no-store")

	// Requests outside the pattern fall back to the server-wide value
	req = httptest.NewRequest(http.MethodGet, "/other", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertHeader(t, rec, "Cache-Control", "public, max-age=3600")
}

func TestRouteCacheControlWithoutServerWideDefault(t *testing.T) {
	server := New()
	server.WithRouteCacheControl("/static/", "public, max-age=31536000, immutable")
	if server.Error != nil {
		t.Fatalf("unexpected error: %v", server.Error)
	}

	handler := securityHeaderMiddlewareWithServer(server)(noopHandler)

	req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertHeader(t, rec, "Cache-Control", "public, max-age=31536000, immutable")

	// Unmatched routes fall back to the default security header value
	req = httptest.NewRequest(http.MethodGet, "/other", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertHeader(t, rec, "Cache-Control", securityHeaders["Cache-Control"])
}

func TestRouteCacheControlDuplicatePatternErrors(t *testing.T) {
	server := New()
	server.WithRouteCacheControl("/api/", "no-store")
	server.WithRouteCacheControl("/api/", "public")

	if server.Error == nil {
		t.Fatal("expected error when registering a cache control value for an already-registered pattern")
	}
}

func assertHeader(t *testing.T, rec *httptest.ResponseRecorder, key, want string) {
	t.Helper()
	got := rec.Header().Get(key)
	if got != want {
		t.Errorf("header %q = %q, want %q", key, got, want)
	}
}
