package jrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valentin-kaiser/go-core/apperror"
	"google.golang.org/protobuf/proto"
)

// Client provides a client implementation for making JSON-RPC calls over HTTP and WebSocket.
// It handles request serialization, HTTP/WebSocket communication, and response deserialization
// using Protocol Buffer JSON format.
//
// The client supports:
//   - Unary RPC calls (HTTP POST)
//   - Server streaming RPC calls (WebSocket)
//   - Client streaming RPC calls (WebSocket)
//   - Bidirectional streaming RPC calls (WebSocket)
//   - Automatic request/response marshaling
//   - Custom HTTP client configuration
//   - Request timeout management
//   - Custom TLS configuration for secure WebSocket connections
//
// Example unary call:
//
//	client := jrpc.NewClient("http://localhost:8080")
//	req := &MyRequest{Field: "value"}
//	resp := &MyResponse{}
//	err := client.Call(ctx,
//
// Example server streaming call:
//
//	out := make(chan proto.Message, 10)
//	factory := func() proto.Message { return &MyResponse{} }
//	go client.ServerStream(ctx, "MyService", "MyMethod", req, factory, out)
//	for msg := range out {
//	    // Process each response message
//	}
type Client struct {
	mutex      *sync.RWMutex
	userAgent  string
	httpClient *http.Client
	tlsConfig  *tls.Config
}

// ClientOption defines a function type for configuring the Client.
type ClientOption func(*Client)

// ResponseFactory is a function that creates a new instance of a response message.
// This is used in streaming methods to instantiate response messages of the correct type.
//
// Example:
//
//	factory := func() proto.Message { return &MyResponse{} }
type ResponseFactory func() proto.Message

// NewClient creates a new JSON-RPC client with default settings.
// The baseURL parameter specifies the server endpoint (e.g., "http://localhost:8080").
func NewClient(opts ...ClientOption) *Client {
	client := &Client{
		mutex: &sync.RWMutex{},
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "jrpc-client/1.0",
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

// WithClient sets a custom HTTP client.
func WithClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		c.httpClient = client
	}
}

// WithUserAgent sets a custom User-Agent header for HTTP and WebSocket requests.
func WithUserAgent(agent string) ClientOption {
	return func(c *Client) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		c.userAgent = agent
	}
}

// WithTLSConfig sets a custom TLS configuration for WebSocket connections.
// This is useful for connecting to servers with self-signed certificates or
// specific TLS requirements.
//
// Example for skipping certificate verification (use with caution):
//
//	tlsConfig := &tls.Config{InsecureSkipVerify: true}
//	client := jrpc.NewClient("https://localhost:8080", jrpc.WithTLSConfig(tlsConfig))
func WithTLSConfig(config *tls.Config) ClientOption {
	return func(c *Client) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		c.tlsConfig = config
	}
}

// Call makes a unary RPC call to the specified service and method.
// It marshals the request message to Protocol Buffer JSON format,
// sends it via HTTP POST, and unmarshals the response.
//
// URL format: {baseURL}/{service}/{method}
// Content-Type: application/json (Protocol Buffer JSON format)
//
// Parameters:
//   - ctx: Context for the request (for cancellation and timeouts)
//   - url: The full URL for the request
//   - req: The request message (must be a proto.Message)
//   - resp: The response message (must be a proto.Message pointer)
//   - headers: Optional HTTP headers to include in the request
//
// Returns an error if the request fails, marshaling/unmarshaling fails,
// or the server returns an error response.
func (c *Client) Call(ctx context.Context, url url.URL, req, resp proto.Message, headers []http.Header) error {
	if url.String() == "" {
		return apperror.NewError("URL cannot be empty")
	}

	if req == nil {
		return apperror.NewError("request cannot be nil")
	}

	if resp == nil {
		return apperror.NewError("response cannot be nil")
	}

	// Marshal request to JSON
	reqBytes, err := marshalOpts.Marshal(req)
	if err != nil {
		return apperror.NewError("failed to marshal request").AddError(err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url.String(), bytes.NewReader(reqBytes))
	if err != nil {
		return apperror.NewError("failed to create HTTP request").AddError(err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Add custom headers if provided
	for _, header := range headers {
		for key, values := range header {
			for _, value := range values {
				httpReq.Header.Add(key, value)
			}
		}
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.userAgent != "" {
		httpReq.Header.Set("User-Agent", c.userAgent)
	}

	// Make HTTP request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return apperror.NewError("HTTP request failed").AddError(err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return apperror.NewError("failed to read response body").AddError(err)
	}

	// Check HTTP status
	if httpResp.StatusCode != http.StatusOK {
		return apperror.NewError(fmt.Sprintf("server returned status %d: %s", httpResp.StatusCode, string(respBytes)))
	}

	// Unmarshal response
	err = unmarshalOpts.Unmarshal(respBytes, resp)
	if err != nil {
		return apperror.NewError("failed to unmarshal response").AddError(err)
	}

	return nil
}

// ServerStream makes a server streaming RPC call where the client sends one request
// and receives a stream of responses over WebSocket.
//
// Parameters:
//   - ctx: Context for the request (for cancellation)
//   - url: The full URL for the request
//   - req: The request message
//   - factory: Factory function to create new response message instances
//   - out: Channel to receive response messages (will be closed when stream ends)
//
// Returns an error if the connection fails or an error occurs during streaming.
func (c *Client) ServerStream(ctx context.Context, url url.URL, req proto.Message, factory ResponseFactory, out chan proto.Message) error {
	if url.String() == "" {
		return apperror.NewError("URL cannot be empty")
	}

	if req == nil {
		return apperror.NewError("request cannot be nil")
	}

	if factory == nil {
		return apperror.NewError("response factory cannot be nil")
	}

	if out == nil {
		return apperror.NewError("output channel cannot be nil")
	}

	conn, err := c.dialWebSocket(url)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send initial request
	err = c.writeWSMessage(conn, req)
	if err != nil {
		return apperror.NewError("failed to send request").AddError(err)
	}

	// Read responses until stream ends
	errChan := make(chan error, 1)
	go func() {
		defer close(out)
		for {
			// Create a new instance of the response message using the factory
			msg := factory()
			if err := c.readWSMessage(conn, &msg); err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					errChan <- nil
					return
				}
				errChan <- err
				return
			}

			select {
			case out <- msg:
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			}
		}
	}()

	// Monitor context cancellation and close connection immediately
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		conn.Close()     // Immediately close connection to interrupt blocking read
		return <-errChan // Wait for goroutine to finish
	}
}

// ClientStream makes a client streaming RPC call where the client sends a stream
// of requests and receives one response over WebSocket.
//
// Parameters:
//   - ctx: Context for the request (for cancellation)
//   - url: The full URL for the request
//   - in: Channel to send request messages (should be closed by caller when done)
//   - resp: The response message (will be populated when stream completes)
//
// Returns an error if the connection fails or an error occurs during streaming.
func (c *Client) ClientStream(ctx context.Context, url url.URL, in chan proto.Message, resp proto.Message) error {
	if url.String() == "" {
		return apperror.NewError("URL cannot be empty")
	}

	if in == nil {
		return apperror.NewError("input channel cannot be nil")
	}

	if resp == nil {
		return apperror.NewError("response cannot be nil")
	}

	conn, err := c.dialWebSocket(url)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Start goroutine to send messages from input channel
	var wg sync.WaitGroup
	var sendErr error
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				sendErr = ctx.Err()
				return
			case msg, ok := <-in:
				if !ok {
					// Input channel closed, signal end of stream
					err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					if err != nil {
						sendErr = apperror.NewError("failed to close stream").AddError(err)
					}
					return
				}

				err = c.writeWSMessage(conn, msg)
				if err != nil {
					sendErr = apperror.NewError("failed to send message").AddError(err)
					return
				}
			}
		}
	}()

	// Wait for sending to complete, then read final response
	wg.Wait()

	// If there was an error during sending, return it
	if sendErr != nil {
		return sendErr
	}

	// Read the final response
	err = c.readWSMessage(conn, &resp)
	if err != nil {
		return apperror.NewError("failed to read response").AddError(err)
	}

	return nil
}

// BidirectionalStream makes a bidirectional streaming RPC call where both client
// and server send streams of messages over WebSocket.
//
// Parameters:
//   - ctx: Context for the request (for cancellation)
//   - service: The service name
//   - method: The method name
//   - in: Channel to send request messages (should be closed by caller when done)
//   - responseFactory: Factory function to create new response message instances
//   - out: Channel to receive response messages (will be closed when stream ends)
//
// Returns an error if the connection fails or an error occurs during streaming.
func (c *Client) BidirectionalStream(ctx context.Context, url url.URL, in chan proto.Message, responseFactory ResponseFactory, out chan proto.Message) error {
	if url.String() == "" {
		return apperror.NewError("URL cannot be empty")
	}

	if in == nil {
		return apperror.NewError("input channel cannot be nil")
	}

	if responseFactory == nil {
		return apperror.NewError("response factory cannot be nil")
	}

	if out == nil {
		return apperror.NewError("output channel cannot be nil")
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	conn, err := c.dialWebSocket(url)
	if err != nil {
		return err
	}
	defer conn.Close()

	errChan := make(chan error, 1)
	var wg sync.WaitGroup
	var writerErr, readerErr error
	var errOnce sync.Once

	// Helper to set error once and signal completion
	setError := func(err error) {
		errOnce.Do(func() {
			if err != nil {
				errChan <- err
			}
		})
	}

	// Writer goroutine - sends messages from input channel
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				writerErr = ctx.Err()
				return
			case msg, ok := <-in:
				if !ok {
					// Input channel closed
					return
				}

				err = c.writeWSMessage(conn, msg)
				if err != nil {
					writerErr = apperror.NewError("failed to send message").AddError(err)
					return
				}
			}
		}
	}()

	// Reader goroutine - receives messages and sends to output channel
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(out)
		for {
			// Create a new instance of the response message using the factory
			msg := responseFactory()
			err = c.readWSMessage(conn, &msg)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					// Normal closure, not an error
					return
				}
				readerErr = err
				return
			}

			select {
			case out <- msg:
			case <-ctx.Done():
				readerErr = ctx.Err()
				return
			}
		}
	}()

	// Wait for both goroutines in a separate goroutine
	go func() {
		wg.Wait()
		// Both goroutines completed, determine final error
		if writerErr != nil {
			setError(writerErr)
		} else if readerErr != nil {
			setError(readerErr)
		} else {
			setError(nil)
		}
	}()

	// Monitor context and close connection on cancellation
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		conn.Close() // Force close to interrupt blocking operations
		<-errChan    // Wait for cleanup
		return ctx.Err()
	}
}

// dialWebSocket establishes a WebSocket connection to the service method endpoint.
func (c *Client) dialWebSocket(url url.URL) (*websocket.Conn, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Convert HTTP(S) scheme to WS(S)
	switch url.Scheme {
	case "http":
		url.Scheme = "ws"
	case "https":
		url.Scheme = "wss"
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: c.httpClient.Timeout,
		TLSClientConfig:  c.tlsConfig,
	}

	// Set User-Agent header for WebSocket handshake
	headers := http.Header{}
	if c.userAgent != "" {
		headers.Set("User-Agent", c.userAgent)
	}

	conn, _, err := dialer.Dial(url.String(), headers)
	if err != nil {
		return nil, apperror.NewError("failed to connect to WebSocket").AddError(err)
	}

	return conn, nil
}

// writeWSMessage writes a proto message to the WebSocket connection.
func (c *Client) writeWSMessage(conn *websocket.Conn, msg proto.Message) error {
	data, err := marshalOpts.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// readWSMessage reads a proto message from the WebSocket connection.
func (c *Client) readWSMessage(conn *websocket.Conn, msg *proto.Message) error {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	// If msg is a pointer to nil, we can't unmarshal
	// The generated code will need to provide the correct type
	if msg == nil || *msg == nil {
		return apperror.NewError("message pointer cannot be nil")
	}

	return unmarshalOpts.Unmarshal(data, *msg)
}
