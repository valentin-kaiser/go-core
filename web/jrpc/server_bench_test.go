package jrpc_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/valentin-kaiser/go-core/web/jrpc"
)

type benchHealth struct{}

func (*benchHealth) Descriptor() protoreflect.FileDescriptor {
	return grpc_health_v1.File_grpc_health_v1_health_proto
}

func (*benchHealth) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

type discard struct{ h http.Header }

func (d *discard) Header() http.Header         { return d.h }
func (d *discard) Write(p []byte) (int, error) { return len(p), nil }
func (d *discard) WriteHeader(int)             {}

func benchMux() *http.ServeMux {
	svc := jrpc.Register(&benchHealth{})
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc/{service}/{method}", svc.HandlerFunc)
	return mux
}

// benchBody is the JSON body of the call
var benchBody = []byte(`{"service":"bench"}`)

// newBenchRequest builds the request once; resetBody makes it readable again for the next call,
// so the benchmark measures the service and not httptest.NewRequest.
func newBenchRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/rpc/Health/Check", nil)
	req.ContentLength = int64(len(benchBody))
	return req
}

func resetBody(req *http.Request) {
	req.Body = io.NopCloser(bytes.NewReader(benchBody))
}

// Server side of a unary call: routing, request decoding, the method and response encoding.
func BenchmarkServerUnary(b *testing.B) {
	mux := benchMux()
	w := &discard{h: http.Header{}}
	b.ReportAllocs()
	b.ResetTimer()
	req := newBenchRequest()
	for i := 0; i < b.N; i++ {
		resetBody(req)
		clear(w.h)
		mux.ServeHTTP(w, req)
	}
}

func BenchmarkServerUnaryParallel(b *testing.B) {
	mux := benchMux()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		w := &discard{h: http.Header{}}
		req := newBenchRequest()
		for pb.Next() {
			resetBody(req)
			clear(w.h)
			mux.ServeHTTP(w, req)
		}
	})
}
