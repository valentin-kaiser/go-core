package jrpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestNewClient verifies that a new client can be created with default settings
func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:8080")

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if client.BaseURL != "http://localhost:8080" {
		t.Errorf("expected baseURL to be http://localhost:8080, got %s", client.BaseURL)
	}

	if client.httpClient == nil {
		t.Fatal("expected non-nil httpClient")
	}

	if client.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected default timeout of 30s, got %v", client.httpClient.Timeout)
	}
}

// TestClientWithTimeout verifies that custom timeout option works
func TestClientWithTimeout(t *testing.T) {
	customTimeout := 5 * time.Second
	client := NewClient("http://localhost:8080", WithTimeout(customTimeout))

	if client.httpClient.Timeout != customTimeout {
		t.Errorf("expected timeout of %v, got %v", customTimeout, client.httpClient.Timeout)
	}
}

// TestClientWithHTTPClient verifies that custom HTTP client option works
func TestClientWithHTTPClient(t *testing.T) {
	customClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	client := NewClient("http://localhost:8080", WithClient(customClient))

	if client.httpClient != customClient {
		t.Error("expected custom HTTP client to be used")
	}
}

// TestClientCall verifies that the Call method makes correct HTTP requests
func TestClientCall(t *testing.T) {
	// Create a test server that returns a valid response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method
		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}

		// Verify URL path
		expectedPath := "/TestService/TestMethod"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %s, got %s", expectedPath, r.URL.Path)
		}

		// Verify headers
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept application/json, got %s", r.Header.Get("Accept"))
		}

		// Return a valid empty response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	// Create client pointing to test server
	client := NewClient(server.URL)

	// Make a call
	req := &emptypb.Empty{}
	resp := &emptypb.Empty{}

	err := client.Call(context.Background(), "TestService", "TestMethod", req, resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClientCallNilRequest verifies that nil request returns an error
func TestClientCallNilRequest(t *testing.T) {
	client := NewClient("http://localhost:8080")

	resp := &emptypb.Empty{}
	err := client.Call(context.Background(), "TestService", "TestMethod", nil, resp)

	if err == nil {
		t.Fatal("expected error for nil request")
	}

	if err.Error() != "request cannot be nil" {
		t.Errorf("expected 'request cannot be nil' error, got %v", err)
	}
}

// TestClientCallNilResponse verifies that nil response returns an error
func TestClientCallNilResponse(t *testing.T) {
	client := NewClient("http://localhost:8080")

	req := &emptypb.Empty{}
	err := client.Call(context.Background(), "TestService", "TestMethod", req, nil)

	if err == nil {
		t.Fatal("expected error for nil response")
	}

	if err.Error() != "response cannot be nil" {
		t.Errorf("expected 'response cannot be nil' error, got %v", err)
	}
}

// TestClientCallServerError verifies that server errors are handled correctly
func TestClientCallServerError(t *testing.T) {
	// Create a test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client := NewClient(server.URL)

	req := &emptypb.Empty{}
	resp := &emptypb.Empty{}

	err := client.Call(context.Background(), "TestService", "TestMethod", req, resp)
	if err == nil {
		t.Fatal("expected error for server error response")
	}

	// Error should contain status code
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

// TestClientCallContextCancellation verifies that context cancellation works
func TestClientCallContextCancellation(t *testing.T) {
	// Create a test server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := NewClient(server.URL)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := &emptypb.Empty{}
	resp := &emptypb.Empty{}

	err := client.Call(ctx, "TestService", "TestMethod", req, resp)
	if err == nil {
		t.Fatal("expected error for context timeout")
	}
}

// TestClientSetGetBaseURL verifies that base URL can be updated and retrieved
func TestClientSetGetBaseURL(t *testing.T) {
	client := NewClient("http://localhost:8080")

	if client.BaseURL != "http://localhost:8080" {
		t.Errorf("expected initial URL http://localhost:8080, got %s", client.BaseURL)
	}

	newURL := "http://localhost:9090"
	client.BaseURL = newURL

	if client.BaseURL != newURL {
		t.Errorf("expected updated URL %s, got %s", newURL, client.BaseURL)
	}
}

// TestClientCallInvalidJSON verifies that invalid JSON responses are handled
func TestClientCallInvalidJSON(t *testing.T) {
	// Create a test server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	client := NewClient(server.URL)

	req := &emptypb.Empty{}
	resp := &emptypb.Empty{}

	err := client.Call(context.Background(), "TestService", "TestMethod", req, resp)
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

// BenchmarkClientCall benchmarks the client call performance
func BenchmarkClientCall(b *testing.B) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	req := &emptypb.Empty{}
	resp := &emptypb.Empty{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = client.Call(context.Background(), "TestService", "TestMethod", req, resp)
	}
}

// TestClientServerStreamNilRequest verifies that nil request returns an error
func TestClientServerStreamNilRequest(t *testing.T) {
	client := NewClient("http://localhost:8080")
	out := make(chan proto.Message, 1)

	err := client.ServerStream(context.Background(), "TestService", "TestMethod", nil, out)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// TestClientServerStreamNilChannel verifies that nil channel returns an error
func TestClientServerStreamNilChannel(t *testing.T) {
	client := NewClient("http://localhost:8080")
	req := &emptypb.Empty{}

	err := client.ServerStream(context.Background(), "TestService", "TestMethod", req, nil)
	if err == nil {
		t.Fatal("expected error for nil output channel")
	}
}

// TestClientClientStreamNilChannel verifies that nil channel returns an error
func TestClientClientStreamNilChannel(t *testing.T) {
	client := NewClient("http://localhost:8080")
	resp := &emptypb.Empty{}

	err := client.ClientStream(context.Background(), "TestService", "TestMethod", nil, resp)
	if err == nil {
		t.Fatal("expected error for nil input channel")
	}
}

// TestClientClientStreamNilResponse verifies that nil response returns an error
func TestClientClientStreamNilResponse(t *testing.T) {
	client := NewClient("http://localhost:8080")
	in := make(chan proto.Message, 1)

	err := client.ClientStream(context.Background(), "TestService", "TestMethod", in, nil)
	if err == nil {
		t.Fatal("expected error for nil response")
	}
}

// TestClientBidirectionalStreamNilChannels verifies that nil channels return errors
func TestClientBidirectionalStreamNilChannels(t *testing.T) {
	client := NewClient("http://localhost:8080")

	// Test nil input channel
	out := make(chan proto.Message, 1)
	err := client.BidirectionalStream(context.Background(), "TestService", "TestMethod", nil, out)
	if err == nil {
		t.Fatal("expected error for nil input channel")
	}

	// Test nil output channel
	in := make(chan proto.Message, 1)
	err = client.BidirectionalStream(context.Background(), "TestService", "TestMethod", in, nil)
	if err == nil {
		t.Fatal("expected error for nil output channel")
	}
}
