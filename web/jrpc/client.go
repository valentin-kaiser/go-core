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
//	client := jrpc.NewClient()
//	req := &MyRequest{Field: "value"}
//	resp := &MyResponse{}
//	err := client.Call(ctx, url.URL{Scheme: "http", Host: "localhost:8080", Path: "/MyService/MyMethod"}, req, resp, nil)
//
// Example server streaming call:
//
//	out := make(chan proto.Message, 10)
//	factory := func() proto.Message { return &MyResponse{} }
//	go client.ServerStream(ctx, url.URL{Scheme: "http", Host: "localhost:8080", Path: "/MyService/MyMethod"}, req, factory, out)
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

// NewClient creates a new JSON-RPC client with default settings.
// Optional ClientOptions can be provided to customize the client's behavior.
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
func (c *Client) Call(ctx context.Context, url url.URL, req, resp proto.Message, header http.Header) error {
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
	for key, values := range header {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	c.mutex.RLock()
	if c.userAgent != "" {
		httpReq.Header.Set("User-Agent", c.userAgent)
	}

	// Make HTTP request
	httpResp, err := c.httpClient.Do(httpReq)
	c.mutex.RUnlock()
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

// ServerStream makes a server streaming RPC call with typed channels where the client sends one request
// and receives a stream of responses over WebSocket.
//
// Parameters:
//   - c: The client instance
//   - ctx: Context for the request (for cancellation)
//   - url: The full URL for the request
//   - req: The request message
//   - out: Typed channel to receive response messages (will be closed when stream ends)
//   - factory: Factory function to create new response message instances
//
// Returns an error if the connection fails or an error occurs during streaming.
func ServerStream[T proto.Message](c *Client, ctx context.Context, url url.URL, req proto.Message, out chan T, factory func() T) error {
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

	errChan := make(chan error, 1)
	go func() {
		defer close(out)
		for {
			resp := factory()
			var protoResp proto.Message = resp
			err := c.readWSMessage(conn, &protoResp)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					errChan <- nil
					return
				}
				errChan <- apperror.NewError("failed to read message").AddError(err)
				return
			}
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			case out <- resp:
			}
		}
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		conn.Close()
		return <-errChan
	}
}

// ClientStream makes a client streaming RPC call with typed channels where the client sends a stream
// of requests and receives one response over WebSocket.
//
// Parameters:
//   - c: The client instance
//   - ctx: Context for the request (for cancellation)
//   - url: The full URL for the request
//   - in: Typed channel to send request messages (should be closed by caller when done)
//   - resp: The response message (will be populated when stream completes)
//
// Returns an error if the connection fails or an error occurs during streaming.
func ClientStream[T proto.Message](c *Client, ctx context.Context, url url.URL, in chan T, resp proto.Message) error {
	conn, err := c.dialWebSocket(url)
	if err != nil {
		return err
	}
	defer conn.Close()

	errChan := make(chan error, 1)
	go func() {
		for msg := range in {
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			default:
				err := c.writeWSMessage(conn, msg)
				if err != nil {
					errChan <- apperror.NewError("failed to send message").AddError(err)
					return
				}
			}
		}
		// Input stream closed, read final response
		err := c.readWSMessage(conn, &resp)
		if err != nil {
			errChan <- apperror.NewError("failed to read response").AddError(err)
			return
		}
		errChan <- nil
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		conn.Close()
		<-errChan
		return ctx.Err()
	}
}

// BidirectionalStream makes a bidirectional streaming RPC call with typed channels where both client
// and server send streams of messages over WebSocket.
//
// Parameters:
//   - c: The client instance
//   - ctx: Context for the request (for cancellation)
//   - url: The full URL for the request
//   - in: Typed channel to send request messages (should be closed by caller when done)
//   - out: Typed channel to receive response messages (will be closed when stream ends)
//   - respFactory: Factory function to create new response message instances
//
// Returns an error if the connection fails or an error occurs during streaming.
func BidirectionalStream[TReq proto.Message, TResp proto.Message](c *Client, ctx context.Context, url url.URL, in chan TReq, out chan TResp, respFactory func() TResp) error {
	conn, err := c.dialWebSocket(url)
	if err != nil {
		return err
	}
	defer conn.Close()

	errChan := make(chan error, 1)
	var wg sync.WaitGroup
	var writerErr, readerErr error
	var errMutex sync.Mutex
	var errOnce sync.Once

	setError := func(err error) {
		errOnce.Do(func() {
			errChan <- err
		})
	}

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range in {
			select {
			case <-ctx.Done():
				errMutex.Lock()
				writerErr = ctx.Err()
				errMutex.Unlock()
				return
			default:
				err := c.writeWSMessage(conn, msg)
				if err != nil {
					errMutex.Lock()
					writerErr = apperror.NewError("failed to send message").AddError(err)
					errMutex.Unlock()
					return
				}
			}
		}
	}()

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(out)
		for {
			resp := respFactory()
			var protoResp proto.Message = resp
			err := c.readWSMessage(conn, &protoResp)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return
				}
				errMutex.Lock()
				readerErr = apperror.NewError("failed to read message").AddError(err)
				errMutex.Unlock()
				return
			}
			select {
			case <-ctx.Done():
				errMutex.Lock()
				readerErr = ctx.Err()
				errMutex.Unlock()
				return
			case out <- resp:
			}
		}
	}()

	go func() {
		wg.Wait()
		errMutex.Lock()
		var finalErr error
		if writerErr != nil {
			finalErr = writerErr
		} else if readerErr != nil {
			finalErr = readerErr
		}
		errMutex.Unlock()
		setError(finalErr)
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		conn.Close()
		<-errChan
		return ctx.Err()
	}
}

// dialWebSocket establishes a WebSocket connection to the service method endpoint.
func (c *Client) dialWebSocket(u url.URL) (*websocket.Conn, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Create a copy of the URL to avoid mutating the caller's data
	dialURL := u

	// Convert HTTP(S) scheme to WS(S)
	switch dialURL.Scheme {
	case "http":
		dialURL.Scheme = "ws"
	case "https":
		dialURL.Scheme = "wss"
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

	conn, _, err := dialer.Dial(dialURL.String(), headers)
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



