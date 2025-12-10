// Package jrpc provides a JSON-RPC over HTTP and WebSocket implementation with
// Protocol Buffer support. It enables automatic method dispatch, streaming
// capabilities, and context enrichment for service implementations.
//
// This package requires server definitions to be generated from Protocol Buffer
// service definitions using the protoc-gen-jrpc plugin:
// https://github.com/valentin-kaiser/protoc-gen-jrpc
//
// The plugin generates the correct struct implementations that satisfy the
// Server interface, ensuring proper integration with the jRPC service framework.
//
// Features:
//   - HTTP and WebSocket endpoint support
//   - Automatic method resolution and dispatch with cached lookups
//   - Protocol Buffer JSON marshaling/unmarshaling
//   - Multiple streaming patterns (unary, server, client, bidirectional)
//   - Context enrichment with HTTP and WebSocket components
//   - Comprehensive error handling and connection management
//
// Usage:
//  1. Define your service in a .proto file
//  2. Generate Go code using protoc with the protoc-gen-jrpc plugin
//  3. Implement the generated Server interface
//  4. Create a new jRPC service with jrpc.Register(yourServer)
//  5. Register the HandlerFunc with the web package function WithJRPC
//
// Example:
//
//	  ```go
//	 package main
//
//	  import (
//		     "context"
//	      "log"
//	      "net/http"
//	  	 "github.com/valentin-kaiser/go-core/web"
//	      "github.com/valentin-kaiser/go-core/web/jrpc"
//	  )
//
//	  type MyService struct {
//	      jrpc.UnimplementedMyServiceServer
//	  }
//
//	  func (s *MyService) MyMethod(ctx context.Context, req *MyRequest) (*MyResponse, error) {
//	      // Implement your method logic here
//	      return &MyResponse{}, nil
//	  }
//
//	  func main() {
//	      err := web.Instance().
//	          WithHost("localhost").
//	          WithPort(8080).
//	          WithJRPC(jrpc.Register(&MyService{})).
//	          Start().Error
//	      if err != nil {
//	          log.Fatal().Err(err).Msg("server exited")
//	      }
//	  }
//	  ```
package jrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/logging/log"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Common error messages to avoid repeated allocations
var (
	logger = logging.GetPackageLogger("jrpc")

	errMethodNotFound           = apperror.NewError("method not found")
	errMethodReflectionNotFound = apperror.NewError("method reflection data not found")
	errInvalidMethodSignature   = apperror.NewError("invalid method signature")
	errFirstArgMustBeContext    = apperror.NewError("first argument must be context.Context")
	errSecondReturnMustBeError  = apperror.NewError("second return value must be error")
	errRequestMustBePointer     = apperror.NewError("request must be a pointer")
	errNilRequest               = apperror.NewError("nil request")
	errExpectedProtoMessage     = apperror.NewError("expected proto.Message for request")
	errExpectedError            = apperror.NewError("expected error type in method return value")

	// Cached reflection types to avoid repeated type operations
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()

	// Reusable marshal options to avoid allocation
	marshalOpts = protojson.MarshalOptions{
		EmitUnpopulated: true,
	}

	unmarshalOpts = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

// upgrader is the WebSocket upgrader with default options
var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool {
		return true // Allow connections from any origin
	},
}

// bufferPool provides a pool of byte buffers for JSON operations
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 1024) // Pre-allocate 1KB buffers
	},
}

// ContextKey represents keys for context values
type ContextKey string

const (
	// ContextKeyResponseWriter key for http.ResponseWriter
	ContextKeyResponseWriter ContextKey = "response"
	// ContextKeyRequest key for *http.Request
	ContextKeyRequest ContextKey = "request"
	// ContextKeyWebSocketConn key for *websocket.Conn
	ContextKeyWebSocketConn ContextKey = "websocket"
)

// Service implements the RTLS-Suite API server with support for both
// HTTP and WebSocket endpoints. It provides automatic method dispatch,
// protocol buffer message handling, and context enrichment.
type Service struct {
	Server
	methods map[string]*methodInfo                  // cached method information for faster lookup
	types   map[protoreflect.FullName]proto.Message // cached message types
}

// Server represents a jRPC service implementation.
type Server interface {
	// Descriptor returns the protocol buffer file descriptor for the service.
	Descriptor() protoreflect.FileDescriptor
}

// methodInfo holds cached reflection and protobuf information for a method
type methodInfo struct {
	descriptor  protoreflect.MethodDescriptor
	method      reflect.Value
	reflectType reflect.Type
	inputType   reflect.Type
	outputType  reflect.Type
	messageType proto.Message
	validated   bool
}

// Register creates a new jrpc service instance and registers the provided
// service implementation. The service implementation has to implement the Descriptor method.
// This function builds a method cache for improved lookup performance.
func Register(s Server) *Service {
	service := &Service{
		Server:  s,
		methods: make(map[string]*methodInfo),
		types:   make(map[protoreflect.FullName]proto.Message),
	}

	sv := reflect.ValueOf(s)
	services := s.Descriptor().Services()
	for i := 0; i < services.Len(); i++ {
		sd := services.Get(i)
		methods := sd.Methods()
		for j := 0; j < methods.Len(); j++ {
			md := methods.Get(j)
			mn := string(md.Name())
			sn := string(sd.Name())

			key := sn + "." + mn

			rm := sv.MethodByName(mn)
			if !rm.IsValid() {
				continue
			}

			mt := rm.Type()

			var pm proto.Message = dynamicpb.NewMessage(md.Input())
			pmt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
			if err == nil {
				pm = pmt.New().Interface()
			}
			service.types[md.Input().FullName()] = pm

			var it, ot reflect.Type
			if mt.NumIn() >= 2 {
				it = mt.In(1)
			}
			if mt.NumOut() >= 1 {
				ot = mt.Out(0)
			}

			service.methods[key] = &methodInfo{
				descriptor:  md,
				method:      rm,
				reflectType: mt,
				inputType:   it,
				outputType:  ot,
				messageType: pm,
				validated:   false,
			}
		}
	}

	return service
}

// SetUpgrader allows setting a custom WebSocket upgrader with specific options.
func SetUpgrader(u websocket.Upgrader) {
	upgrader = u
}

// HandlerFunc processes both HTTP and WebSocket requests to API endpoints.
// It automatically detects whether the request is a WebSocket upgrade request
// and routes to the appropriate handler (unary or websocket).
//
// URL format: /{service}/{method}
// Content-Type: application/json (Protocol Buffer JSON format)
//
// For WebSocket requests, the Connection header must contain "Upgrade" and
// the Upgrade header must contain "websocket".
//
// Parameters:
//   - w: HTTP ResponseWriter for sending the response
//   - r: HTTP Request containing the API call
func (s *Service) HandlerFunc(w http.ResponseWriter, r *http.Request) {
	defer interruption.Catch()

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if s.isWebSocketRequest(r) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Error().Err(err).Msg("failed to upgrade connection to websocket")
			http.Error(w, "Failed to upgrade to WebSocket", http.StatusBadRequest)
			return
		}

		s.websocket(w, r, conn)
		return
	}

	s.unary(w, r)
}

// isWebSocketRequest checks if the HTTP request is requesting a WebSocket upgrade
// Optimized version with reduced string allocations
func (s *Service) isWebSocketRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// unary processes HTTP POST requests to API endpoints.
// It performs method resolution, request validation, message unmarshaling,
// method invocation, and response marshaling. The context is enriched with
// HTTP components for use by service methods.
//
// URL format: /{service}/{method}
// Content-Type: application/json (Protocol Buffer JSON format)
//
// Parameters:
//   - w: HTTP ResponseWriter for sending the response
//   - r: HTTP Request containing the API call
func (s *Service) unary(w http.ResponseWriter, r *http.Request) {
	ctx := WithHTTPContext(r.Context(), w, r)

	service := r.PathValue("service")
	method := r.PathValue("method")
	md, err := s.find(service, method)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	msg, err := s.message(md)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.ContentLength > 0 {
		// Use buffer pool for body reading
		buf, ok := bufferPool.Get().([]byte)
		if !ok {
			http.Error(w, "failed to get buffer from pool", http.StatusInternalServerError)
			return
		}
		defer bufferPool.Put(buf[:0])

		// Ensure buffer is large enough
		if cap(buf) < int(r.ContentLength) {
			buf = make([]byte, r.ContentLength)
		} else {
			buf = buf[:r.ContentLength]
		}

		_, err := io.ReadFull(r.Body, buf)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		err = unmarshalOpts.Unmarshal(buf, msg)
		if err != nil {
			http.Error(w, apperror.Wrap(err).Error(), http.StatusBadRequest)
			return
		}
	}
	defer apperror.Catch(r.Body.Close, "closing request body failed")

	resp, err := s.call(ctx, service, method, msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	out, err := s.marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(out)
	if err != nil {
		log.Error().Err(err).Msg("failed to write response")
	}
}

// websocket processes WebSocket connections for streaming API endpoints.
// It validates method signatures against protocol buffer definitions, determines
// the streaming pattern (bidirectional, server-side, or client-side), and routes
// to the appropriate handler. The context is enriched with both HTTP and WebSocket
// components for comprehensive access within service methods.
//
// Supported streaming patterns:
//   - Bidirectional: func(ctx, chan *InputMsg, chan OutputMsg) error
//   - Server streaming: func(ctx, *InputMsg, chan OutputMsg) error
//   - Client streaming: func(ctx, chan *InputMsg) (OutputMsg, error)
//
// Parameters:
//   - w: HTTP ResponseWriter from the WebSocket upgrade
//   - r: HTTP Request from the WebSocket upgrade
//   - conn: Established WebSocket connection
func (s *Service) websocket(w http.ResponseWriter, r *http.Request, conn *websocket.Conn) {
	service := r.PathValue("service")
	method := r.PathValue("method")

	md, err := s.find(service, method)
	if err != nil {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
		return
	}

	if !md.method.IsValid() {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.NewError("service not registered"))
		return
	}

	m := md.method
	mt := md.reflectType

	streamingType, err := s.validateMethodSignature(mt, md.descriptor)
	if err != nil {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.NewError("invalid method signature").AddError(err))
		return
	}

	switch streamingType {
	case StreamingTypeBidirectional:
		s.handleBidirectionalStream(WithWebSocketContext(r.Context(), w, r, conn), conn, m, mt)
	case StreamingTypeServerStream:
		s.handleServerStream(WithWebSocketContext(r.Context(), w, r, conn), conn, m, mt, md)
	case StreamingTypeClientStream:
		s.handleClientStream(WithWebSocketContext(r.Context(), w, r, conn), conn, m, mt)
	case StreamingTypeUnary:
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.NewError("unary methods are not supported over WebSocket"))
	default:
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.NewError("unsupported streaming type"))
	}
}

// WithHTTPContext enriches the provided context with HTTP request components.
// It adds the ResponseWriter and Request to the context, making them available
// throughout the request processing pipeline for logging, middleware, and
// other cross-cutting concerns.
//
// Parameters:
//   - ctx: The base context to enrich
//   - w: The HTTP ResponseWriter for the current request
//   - r: The HTTP Request being processed
//
// Returns the enriched context containing the HTTP components.
func WithHTTPContext(ctx context.Context, w http.ResponseWriter, r *http.Request) context.Context {
	ctx = context.WithValue(ctx, ContextKeyResponseWriter, w)
	ctx = context.WithValue(ctx, ContextKeyRequest, r)
	return ctx
}

// WithWebSocketContext adds a WebSocket connection to the context.
// This enables WebSocket-specific operations and connection management
// from within streaming service methods.
//
// Parameters:
//   - ctx: The base context to enrich
//   - conn: The WebSocket connection for the current session
//
// Returns the enriched context containing the WebSocket connection.
func WithWebSocketContext(ctx context.Context, w http.ResponseWriter, r *http.Request, conn *websocket.Conn) context.Context {
	ctx = context.WithValue(ctx, ContextKeyWebSocketConn, conn)
	ctx = context.WithValue(ctx, ContextKeyResponseWriter, w)
	ctx = context.WithValue(ctx, ContextKeyRequest, r)
	return ctx
}

// GetResponseWriter extracts the HTTP ResponseWriter from the context.
// This allows service methods to access the original response writer for
// setting custom headers, status codes, or other HTTP-specific operations.
//
// Parameters:
//   - ctx: The context containing the ResponseWriter
//
// Returns:
//   - http.ResponseWriter: The response writer if found
//   - bool: True if the ResponseWriter was found in the context
func GetResponseWriter(ctx context.Context) (http.ResponseWriter, bool) {
	w, ok := ctx.Value(ContextKeyResponseWriter).(http.ResponseWriter)
	return w, ok
}

// GetRequest extracts the HTTP Request from the context.
// This provides access to request metadata such as headers, URL parameters,
// authentication information, and other request-specific data for logging,
// authorization, and business logic purposes.
//
// Parameters:
//   - ctx: The context containing the HTTP Request
//
// Returns:
//   - *http.Request: The HTTP request if found
//   - bool: True if the Request was found in the context
func GetRequest(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(ContextKeyRequest).(*http.Request)
	return r, ok
}

// GetWebSocketConn extracts the WebSocket connection from the context.
// This enables streaming service methods to access connection properties,
// configure timeouts, handle connection-specific operations, and manage
// the WebSocket lifecycle.
//
// Parameters:
//   - ctx: The context containing the WebSocket connection
//
// Returns:
//   - *websocket.Conn: The WebSocket connection if found
//   - bool: True if the connection was found in the context
func GetWebSocketConn(ctx context.Context) (*websocket.Conn, bool) {
	conn, ok := ctx.Value(ContextKeyWebSocketConn).(*websocket.Conn)
	return conn, ok
}

func (s *Service) call(ctx context.Context, service, method string, req proto.Message) (any, error) {
	methodInfo, exists := s.methods[service+"."+method]
	if !exists {
		return nil, errMethodNotFound
	}

	if !methodInfo.method.IsValid() {
		return nil, errMethodReflectionNotFound
	}

	m := methodInfo.method
	mt := methodInfo.reflectType

	// Validate method signature if not already validated
	if !methodInfo.validated {
		if mt.NumIn() != 2 || mt.NumOut() != 2 {
			return nil, errInvalidMethodSignature
		}
		if !mt.In(0).Implements(contextType) {
			return nil, errFirstArgMustBeContext
		}
		if !mt.Out(1).Implements(errorType) {
			return nil, errSecondReturnMustBeError
		}
		methodInfo.validated = true
	}

	wanted := mt.In(1)
	if wanted.Kind() != reflect.Ptr {
		return nil, errRequestMustBePointer
	}

	reqVal := reflect.ValueOf(req)
	if !reqVal.IsValid() {
		return nil, errNilRequest
	}

	if !reqVal.Type().AssignableTo(wanted) {
		// Convert via JSON round-trip using protojson to the expected type.
		// Use buffer pool for better performance
		buf, ok := bufferPool.Get().([]byte)
		if !ok {
			return nil, errors.New("failed to get buffer from pool")
		}
		defer bufferPool.Put(buf[:0])

		reqPtr := reflect.New(wanted.Elem())
		b, err := marshalOpts.Marshal(req)
		if err != nil {
			return nil, err
		}
		pm, ok := reqPtr.Interface().(proto.Message)
		if !ok {
			return nil, errExpectedProtoMessage
		}
		err = unmarshalOpts.Unmarshal(b, pm)
		if err != nil {
			return nil, err
		}
		reqVal = reqPtr
	}

	outs := m.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
	res := outs[0].Interface()
	var err error
	if e := outs[1].Interface(); e != nil {
		var ok bool
		err, ok = e.(error)
		if !ok {
			return nil, errExpectedError
		}
	}

	l := logger.Trace()
	if err != nil {
		l = logger.Warn().Err(err)
	}

	l.Field("service", service).Field("method", method).Msg("jRPC method called")
	return res, err
}

func (s *Service) find(service, method string) (*methodInfo, error) {
	md, exists := s.methods[service+"."+method]
	if !exists {
		return nil, errMethodNotFound
	}

	return md, nil
}

func (s *Service) message(md *methodInfo) (proto.Message, error) {
	if md.messageType == nil {
		return nil, apperror.NewError("message type not found")
	}

	return proto.Clone(md.messageType), nil
}

func (s *Service) marshal(m any) ([]byte, error) {
	if m == nil {
		return nil, apperror.NewError("cannot marshal nil message")
	}

	switch msg := m.(type) {
	case proto.Message:
		out, err := marshalOpts.Marshal(msg)
		if err != nil {
			return nil, apperror.NewError("failed to marshal response").AddError(err)
		}

		return out, nil
	default:
		return nil, apperror.NewError("failed to marshal response")
	}
}

// handleBidirectionalStream handles bidirectional streaming WebSocket connections
func (s *Service) handleBidirectionalStream(ctx context.Context, conn *websocket.Conn, m reflect.Value, mt reflect.Type) {
	inType, outType := mt.In(1), mt.In(2)
	inPtr, outPtr := inType.Elem(), outType.Elem()
	in, out := reflect.MakeChan(inType, 0), reflect.MakeChan(outType, 0)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	read := s.startMessageReader(ctx, conn, in, inPtr)
	write := s.startMessageWriter(ctx, conn, out, outPtr)

	done := make(chan error, 1)
	go func() {
		outs := m.Call([]reflect.Value{reflect.ValueOf(ctx), in, out})
		e := outs[0].Interface()
		if e != nil {
			err, ok := e.(error)
			if !ok {
				log.Error().Msg("method returned non-error type in error position")
			}
			done <- err
		} else {
			done <- nil
		}
		out.Close()
	}()

	var final error
	select {
	case final = <-done:
	case final = <-read:
	}
	<-write

	if final != nil && !websocket.IsCloseError(final, websocket.CloseNormalClosure) {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(final))
		return
	}
	s.closeWS(conn, websocket.CloseNormalClosure, nil)
}

// handleServerStream handles server streaming WebSocket connections
func (s *Service) handleServerStream(ctx context.Context, conn *websocket.Conn, m reflect.Value, mt reflect.Type, md *methodInfo) {
	outType := mt.In(2)
	outPtr := outType.Elem()
	out := reflect.MakeChan(outType, 0)

	msg, err := s.message(md)
	if err != nil {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
		return
	}

	// Read initial message
	reqPtr := reflect.New(mt.In(1).Elem())
	err = s.readWSMessage(conn, reqPtr)
	if err != nil {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
		return
	}

	if reqPtr.IsValid() && len(reflect.ValueOf(msg).Elem().String()) > 0 {
		pmsg, ok := reqPtr.Interface().(proto.Message)
		if !ok {
			s.closeWS(conn, websocket.CloseInternalServerErr, apperror.NewError("request message is not a proto message"))
			return
		}
		b, err := marshalOpts.Marshal(pmsg)
		if err != nil {
			s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
			return
		}
		err = unmarshalOpts.Unmarshal(b, msg)
		if err != nil {
			s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
			return
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	write := s.startMessageWriter(ctx, conn, out, outPtr)

	wanted := mt.In(1)
	reqVal := reflect.ValueOf(msg)
	if !reqVal.Type().AssignableTo(wanted) {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.NewError("request message is of wrong type"))
		return
	}

	done := make(chan error, 1)
	go func() {
		outs := m.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal, out})
		e := outs[0].Interface()
		if e != nil {
			err, ok := e.(error)
			if !ok {
				log.Error().Msg("method returned non-error type in error position")
			}
			done <- err
		} else {
			done <- nil
		}
		out.Close()
	}()

	final := <-done
	<-write

	if final != nil && !websocket.IsCloseError(final, websocket.CloseNormalClosure) {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(final))
		return
	}
	s.closeWS(conn, websocket.CloseNormalClosure, nil)
}

// handleClientStream handles client streaming WebSocket connections
func (s *Service) handleClientStream(ctx context.Context, conn *websocket.Conn, m reflect.Value, mt reflect.Type) {
	inType := mt.In(1)
	inPtr := inType.Elem()
	in := reflect.MakeChan(inType, 0)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	read := s.startMessageReader(ctx, conn, in, inPtr)

	done := make(chan struct {
		resp any
		err  error
	}, 1)
	go func() {
		outs := m.Call([]reflect.Value{reflect.ValueOf(ctx), in})
		res := outs[0].Interface()
		e := outs[1].Interface()
		if e != nil {
			err, ok := e.(error)
			if !ok {
				log.Error().Msg("method returned non-error type in error position")
			}
			done <- struct {
				resp any
				err  error
			}{nil, err}
			return
		}
		done <- struct {
			resp any
			err  error
		}{res, nil}
	}()

	var final struct {
		resp any
		err  error
	}
	select {
	case final = <-done:
	case err := <-read:
		final.err = err
	}

	if final.err != nil && !websocket.IsCloseError(final.err, websocket.CloseNormalClosure) {
		s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(final.err))
		return
	}

	if final.resp != nil {
		err := s.writeWSMessage(conn, reflect.ValueOf(final.resp), reflect.TypeOf(final.resp))
		if err != nil {
			s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
			return
		}
	}
	s.closeWS(conn, websocket.CloseNormalClosure, nil)
}
func (s *Service) readWSMessage(conn *websocket.Conn, msgPtr reflect.Value) error {
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return apperror.NewError("failed to read websocket message").AddError(err)
	}

	if messageType != websocket.TextMessage {
		return apperror.NewError("only text messages are supported")
	}

	if msgPtr.Type().Kind() != reflect.Ptr {
		return apperror.NewError("message type is not a pointer")
	}

	msg, ok := msgPtr.Interface().(proto.Message)
	if !ok {
		return apperror.NewError("message type is not a proto message")
	}

	if len(payload) > 0 {
		err = unmarshalOpts.Unmarshal(payload, msg)
		if err != nil {
			return apperror.NewError("failed to unmarshal websocket message").AddError(err)
		}
	}

	return nil
}

// startMessageReader starts a goroutine to read messages from WebSocket into a channel
func (s *Service) startMessageReader(ctx context.Context, conn *websocket.Conn, inChan reflect.Value, inPtr reflect.Type) <-chan error {
	read := make(chan error, 1)
	go func() {
		defer close(read)
		defer inChan.Close()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			reqPtr := reflect.New(inPtr.Elem())
			err := s.readWSMessage(conn, reqPtr)
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				read <- err
				return
			}

			select {
			case <-ctx.Done():
				return
			default:
				inChan.Send(reqPtr)
			}
		}
	}()
	return read
}

// writeWSMessage marshals and writes a proto message to the WebSocket
func (s *Service) writeWSMessage(conn *websocket.Conn, val reflect.Value, t reflect.Type) error {
	var out any
	switch t.Kind() {
	case reflect.Ptr, reflect.Struct:
		out = val.Interface()
	default:
		return apperror.NewError("unsupported type for websocket message")
	}

	data, err := s.marshal(out)
	if err != nil {
		return apperror.Wrap(err)
	}

	err = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		return apperror.Wrap(err)
	}
	err = conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return apperror.Wrap(err)
	}
	return nil
}

// startMessageWriter starts a goroutine to write messages from a channel to WebSocket
func (s *Service) startMessageWriter(ctx context.Context, conn *websocket.Conn, outChan reflect.Value, outPtr reflect.Type) <-chan struct{} {
	write := make(chan struct{})
	go func() {
		defer close(write)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			val, ok := outChan.Recv()
			if !ok {
				return
			}

			err := s.writeWSMessage(conn, val, outPtr)
			if err != nil {
				s.closeWS(conn, websocket.CloseInternalServerErr, apperror.Wrap(err))
				return
			}
		}
	}()
	return write
}

// isExpectedCloseError checks if an error is an expected websocket close error
// that should not be logged as it indicates normal client disconnection
func isExpectedCloseError(err error) bool {
	if err == nil {
		return false
	}

	// Check for normal websocket close codes
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived) {
		return true
	}

	// Check for network errors that occur during normal disconnection
	errStr := strings.ToLower(err.Error())
	patterns := []string{
		"wsasend:",                        // Windows socket send error
		"wsarecv:",                        // Windows socket receive error
		"broken pipe",                     // Unix connection broken
		"connection reset",                // Connection reset by peer
		"connection aborted",              // Connection aborted
		"connection refused",              // Connection refused
		"going away",                      // Client going away
		"use of closed",                   // Use of closed network connection
		"tls: failed to send closenotify", // TLS close notification failure
	}

	for _, pattern := range patterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

func (s *Service) closeWS(conn *websocket.Conn, code int, err error) {
	var reason string
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) && !errors.Is(err, net.ErrClosed) && !isExpectedCloseError(err) {
		reason, _, _ = apperror.Split(err)
		log.Trace().Field("code", code).Err(err).Msg("websocket connection closing with error")
	}
	if len(reason) > 123 {
		reason = reason[:123] // Close reason must be <= 123 bytes
	}
	err = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) && !errors.Is(err, net.ErrClosed) && !isExpectedCloseError(err) {
		log.Trace().Err(err).Msg("failed to send websocket close message")
	}
	err = conn.Close()
	if err != nil && !errors.Is(err, net.ErrClosed) && !isExpectedCloseError(err) {
		log.Trace().Err(err).Msg("failed to close websocket connection")
	}
}

// StreamingType represents the type of streaming for a method
type StreamingType int

const (
	// StreamingTypeUnary represents a unary (non-streaming) method
	StreamingTypeUnary StreamingType = iota
	// StreamingTypeBidirectional represents a bidirectional streaming method
	StreamingTypeBidirectional
	// StreamingTypeServerStream represents a server streaming method
	StreamingTypeServerStream
	// StreamingTypeClientStream represents a client streaming method
	StreamingTypeClientStream
	// StreamingTypeInvalid represents an invalid method signature
	StreamingTypeInvalid
)

// validateMethodSignature validates and determines the streaming type of a method
func (s *Service) validateMethodSignature(mt reflect.Type, md protoreflect.MethodDescriptor) (StreamingType, error) {
	// Basic validation: must have at least context parameter and error return
	if mt.NumIn() < 1 || !mt.In(0).Implements(contextType) {
		return StreamingTypeInvalid, apperror.NewError("first parameter must be context.Context")
	}
	if mt.NumOut() < 1 || !mt.Out(mt.NumOut()-1).Implements(errorType) {
		return StreamingTypeInvalid, apperror.NewError("last return value must be error")
	}

	inputType := s.getProtoMessageType(md.Input())
	outputType := s.getProtoMessageType(md.Output())

	// Determine streaming type based on proto descriptor and validate signature
	isServerStreaming := md.IsStreamingServer()
	isClientStreaming := md.IsStreamingClient()

	switch {
	case isServerStreaming && isClientStreaming:
		// Bidirectional streaming: func(ctx, chan *InputMsg, chan OutputMsg) error
		if mt.NumIn() != 3 || mt.NumOut() != 1 {
			return StreamingTypeInvalid, apperror.NewError("bidirectional streaming method must have signature: func(context.Context, chan *InputMsg, chan OutputMsg) error")
		}

		// Validate input channel type: chan *InputMsg
		if mt.In(1).Kind() != reflect.Chan || mt.In(1).Elem().Kind() != reflect.Ptr {
			return StreamingTypeInvalid, apperror.NewError("second parameter must be chan *InputMsg")
		}
		actualInputType := mt.In(1).Elem().Elem() // chan *T -> T
		if !s.typesMatch(actualInputType, inputType) {
			return StreamingTypeInvalid, apperror.NewError("input channel type mismatch: expected chan *" + inputType.String() + ", got " + mt.In(1).String())
		}

		// Validate output channel type: chan OutputMsg
		if mt.In(2).Kind() != reflect.Chan {
			return StreamingTypeInvalid, apperror.NewError("third parameter must be chan OutputMsg")
		}
		actualOutputType := mt.In(2).Elem() // chan T -> T
		// For output, we need to handle both *T and T cases
		if actualOutputType.Kind() == reflect.Ptr {
			actualOutputType = actualOutputType.Elem()
		}
		if !s.typesMatch(actualOutputType, outputType) {
			return StreamingTypeInvalid, apperror.NewError("output channel type mismatch: expected chan " + outputType.String() + ", got " + mt.In(2).String())
		}

		return StreamingTypeBidirectional, nil

	case isServerStreaming && !isClientStreaming:
		// Server streaming: func(ctx, *InputMsg, chan OutputMsg) error
		if mt.NumIn() != 3 || mt.NumOut() != 1 {
			return StreamingTypeInvalid, apperror.NewError("server streaming method must have signature: func(context.Context, *InputMsg, chan OutputMsg) error")
		}

		// Validate input type: *InputMsg
		if mt.In(1).Kind() != reflect.Ptr {
			return StreamingTypeInvalid, apperror.NewError("second parameter must be *InputMsg")
		}
		actualInputType := mt.In(1).Elem() // *T -> T
		if !s.typesMatch(actualInputType, inputType) {
			return StreamingTypeInvalid, apperror.NewError("input type mismatch: expected *" + inputType.String() + ", got " + mt.In(1).String())
		}

		// Validate output channel type: chan OutputMsg
		if mt.In(2).Kind() != reflect.Chan || mt.In(2).ChanDir()&reflect.SendDir == 0 {
			return StreamingTypeInvalid, apperror.NewError("third parameter must be send chan OutputMsg")
		}
		actualOutputType := mt.In(2).Elem() // chan T -> T
		if actualOutputType.Kind() == reflect.Ptr {
			actualOutputType = actualOutputType.Elem()
		}
		if !s.typesMatch(actualOutputType, outputType) {
			return StreamingTypeInvalid, apperror.NewError("output channel type mismatch: expected chan " + outputType.String() + ", got " + mt.In(2).String())
		}

		return StreamingTypeServerStream, nil

	case !isServerStreaming && isClientStreaming:
		// Client streaming: func(ctx, chan *InputMsg) (OutputMsg, error)
		if mt.NumIn() != 2 || mt.NumOut() != 2 {
			return StreamingTypeInvalid, apperror.NewError("client streaming method must have signature: func(context.Context, chan *InputMsg) (OutputMsg, error)")
		}

		// Validate input channel type: chan *InputMsg
		if mt.In(1).Kind() != reflect.Chan || mt.In(1).Elem().Kind() != reflect.Ptr {
			return StreamingTypeInvalid, apperror.NewError("second parameter must be chan *InputMsg")
		}
		actualInputType := mt.In(1).Elem().Elem() // chan *T -> T
		if !s.typesMatch(actualInputType, inputType) {
			return StreamingTypeInvalid, apperror.NewError("input channel type mismatch: expected chan *" + inputType.String() + ", got " + mt.In(1).String())
		}

		// Validate output type: OutputMsg or *OutputMsg
		actualOutputType := mt.Out(0)
		if actualOutputType.Kind() == reflect.Ptr {
			actualOutputType = actualOutputType.Elem()
		}
		if !s.typesMatch(actualOutputType, outputType) {
			return StreamingTypeInvalid, apperror.NewError("output type mismatch: expected " + outputType.String() + ", got " + mt.Out(0).String())
		}

		return StreamingTypeClientStream, nil

	default:
		// Unary: func(ctx, *InputMsg) (OutputMsg, error)
		if mt.NumIn() != 2 || mt.NumOut() != 2 {
			return StreamingTypeInvalid, apperror.NewError("unary method must have signature: func(context.Context, *InputMsg) (OutputMsg, error)")
		}

		// Validate input type: *InputMsg
		if mt.In(1).Kind() != reflect.Ptr {
			return StreamingTypeInvalid, apperror.NewError("second parameter must be *InputMsg")
		}
		actualInputType := mt.In(1).Elem() // *T -> T
		if !s.typesMatch(actualInputType, inputType) {
			return StreamingTypeInvalid, apperror.NewError("input type mismatch: expected *" + inputType.String() + ", got " + mt.In(1).String())
		}

		// Validate output type: OutputMsg or *OutputMsg
		actualOutputType := mt.Out(0)
		if actualOutputType.Kind() == reflect.Ptr {
			actualOutputType = actualOutputType.Elem()
		}
		if !s.typesMatch(actualOutputType, outputType) {
			return StreamingTypeInvalid, apperror.NewError("output type mismatch: expected " + outputType.String() + ", got " + mt.Out(0).String())
		}

		return StreamingTypeUnary, nil
	}
}

// getProtoMessageType resolves a proto message descriptor to its Go reflect.Type
func (s *Service) getProtoMessageType(msgDesc protoreflect.MessageDescriptor) reflect.Type {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(msgDesc.FullName())
	if err != nil {
		dynMsg := dynamicpb.NewMessage(msgDesc)
		return reflect.TypeOf(dynMsg).Elem()
	}
	return reflect.TypeOf(mt.New().Interface()).Elem()
}

// typesMatch checks if two reflect.Types represent the same proto message type
func (s *Service) typesMatch(actual, expected reflect.Type) bool {
	// Direct type comparison
	if actual == expected {
		return true
	}

	// Compare by type name as fallback
	actualName := actual.String()
	expectedName := expected.String()

	// Handle package differences - compare the base type name
	if actualName == expectedName {
		return true
	}

	// Extract the struct name for comparison
	actualParts := actual.Name()
	expectedParts := expected.Name()

	return actualParts == expectedParts
}
