package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/valentin-kaiser/go-core/logging"
)

type benchWriter struct{ h http.Header }

func (d *benchWriter) Header() http.Header         { return d.h }
func (d *benchWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *benchWriter) WriteHeader(int)             {}

func benchLogger(b *testing.B, level logging.Level) {
	b.Helper()
	prev := logger
	logger = logging.NewZerologAdapterWithLogger(zerolog.New(io.Discard)).SetLevel(level)
	b.Cleanup(func() { logger = prev })
}

var benchOK = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func benchCORS() http.Handler {
	return corsHeaderMiddlewareWithConfig(&CORSConfig{
		AllowOrigins: []string{"https://a.example", "https://b.example"},
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Content-Type", "Authorization"},
	})(benchOK)
}

// logMiddleware builds nine fields per request; the question is what it costs when Debug is off.
func BenchmarkLogMiddlewareDisabled(b *testing.B) {
	benchLogger(b, logging.InfoLevel)
	h := logMiddleware(benchOK)
	req := httptest.NewRequest(http.MethodGet, "/api/users/42?x=1", nil)
	w := &benchWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(w.h)
		h.ServeHTTP(w, req)
	}
}

func BenchmarkLogMiddlewareEnabled(b *testing.B) {
	benchLogger(b, logging.DebugLevel)
	h := logMiddleware(benchOK)
	req := httptest.NewRequest(http.MethodGet, "/api/users/42?x=1", nil)
	w := &benchWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(w.h)
		h.ServeHTTP(w, req)
	}
}

func BenchmarkLogMiddlewareDisabledParallel(b *testing.B) {
	benchLogger(b, logging.InfoLevel)
	h := logMiddleware(benchOK)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/42?x=1", nil)
		w := &benchWriter{h: http.Header{}}
		for pb.Next() {
			clear(w.h)
			h.ServeHTTP(w, req)
		}
	})
}

func BenchmarkCORSMiddleware(b *testing.B) {
	h := benchCORS()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://b.example")
	w := &benchWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(w.h)
		h.ServeHTTP(w, req)
	}
}

func BenchmarkCORSMiddlewareParallel(b *testing.B) {
	h := benchCORS()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://b.example")
		w := &benchWriter{h: http.Header{}}
		for pb.Next() {
			clear(w.h)
			h.ServeHTTP(w, req)
		}
	})
}

func BenchmarkVaryHeaderMiddleware(b *testing.B) {
	h := varyHeaderMiddleware("Origin", "Accept-Encoding")(benchOK)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := &benchWriter{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(w.h)
		h.ServeHTTP(w, req)
	}
}
