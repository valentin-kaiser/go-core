package web_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/valentin-kaiser/go-core/web"
)

// Requests are served while routes, status hooks and middlewares are registered.
func TestRouterConcurrentRegistration(t *testing.T) {
	r := web.NewRouter()
	r.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			p := "/p" + strconv.Itoa(i)
			r.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			r.OnStatus(p, http.StatusNotFound, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			r.Use(web.MiddlewareOrderDefault, func(next http.Handler) http.Handler { return next })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p"+strconv.Itoa(i%50), nil))
		}
	}()
	wg.Wait()
}

// A status hook registered after the router served requests has to be used by the next request.
func TestRouterStatusHookRegisteredLater(t *testing.T) {
	r := web.NewRouter()
	r.HandleFunc("/gone", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gone", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("before hook: status %d, want 404", rec.Code)
	}

	r.OnStatus("/gone", http.StatusNotFound, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gone", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("after hook: status %d, want 418", rec.Code)
	}
}
