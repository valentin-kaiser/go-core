package xrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valentin-kaiser/go-core/apperror"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// Client calls xrpc services over JSON-RPC 2.0, XML or gRPC.
//
// Methods are named "pkg.Service/Method". JSON and XML calls use HTTP POST on the
// endpoint and WebSocket for streams; gRPC calls use the connection given with WithGRPCConn.
type Client struct {
	endpoint  *url.URL
	protocol  Protocol
	http      *http.Client
	tls       *tls.Config
	userAgent string
	header    http.Header
	codec     codec
	conn      *grpc.ClientConn
	nextID    atomic.Uint64
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithProtocol selects the JSON or XML encoding. The default is JSON.
func WithProtocol(p Protocol) ClientOption { return func(c *Client) { c.protocol = p } }

// WithHTTPClient sets the HTTP client used for unary JSON and XML calls.
func WithHTTPClient(h *http.Client) ClientOption { return func(c *Client) { c.http = h } }

// WithUserAgent sets the User-Agent header of HTTP and WebSocket requests.
func WithUserAgent(agent string) ClientOption { return func(c *Client) { c.userAgent = agent } }

// WithHeader adds headers to every HTTP and WebSocket request, or metadata to gRPC calls.
func WithHeader(h http.Header) ClientOption { return func(c *Client) { c.header = h.Clone() } }

// WithTLSConfig sets the TLS configuration for WebSocket connections.
func WithTLSConfig(cfg *tls.Config) ClientOption { return func(c *Client) { c.tls = cfg } }

// WithClientFieldNames makes the client send field names instead of field numbers.
func WithClientFieldNames() ClientOption { return func(c *Client) { c.codec.names = true } }

// WithGRPCConn makes the client use gRPC over conn; the endpoint is ignored.
func WithGRPCConn(conn *grpc.ClientConn) ClientOption {
	return func(c *Client) {
		c.conn = conn
		c.protocol = ProtocolGRPC
	}
}

// NewClient creates a client for the given endpoint URL, e.g. "http://localhost:8080/rpc".
func NewClient(endpoint string, opts ...ClientOption) (*Client, error) {
	c := &Client{
		protocol:  ProtocolJSON,
		http:      &http.Client{Timeout: 30 * time.Second},
		userAgent: "xrpc-client/1.0",
	}
	for _, opt := range opts {
		opt(c)
	}

	switch c.protocol {
	case ProtocolGRPC:
		if c.conn == nil {
			return nil, apperror.NewError("gRPC protocol requires WithGRPCConn")
		}
	case ProtocolJSON, ProtocolXML:
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, apperror.NewError("invalid endpoint").AddError(err)
		}
		if u.Host == "" {
			return nil, apperror.NewError("invalid endpoint: missing host")
		}
		c.endpoint = u
	default:
		return nil, apperror.NewErrorf("unsupported protocol %q", c.protocol)
	}
	return c, nil
}

func grpcPath(method string) string {
	if strings.Contains(method, "/") {
		return "/" + strings.TrimPrefix(method, "/")
	}
	if i := strings.LastIndexByte(method, '.'); i >= 0 {
		return "/" + method[:i] + "/" + method[i+1:]
	}
	return "/" + method
}

func (c *Client) grpcContext(ctx context.Context) context.Context {
	for k, vs := range c.header {
		for _, v := range vs {
			ctx = metadata.AppendToOutgoingContext(ctx, strings.ToLower(k), v)
		}
	}
	return ctx
}

// Call performs a unary call.
func (c *Client) Call(ctx context.Context, method string, in, out proto.Message) error {
	if in == nil || out == nil {
		return apperror.NewError("request and response must not be nil")
	}
	if c.protocol == ProtocolGRPC {
		return c.conn.Invoke(c.grpcContext(ctx), grpcPath(method), in, out)
	}

	w := newWire(c.protocol, c.codec)
	body, err := w.encodeRequest(numericID(c.nextID.Add(1)), method, in)
	if err != nil {
		return apperror.NewError("failed to encode request").AddError(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", w.contentType())
	req.Header.Set("Accept", w.contentType())
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return apperror.NewError("request failed").AddError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, defaultMaxBody))
	if err != nil {
		return apperror.NewError("failed to read response").AddError(err)
	}

	envs, _, perr := w.parse(data)
	if perr != nil || len(envs) != 1 || !envs[0].isResp {
		return apperror.NewErrorf("unexpected response (HTTP %d): %s", resp.StatusCode, truncate(data, 200))
	}
	if envs[0].err != nil {
		return envs[0].err
	}
	return envs[0].bind(out)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return strings.TrimSpace(string(b))
}

type nextFunc func(context.Context) (proto.Message, bool)

// stream runs a streaming call. first is the request of a server stream, next
// supplies the messages of client and bidirectional streams, deliver receives
// the server messages and result receives the response of a client stream.
func (c *Client) stream(ctx context.Context, method string, k kind, first proto.Message, next nextFunc, newOut func() proto.Message, deliver func(proto.Message) error, result proto.Message) error {
	if c.protocol == ProtocolGRPC {
		return c.streamGRPC(ctx, method, k, first, next, newOut, deliver, result)
	}
	return c.streamWS(ctx, method, first, next, newOut, deliver, result)
}

func (c *Client) streamGRPC(ctx context.Context, method string, k kind, first proto.Message, next nextFunc, newOut func() proto.Message, deliver func(proto.Message) error, result proto.Message) error {
	ctx, cancel := context.WithCancel(c.grpcContext(ctx))
	defer cancel()

	desc := &grpc.StreamDesc{
		ServerStreams: k == kindServerStream || k == kindBidi,
		ClientStreams: k == kindClientStream || k == kindBidi,
	}
	cs, err := c.conn.NewStream(ctx, desc, grpcPath(method))
	if err != nil {
		return err
	}

	if first != nil {
		if err := cs.SendMsg(first); err != nil {
			return err
		}
		if err := cs.CloseSend(); err != nil {
			return err
		}
	}
	if next != nil {
		go func() {
			for {
				msg, ok := next(ctx)
				if !ok {
					_ = cs.CloseSend()
					return
				}
				if err := cs.SendMsg(msg); err != nil {
					return // the error surfaces on RecvMsg
				}
			}
		}()
	}

	if k == kindClientStream {
		return cs.RecvMsg(result)
	}
	for {
		msg := newOut()
		if err := cs.RecvMsg(msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := deliver(msg); err != nil {
			return err
		}
	}
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, wire, error) {
	u := *c.endpoint
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}

	sub := subprotocolJSON
	if c.protocol == ProtocolXML {
		sub = subprotocolXML
	}
	tlsCfg := c.tls
	if tlsCfg == nil {
		if t, ok := c.http.Transport.(*http.Transport); ok {
			tlsCfg = t.TLSClientConfig
		}
	}
	d := websocket.Dialer{
		Subprotocols:     []string{sub},
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: 30 * time.Second,
		Proxy:            http.ProxyFromEnvironment,
	}

	header := c.header.Clone()
	if header == nil {
		header = make(http.Header)
	}
	if c.userAgent != "" {
		header.Set("User-Agent", c.userAgent)
	}

	conn, _, err := d.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, nil, apperror.NewError("websocket dial failed").AddError(err)
	}
	return conn, newWire(c.protocol, c.codec), nil
}

func (c *Client) streamWS(ctx context.Context, method string, first proto.Message, next nextFunc, newOut func() proto.Message, deliver func(proto.Message) error, result proto.Message) error {
	conn, w, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()

	var wmu sync.Mutex
	write := func(data []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return conn.WriteMessage(websocket.TextMessage, data)
	}

	id := numericID(c.nextID.Add(1))
	req, err := w.encodeRequest(id, method, first)
	if err != nil {
		return apperror.NewError("failed to encode request").AddError(err)
	}
	if err := write(req); err != nil {
		return err
	}

	sendErr := make(chan error, 1)
	if next != nil {
		go func() {
			for {
				msg, ok := next(ctx)
				if !ok {
					if ctx.Err() == nil {
						if data, err := w.encodeEvent(methodClose, id, nil); err == nil {
							_ = write(data)
						}
					}
					return
				}
				data, err := w.encodeEvent(methodMessage, id, msg)
				if err != nil {
					sendErr <- apperror.NewError("failed to encode message").AddError(err)
					cancel()
					return
				}
				if err := write(data); err != nil {
					return // the error surfaces on read
				}
			}
		}()
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case e := <-sendErr:
				return e
			default:
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}

		envs, _, perr := w.parse(data)
		if perr != nil || len(envs) != 1 {
			return apperror.NewError("unexpected message from server")
		}
		e := envs[0]
		switch {
		case e.isResp:
			if e.err != nil {
				return e.err
			}
			if result != nil {
				return e.bind(result)
			}
			return nil
		case e.method == methodMessage && e.ctrl != nil:
			_, bindData, err := e.ctrl()
			if err != nil {
				return err
			}
			msg := newOut()
			if err := bindData(msg); err != nil {
				return err
			}
			if err := deliver(msg); err != nil {
				return err
			}
		}
	}
}

func chanNext[T proto.Message](in <-chan T) nextFunc {
	return func(ctx context.Context) (proto.Message, bool) {
		select {
		case m, ok := <-in:
			return m, ok
		case <-ctx.Done():
			return nil, false
		}
	}
}

func chanDeliver[T proto.Message](ctx context.Context, out chan<- T) func(proto.Message) error {
	return func(m proto.Message) error {
		v, ok := m.(T)
		if !ok {
			return apperror.NewError("unexpected message type")
		}
		select {
		case out <- v:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ServerStream sends one request and delivers the server messages to out, which is closed when the stream ends.
func ServerStream[Out proto.Message](ctx context.Context, c *Client, method string, in proto.Message, out chan<- Out, newOut func() Out) error {
	if in == nil || out == nil || newOut == nil {
		return apperror.NewError("request, output channel and factory must not be nil")
	}
	defer close(out)
	return c.stream(ctx, method, kindServerStream, in, nil, func() proto.Message { return newOut() }, chanDeliver(ctx, out), nil)
}

// ClientStream sends the messages of in and returns the server response in out once in is closed.
func ClientStream[In proto.Message](ctx context.Context, c *Client, method string, in <-chan In, out proto.Message) error {
	if in == nil || out == nil {
		return apperror.NewError("input channel and response must not be nil")
	}
	return c.stream(ctx, method, kindClientStream, nil, chanNext(in), nil, nil, out)
}

// BidiStream exchanges messages in both directions; out is closed when the stream ends.
func BidiStream[In, Out proto.Message](ctx context.Context, c *Client, method string, in <-chan In, out chan<- Out, newOut func() Out) error {
	if in == nil || out == nil || newOut == nil {
		return apperror.NewError("channels and factory must not be nil")
	}
	defer close(out)
	return c.stream(ctx, method, kindBidi, nil, chanNext(in), func() proto.Message { return newOut() }, chanDeliver(ctx, out), nil)
}
