package jrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valentin-kaiser/go-core/v2/apperror"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "jrpc-client/1.0"

	schemeHTTP  = "http"
	schemeHTTPS = "https"
	schemeWS    = "ws"
	schemeWSS   = "wss"
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
	mutex         *sync.RWMutex
	userAgent     string
	httpClient    *http.Client
	tlsConfig     *tls.Config
	marshalOpts   protojson.MarshalOptions
	unmarshalOpts protojson.UnmarshalOptions
}

// NewClient creates a new JSON-RPC client with default settings.
// Optional ClientOptions can be provided to customize the client's behavior.
func NewClient(opts ...ClientOption) *Client {
	client := &Client{
		mutex: &sync.RWMutex{},
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		userAgent: defaultUserAgent,
		marshalOpts: protojson.MarshalOptions{
			EmitUnpopulated: true,
			UseEnumNumbers:  true,
		},
		unmarshalOpts: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
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
//   - u: The full URL for the request
//   - req: The request message (must be a proto.Message)
//   - resp: The response message (must be a proto.Message pointer)
//   - headers: Optional HTTP headers to include in the request
//
// Returns an error if the request fails, marshaling/unmarshaling fails,
// or the server returns an error response.
func (c *Client) Call(ctx context.Context, u *url.URL, req, resp proto.Message, header http.Header) error {
	if req == nil {
		return apperror.NewError("request cannot be nil")
	}

	if resp == nil {
		return apperror.NewError("response cannot be nil")
	}

	c.mutex.RLock()
	reqBytes, err := c.marshalOpts.Marshal(req)
	c.mutex.RUnlock()
	if err != nil {
		return apperror.NewError("marshaling request failed").AddError(err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(reqBytes))
	if err != nil {
		return apperror.NewError("creating HTTP request failed").AddError(err)
	}

	httpReq.Header.Set(headerContentType, mediaTypeJSON)
	httpReq.Header.Set(headerAccept, mediaTypeJSON)

	for key, values := range header {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	c.mutex.RLock()
	if c.userAgent != "" {
		httpReq.Header.Set(headerUserAgent, c.userAgent)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	c.mutex.RUnlock()
	if err != nil {
		return apperror.NewError("HTTP request failed").AddError(err)
	}
	defer apperror.Catch(func() error { return httpResp.Body.Close() }, "closing response body failed")

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return apperror.NewError("reading response body failed").AddError(err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return apperror.NewErrorf("server returned status %d: %s", httpResp.StatusCode, string(respBytes))
	}

	c.mutex.RLock()
	err = c.unmarshalOpts.Unmarshal(respBytes, resp)
	c.mutex.RUnlock()
	if err != nil {
		return apperror.NewError("unmarshaling response failed").AddError(err)
	}

	return nil
}

// finishClientStream signals the end of the request stream and reads the final response.
func (c *Client) finishClientStream(conn *websocket.Conn, resp proto.Message) error {
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	err := conn.WriteMessage(websocket.CloseMessage, closeMsg)
	if err != nil {
		return apperror.NewError("sending close message failed").AddError(err)
	}
	// The response may arrive together with a close error, which the WebSocket protocol makes expected here.
	err = c.readWSMessage(conn, &resp)
	if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return apperror.NewError("reading response failed").AddError(err)
	}
	return nil
}

// dialWebSocket establishes a WebSocket connection to the service method endpoint.
func (c *Client) dialWebSocket(u *url.URL) (*websocket.Conn, *http.Response, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Create a copy of the URL to avoid mutating the caller's data
	dialURL := *u

	switch dialURL.Scheme {
	case schemeHTTP:
		dialURL.Scheme = schemeWS
	case schemeHTTPS:
		dialURL.Scheme = schemeWSS
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: c.httpClient.Timeout,
		TLSClientConfig:  c.tlsConfig,
	}

	headers := http.Header{}
	if c.userAgent != "" {
		headers.Set(headerUserAgent, c.userAgent)
	}

	conn, resp, err := dialer.Dial(dialURL.String(), headers)
	if err != nil {
		return nil, nil, apperror.NewError("connecting to websocket failed").AddError(err)
	}

	return conn, resp, nil
}

// writeWSMessage writes a proto message to the WebSocket connection.
func (c *Client) writeWSMessage(conn *websocket.Conn, msg proto.Message) error {
	c.mutex.RLock()
	data, err := c.marshalOpts.Marshal(msg)
	c.mutex.RUnlock()
	if err != nil {
		return apperror.NewError("marshaling message failed").AddError(err)
	}
	err = conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return apperror.NewError("writing websocket message failed").AddError(err)
	}
	return nil
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

	c.mutex.RLock()
	err = c.unmarshalOpts.Unmarshal(data, *msg)
	c.mutex.RUnlock()
	if err != nil {
		return apperror.NewError("unmarshaling message failed").AddError(err)
	}
	return nil
}

// ClientOption defines a function type for configuring the Client.
type ClientOption func(*Client)

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

// WithMarshalOptions sets custom protojson marshal options for the client.
// These options control how protocol buffer messages are marshaled to JSON.
func WithMarshalOptions(opts protojson.MarshalOptions) ClientOption {
	return func(c *Client) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		c.marshalOpts = opts
	}
}

// WithUnmarshalOptions sets custom protojson unmarshal options for the client.
// These options control how JSON is unmarshaled into protocol buffer messages.
func WithUnmarshalOptions(opts protojson.UnmarshalOptions) ClientOption {
	return func(c *Client) {
		c.mutex.Lock()
		defer c.mutex.Unlock()
		c.unmarshalOpts = opts
	}
}

// ServerStream makes a server streaming RPC call with typed channels where the client sends one request
// and receives a stream of responses over WebSocket.
//
// Parameters:
//   - c: The client instance
//   - ctx: Context for the request (for cancellation)
//   - u: The full URL for the request
//   - req: The request message
//   - out: Typed channel to receive response messages (will be closed when stream ends)
//   - factory: Factory function to create new response message instances
//
// Returns an error if the connection fails or an error occurs during streaming.
func ServerStream[T proto.Message](c *Client, ctx context.Context, u *url.URL, req proto.Message, out chan<- T, factory func() T) error {
	if u == nil {
		return apperror.NewError("url cannot be nil")
	}

	if out == nil {
		return apperror.NewError("output channel cannot be nil")
	}

	if factory == nil {
		return apperror.NewError("response factory cannot be nil")
	}

	conn, resp, err := c.dialWebSocket(u)
	if err != nil {
		return apperror.NewError("starting server stream failed").AddError(err)
	}
	defer apperror.Catch(func() error { return resp.Body.Close() }, "closing handshake response body failed")
	closeConn := closeOnce(conn)
	defer apperror.Catch(closeConn, "closing websocket connection failed")

	err = c.writeWSMessage(conn, req)
	if err != nil {
		return apperror.NewError("sending request failed").AddError(err)
	}

	errChan := make(chan error, 1)
	go func() {
		defer close(out)
		errChan <- readServerMessages(ctx, c, conn, out, factory)
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		apperror.Catch(closeConn, "closing websocket connection failed")
		return <-errChan
	}
}

// ClientStream makes a client streaming RPC call with typed channels where the client sends a stream
// of requests and receives one response over WebSocket.
//
// Parameters:
//   - c: The client instance
//   - ctx: Context for the request (for cancellation)
//   - u: The full URL for the request
//   - in: Typed channel to send request messages (should be closed by caller when done)
//   - resp: The response message (will be populated when stream completes)
//
// Returns an error if the connection fails or an error occurs during streaming.
func ClientStream[T proto.Message](c *Client, ctx context.Context, u *url.URL, in <-chan T, resp proto.Message) error {
	if u == nil {
		return apperror.NewError("url cannot be nil")
	}

	if in == nil {
		return apperror.NewError("input channel cannot be nil")
	}

	if resp == nil {
		return apperror.NewError("response cannot be nil")
	}

	conn, r, err := c.dialWebSocket(u)
	if err != nil {
		return apperror.NewError("starting client stream failed").AddError(err)
	}
	defer apperror.Catch(func() error { return r.Body.Close() }, "closing handshake response body failed")
	closeConn := closeOnce(conn)
	defer apperror.Catch(closeConn, "closing websocket connection failed")

	errChan := make(chan error, 1)
	go func() {
		err := writeStreamMessages(ctx, c, conn, in)
		if err != nil {
			errChan <- err
			return
		}
		errChan <- c.finishClientStream(conn, resp)
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		apperror.Catch(closeConn, "closing websocket connection failed")
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
//   - u: The full URL for the request
//   - in: Typed channel to send request messages (should be closed by caller when done)
//   - out: Typed channel to receive response messages (will be closed when stream ends)
//   - respFactory: Factory function to create new response message instances
//
// Returns an error if the connection fails or an error occurs during streaming.
func BidirectionalStream[TReq proto.Message, TResp proto.Message](c *Client, ctx context.Context, u *url.URL, in <-chan TReq, out chan<- TResp, respFactory func() TResp) error {
	if u == nil {
		return apperror.NewError("url cannot be nil")
	}

	if in == nil {
		return apperror.NewError("input channel cannot be nil")
	}

	if out == nil {
		return apperror.NewError("output channel cannot be nil")
	}

	if respFactory == nil {
		return apperror.NewError("response factory cannot be nil")
	}

	conn, resp, err := c.dialWebSocket(u)
	if err != nil {
		return apperror.NewError("starting bidirectional stream failed").AddError(err)
	}
	defer apperror.Catch(func() error { return resp.Body.Close() }, "closing handshake response body failed")
	closeConn := closeOnce(conn)
	defer apperror.Catch(closeConn, "closing websocket connection failed")

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

	wg.Go(func() {
		err := writeStreamMessages(ctx, c, conn, in)
		errMutex.Lock()
		writerErr = err
		errMutex.Unlock()
	})

	wg.Go(func() {
		defer close(out)
		err := readServerMessages(ctx, c, conn, out, respFactory)
		errMutex.Lock()
		readerErr = err
		errMutex.Unlock()
	})

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
		apperror.Catch(closeConn, "closing websocket connection failed")
		<-errChan
		return ctx.Err()
	}
}

// readServerMessages forwards the messages the server sends to out until the server closes the connection normally
// (nil), reading fails or ctx is done.
func readServerMessages[T proto.Message](ctx context.Context, c *Client, conn *websocket.Conn, out chan<- T, factory func() T) error {
	for {
		resp := factory()
		var protoResp proto.Message = resp
		err := c.readWSMessage(conn, &protoResp)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return apperror.NewError("reading message failed").AddError(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- resp:
		}
	}
}

// writeStreamMessages sends the messages of in to the server until in is closed, a write fails or ctx is done.
func writeStreamMessages[T proto.Message](ctx context.Context, c *Client, conn *websocket.Conn, in <-chan T) error {
	for msg := range in {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := c.writeWSMessage(conn, msg)
			if err != nil {
				return apperror.NewError("sending message failed").AddError(err)
			}
		}
	}
	return nil
}

// closeOnce wraps c so that closing it repeatedly is safe: the first call closes
// and later calls (e.g. explicit close on cancellation plus a deferred close) are no-ops.
func closeOnce(c io.Closer) func() error {
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() { err = c.Close() })
		return err
	}
}
