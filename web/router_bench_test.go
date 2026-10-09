package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/valentin-kaiser/go-core/web"
)

type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardWriter) WriteHeader(int)             {}

func benchRouter() *web.Router {
	r := web.NewRouter()
	for _, p := range []string{"/a", "/b", "/api/users", "/api/users/{id}", "/static/", "/health"} {
		r.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	}
	return r
}

// The request is built once so the benchmark measures the router, not httptest.
func BenchmarkRouterServeHTTP(b *testing.B) {
	r := benchRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	w := &discardWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.ServeHTTP(w, req)
	}
}

func BenchmarkRouterServeHTTPParallel(b *testing.B) {
	r := benchRouter()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
		w := &discardWriter{h: http.Header{}}
		for pb.Next() {
			r.ServeHTTP(w, req)
		}
	})
}

// A router configured like a real server: status hooks and middlewares registered.
func benchConfiguredRouter() *web.Router {
	r := benchRouter()
	for _, p := range []string{"/a", "/api/", "/static/", "/health"} {
		r.OnStatus(p, http.StatusNotFound, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	}
	for i := 0; i < 4; i++ {
		r.Use(web.MiddlewareOrderDefault, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { next.ServeHTTP(w, req) })
		})
	}
	return r
}

func BenchmarkRouterConfiguredServeHTTP(b *testing.B) {
	r := benchConfiguredRouter()
	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	w := &discardWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.ServeHTTP(w, req)
	}
}

func BenchmarkRouterConfiguredServeHTTPParallel(b *testing.B) {
	r := benchConfiguredRouter()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
		w := &discardWriter{h: http.Header{}}
		for pb.Next() {
			r.ServeHTTP(w, req)
		}
	})
}
