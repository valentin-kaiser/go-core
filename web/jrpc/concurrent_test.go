package jrpc_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Concurrent first calls validate the method signature; that must not race.
func TestUnaryConcurrentCalls(t *testing.T) {
	mux := benchMux()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				req := httptest.NewRequest(http.MethodPost, "/rpc/Health/Check", strings.NewReader(`{"service":"x"}`))
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "status") {
					t.Errorf("status %d body %q", rec.Code, rec.Body.String())
					return
				}
			}
		}()
	}
	wg.Wait()
}
