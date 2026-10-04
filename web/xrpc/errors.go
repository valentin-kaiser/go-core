package xrpc

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Error codes as defined by the JSON-RPC 2.0 specification, plus implementation-defined server errors.
const (
	CodeParseError        = -32700
	CodeInvalidRequest    = -32600
	CodeMethodNotFound    = -32601
	CodeInvalidParams     = -32602
	CodeInternalError     = -32603
	CodeServerError       = -32000 // default for errors returned by handlers
	CodeStreamingRequired = -32001 // streaming method called without a streaming transport
)

// Error is a JSON-RPC 2.0 error object. Handlers may return it to control the code and data sent to the caller.
type Error struct {
	Code    int
	Message string
	// Data is optional additional information, encoded like any other message.
	Data proto.Message
	// RawData holds the undecoded data member of errors received by a client.
	RawData []byte
}

// NewError creates an Error with the given code and message.
func NewError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WithData returns a copy of the error carrying the given data message.
func (e *Error) WithData(data proto.Message) *Error {
	c := *e
	c.Data = data
	return &c
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

func toError(err error) *Error {
	var xe *Error
	if errors.As(err, &xe) {
		return xe
	}
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		return NewError(CodeServerError, st.Message())
	}
	return NewError(CodeServerError, err.Error())
}

func httpStatus(e *Error) int {
	switch e.Code {
	case CodeParseError, CodeInvalidRequest, CodeInvalidParams:
		return http.StatusBadRequest
	case CodeMethodNotFound:
		return http.StatusNotFound
	case CodeStreamingRequired:
		return http.StatusUpgradeRequired
	}
	return http.StatusInternalServerError
}

func toGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}

	e := toError(err)
	code := codes.Unknown
	switch e.Code {
	case CodeParseError, CodeInvalidRequest, CodeInvalidParams:
		code = codes.InvalidArgument
	case CodeMethodNotFound:
		code = codes.Unimplemented
	case CodeInternalError:
		code = codes.Internal
	}
	return status.Error(code, e.Message)
}
