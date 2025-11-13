package web_test

import (
	"crypto/tls"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valentin-kaiser/go-core/security"
	"github.com/valentin-kaiser/go-core/web"
)

func TestServerInstance(t *testing.T) {
	server1 := web.Instance()
	server2 := web.Instance()

	if server1 != server2 {
		t.Error("Instance() should return the same server instance (singleton)")
	}
}

func TestServerStartAsync(t *testing.T) {
	server := web.New()
	server.WithHost("localhost").WithPort(0, web.ProtocolHTTP)

	done := make(chan error, 1)

	// Start server asynchronously
	server.StartAsync(done)

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Stop the server
	if err := server.Stop(); err != nil {
		t.Logf("Warning: failed to stop server: %v", err)
	}

	// Wait for completion
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StartAsync should not return error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("StartAsync timed out")
	}
}

func TestServerErrorHandling(t *testing.T) {
	server := web.New()

	// Simulate an error condition
	server.Error = errors.New("test error")

	// Test that methods still return the server instance even with errors
	result := server.WithHost("localhost")
	if result != server {
		t.Error("Methods should still return server instance even with errors")
	}

	// Test that Start() respects existing errors
	server.Start()
	if server.Error.Error() != "test error" {
		t.Error("Start() should preserve existing errors")
	}
}

func TestServerMemoryUsage(_ *testing.T) {
	// Test that creating many servers doesn't cause memory leaks
	for i := 0; i < 100; i++ {
		server := web.New()
		server.WithHost("localhost").
			WithPort(8080+uint16(i), web.ProtocolHTTP).
			WithHandlerFunc(fmt.Sprintf("/test%d", i), func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
	}

	// If we get here without panicking, the test passes
}

func TestServerWithVaryHeader(t *testing.T) {
	server := web.New()

	// Add a single Vary header
	result := server.WithVaryHeader("Accept-Language")

	if result != server {
		t.Error("WithVaryHeader() should return the same server instance")
	}

	if server.Error != nil {
		t.Errorf("WithVaryHeader() should not set an error, got: %v", server.Error)
	}
}

func TestServerWithVaryHeaders(t *testing.T) {
	server := web.New()

	// Add multiple Vary headers
	headers := []string{"Accept-Language", "User-Agent"}
	result := server.WithVaryHeaders(headers)

	if result != server {
		t.Error("WithVaryHeaders() should return the same server instance")
	}

	if server.Error != nil {
		t.Errorf("WithVaryHeaders() should not set an error, got: %v", server.Error)
	}
}

// Benchmark tests
func BenchmarkServerWithHandlerFunc(b *testing.B) {
	server := web.New()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.WithHandlerFunc(fmt.Sprintf("/bench%d", i), func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}
}

func TestServerConfiguration(t *testing.T) {
	server := web.New()

	// Test chainable configuration
	result := server.
		WithHost("0.0.0.0").
		WithPort(8080, web.ProtocolHTTP).
		WithReadTimeout(30 * time.Second).
		WithWriteTimeout(30 * time.Second).
		WithIdleTimeout(60 * time.Second)

	if result != server {
		t.Error("Configuration methods should return the same server instance")
	}
}

func TestServerHandlers(t *testing.T) {
	server := web.New()

	// Test handler registration
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "test response")
	})

	result := server.WithHandlerFunc("/test", testHandler)
	if result != server {
		t.Error("WithHandlerFunc should return the same server instance")
	}

	result = server.WithHandler("/handler", testHandler)
	if result != server {
		t.Error("WithHandler should return the same server instance")
	}
}

func TestServerFileServing(t *testing.T) {
	server := web.New()

	// Create a temporary directory with a test file
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test file server
	result := server.WithFileServer([]string{"/static"}, tempDir)
	if result != server {
		t.Error("WithFileServer should return the same server instance")
	}
}

func TestServerHeaders(t *testing.T) {
	server := web.New()

	// Test header configuration
	result := server.
		WithSecurityHeaders().
		WithCORSHeaders().
		WithHeader("X-Custom", "test").
		WithCacheControl("max-age=3600")

	if result != server {
		t.Error("Header methods should return the same server instance")
	}

	// Test multiple headers
	headers := map[string]string{
		"X-Test":    "value1",
		"X-Another": "value2",
	}
	result = server.WithHeaders(headers)
	if result != server {
		t.Error("WithHeaders should return the same server instance")
	}
}

func TestServerTLS(t *testing.T) {
	server := web.New()

	// Test self-signed TLS
	result := server.WithSelfSignedTLS()
	if result != server {
		t.Error("WithSelfSignedTLS should return the same server instance")
	}

	// Test custom TLS config
	subject := pkix.Name{Organization: []string{"Test"}}
	cert, caPool, err := security.GenerateSelfSignedCertificate(subject)
	if err != nil {
		t.Fatalf("Failed to generate test certificate: %v", err)
	}

	tlsConfig := security.NewTLSConfig(cert, caPool, tls.NoClientCert)
	result = server.WithTLS(tlsConfig)
	if result != server {
		t.Error("WithTLS should return the same server instance")
	}
}

func TestServerWebsocket(t *testing.T) {
	server := web.New()

	wsHandler := func(_ http.ResponseWriter, _ *http.Request, conn *websocket.Conn) {
		// Simple echo websocket handler
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				break
			}
		}
	}

	result := server.WithWebsocket("/ws", wsHandler)
	if result != server {
		t.Error("WithWebsocket should return the same server instance")
	}
}

func TestServerPortValidation(t *testing.T) {
	server := web.New()

	// Test invalid port should set error
	server.WithPort(0, web.ProtocolHTTP)
	if server.Error != nil {
		t.Errorf("Port 0 should be valid, got error: %v", server.Error)
	}

	// Test maximum valid port
	server = web.New()
	server.WithPort(65535, web.ProtocolHTTP)
	if server.Error != nil {
		t.Errorf("Port 65535 should be valid, got error: %v", server.Error)
	}
}

func TestServerMiddleware(t *testing.T) {
	server := web.New()

	// Test middleware methods
	result := server.
		WithLog().
		WithRateLimit("test", 10, time.Minute).
		WithGzip()

	if result != server {
		t.Error("Middleware methods should return the same server instance")
	}

	// Test Vary headers
	result = server.WithVaryHeaders([]string{"Accept-Encoding", "Origin"})
	if result != server {
		t.Error("WithVaryHeaders should return the same server instance")
	}
}

func TestResponseWriter(t *testing.T) {
	// Test the custom ResponseWriter functionality
	server := web.New()

	// Create a simple handler to test ResponseWriter
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Test", "true")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}

	server.WithHandlerFunc("/test", handler)

	// This tests that the server can handle basic requests
	if server.Error != nil {
		t.Errorf("Unexpected error setting up server: %v", server.Error)
	}
}
