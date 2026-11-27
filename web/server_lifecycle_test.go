package web_test

import (
	"net"
	"net/http"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/web"
)

const (
	loopbackHost      = "127.0.0.1"
	listenTimeout     = 5 * time.Second
	dialTimeout       = 100 * time.Millisecond
	doneTimeout       = 10 * time.Second
	okPath            = "/ok"
	otherPath         = "/other"
	redirectTarget    = "/target"
	redirectHeader    = "X-Redirect"
	redirectHeaderVal = "yes"
)

// response holds what the tests check of a response whose body is already closed.
type response struct {
	status int
	header http.Header
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		t.Fatalf("reserving a port failed: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a tcp listener always has a tcp address
	err = listener.Close()
	if err != nil {
		t.Fatalf("releasing the reserved port failed: %v", err)
	}
	return uint16(port) //nolint:gosec // ports are below 65536
}

func waitListening(t *testing.T, port uint16) {
	t.Helper()
	addr := net.JoinHostPort(loopbackHost, strconv.Itoa(int(port)))
	deadline := time.Now().Add(listenTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			runtime.Gosched()
			continue
		}
		err = conn.Close()
		if err != nil {
			t.Fatalf("closing probe connection failed: %v", err)
		}
		return
	}
	t.Fatalf("server did not listen on %s within %s", addr, listenTimeout)
}

func waitDone(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s returned error: %v", what, err)
		}
	case <-time.After(doneTimeout):
		t.Fatalf("%s did not finish within %s", what, doneTimeout)
	}
}

func startServer(t *testing.T, server *web.Server, port uint16) chan error {
	t.Helper()
	done := make(chan error, 1)
	server.StartAsync(done)
	waitListening(t, port)
	return done
}

func request(t *testing.T, port uint16, path, host string, header http.Header) response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	url := "http://" + net.JoinHostPort(loopbackHost, strconv.Itoa(int(port))) + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("creating request failed: %v", err)
	}
	req.Header = header
	if host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	err = resp.Body.Close()
	if err != nil {
		t.Fatalf("closing response body failed: %v", err)
	}
	return response{status: resp.StatusCode, header: resp.Header}
}

func TestServerRedirectRule(t *testing.T) {
	source, target := freePort(t), freePort(t)
	server := web.New().WithHost(loopbackHost).WithHTTPPort(source).
		WithHandlerFunc(okPath, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Vary", "Accept")
			w.WriteHeader(http.StatusOK)
		}).
		WithVaryHeaders([]string{"Origin", "Accept-Encoding"}).
		WithRedirect(web.RedirectRule{
			SourcePort:  source,
			TargetPort:  target,
			SourceRoute: "/",
			TargetRoute: redirectTarget,
			Exceptions:  []string{okPath},
			Conditions:  []func(*http.Request) bool{func(r *http.Request) bool { return r.Header.Get(redirectHeader) == redirectHeaderVal }},
		})
	if server.Error != nil {
		t.Fatalf("configuring server failed: %v", server.Error)
	}
	done := startServer(t, server, source)

	withHeader := http.Header{redirectHeader: []string{redirectHeaderVal}}
	tests := []struct {
		name         string
		path         string
		host         string
		header       http.Header
		wantStatus   int
		wantLocation string
	}{
		{name: "exception is served", path: okPath, header: withHeader, wantStatus: http.StatusOK},
		{name: "matching rule redirects", path: otherPath, header: withHeader, wantStatus: http.StatusMovedPermanently, wantLocation: "http://" + net.JoinHostPort(loopbackHost, strconv.Itoa(int(target))) + redirectTarget},
		{name: "failing condition is served", path: otherPath, header: http.Header{}, wantStatus: http.StatusNotFound},
		{name: "host without port is rejected", path: otherPath, host: "localhost", header: withHeader, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := request(t, source, tt.path, tt.host, tt.header)
			if resp.status != tt.wantStatus {
				t.Fatalf("GET %s status = %d, want %d", tt.path, resp.status, tt.wantStatus)
			}
			if got := resp.header.Get("Location"); got != tt.wantLocation {
				t.Fatalf("GET %s Location = %q, want %q", tt.path, got, tt.wantLocation)
			}
		})
	}

	vary := request(t, source, okPath, "", withHeader).header.Values("Vary")
	want := []string{"Accept", "Origin", "Accept-Encoding"}
	if len(vary) != len(want) {
		t.Fatalf("Vary = %v, want %v", vary, want)
	}
	for i := range want {
		if vary[i] != want[i] {
			t.Fatalf("Vary = %v, want %v", vary, want)
		}
	}

	err := server.Stop()
	if err != nil {
		t.Fatalf("stopping server failed: %v", err)
	}
	waitDone(t, done, "StartAsync")
}

func TestServerBlacklistPlainAddresses(t *testing.T) {
	port := freePort(t)
	server := web.New().WithHost(loopbackHost).WithHTTPPort(port).
		WithHandlerFunc(okPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }).
		SetBlacklist([]string{loopbackHost})
	if server.Error != nil {
		t.Fatalf("configuring server failed: %v", server.Error)
	}
	done := startServer(t, server, port)

	resp := request(t, port, okPath, "", http.Header{})
	if resp.status != http.StatusForbidden {
		t.Fatalf("GET %s status = %d, want %d", okPath, resp.status, http.StatusForbidden)
	}

	err := server.Stop()
	if err != nil {
		t.Fatalf("stopping server failed: %v", err)
	}
	waitDone(t, done, "StartAsync")

	tests := []struct {
		name    string
		entry   string
		wantErr bool
	}{
		{name: "plain IPv4", entry: "192.0.2.1"},
		{name: "plain IPv6", entry: "2001:db8::1"},
		{name: "network", entry: "192.0.2.0/24"},
		{name: "not an address", entry: "not-an-address", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := web.New().SetBlacklist([]string{tt.entry}).Error
			if (got != nil) != tt.wantErr {
				t.Fatalf("SetBlacklist(%q) error = %v, wantErr %v", tt.entry, got, tt.wantErr)
			}
		})
	}
}

func TestServerShutdown(t *testing.T) {
	err := web.New().Shutdown()
	if err != nil {
		t.Fatalf("Shutdown of a server that never started returned error: %v", err)
	}

	port := freePort(t)
	server := web.New().WithHost(loopbackHost).WithHTTPPort(port)
	done := startServer(t, server, port)

	err = server.Shutdown()
	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	waitDone(t, done, "StartAsync")
}

func TestServerRestart(t *testing.T) {
	port := freePort(t)
	server := web.New().WithHost(loopbackHost).WithHTTPPort(port)
	first := startServer(t, server, port)

	restarted := make(chan error, 1)
	go func() { restarted <- server.Restart() }()

	waitDone(t, first, "first StartAsync")
	waitListening(t, port)
	err := server.Stop()
	if err != nil {
		t.Fatalf("stopping restarted server failed: %v", err)
	}
	waitDone(t, restarted, "Restart")
}

func TestServerRestartAsync(t *testing.T) {
	port := freePort(t)
	server := web.New().WithHost(loopbackHost).WithHTTPPort(port)
	first := startServer(t, server, port)

	restarted := make(chan error, 1)
	server.RestartAsync(restarted)

	waitDone(t, first, "first StartAsync")
	waitListening(t, port)
	err := server.Stop()
	if err != nil {
		t.Fatalf("stopping restarted server failed: %v", err)
	}
	waitDone(t, restarted, "RestartAsync")
}
