package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

func serve(r *Router, remote string, headers map[string]string) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func ipRouter(t *testing.T) *Router {
	t.Helper()
	r := NewRouter()
	r.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return r
}

func TestRouterRejectsInvalidClientAddress(t *testing.T) {
	r := ipRouter(t)
	for _, remote := range []string{"not-an-ip:1", "999.1.1.1:80", "[fe80::1%eth0]:80"} {
		if code := serve(r, remote, nil); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", remote, code)
		}
	}
	if code := serve(r, "192.0.2.1:1234", nil); code != http.StatusOK {
		t.Errorf("valid address: status %d", code)
	}
	if code := serve(r, "[2001:db8::1]:80", nil); code != http.StatusOK {
		t.Errorf("valid IPv6 address: status %d", code)
	}
}

func TestRouterForwardedHeaders(t *testing.T) {
	r := ipRouter(t)
	if err := r.setBlacklist([]string{"203.0.113.0/24"}); err != nil {
		t.Fatal(err)
	}

	if code := serve(r, "192.0.2.1:1", map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.1"}); code != http.StatusForbidden {
		t.Errorf("first X-Forwarded-For entry is blacklisted: status %d, want 403", code)
	}
	if code := serve(r, "192.0.2.1:1", map[string]string{"X-Real-IP": " 203.0.113.9 "}); code != http.StatusForbidden {
		t.Errorf("X-Real-IP is blacklisted: status %d, want 403", code)
	}
	if code := serve(r, "203.0.113.5:1", nil); code != http.StatusForbidden {
		t.Errorf("remote address is blacklisted: status %d, want 403", code)
	}
	if code := serve(r, "192.0.2.1:1", map[string]string{"X-Forwarded-For": "198.51.100.1"}); code != http.StatusOK {
		t.Errorf("clean address: status %d, want 200", code)
	}

	if err := r.setWhitelist([]string{"203.0.113.7/32"}); err != nil {
		t.Fatal(err)
	}
	if code := serve(r, "192.0.2.1:1", map[string]string{"X-Forwarded-For": "203.0.113.7"}); code != http.StatusOK {
		t.Errorf("whitelisted address in a blacklisted range: status %d, want 200", code)
	}
}

// The honeypot adds to the blacklist while requests read it.
func TestRouterHoneypotConcurrentWithRequests(t *testing.T) {
	r := ipRouter(t)
	var mu sync.Mutex
	var last map[string]struct{}
	r.honeypotCallback = func(m map[string]*netIPNet) {
		mu.Lock()
		defer mu.Unlock()
		last = make(map[string]struct{}, len(m))
		for k := range m {
			last[k] = struct{}{}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			req := httptest.NewRequest(http.MethodGet, "/trap", nil)
			req.Header.Set("X-Real-IP", "198.51.100."+itoa(i%250+1))
			r.honeypot(httptest.NewRecorder(), req)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 400; i++ {
			serve(r, "192.0.2.1:1", nil)
		}
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(last) == 0 {
		t.Fatal("the honeypot callback never saw a blacklist entry")
	}
	if code := serve(r, "198.51.100.5:1", nil); code != http.StatusForbidden {
		t.Errorf("a honeypot victim is not blocked: status %d", code)
	}
}

type netIPNet = net.IPNet

func itoa(i int) string { return strconv.Itoa(i) }
