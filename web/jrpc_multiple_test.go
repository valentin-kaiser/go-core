package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/valentin-kaiser/go-core/web/jrpc"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type watchOnlyHealthService struct{}

func (*watchOnlyHealthService) Descriptor() protoreflect.FileDescriptor {
	return grpc_health_v1.File_grpc_health_v1_health_proto
}

func (*watchOnlyHealthService) Watch(context.Context, *grpc_health_v1.HealthCheckRequest, chan *grpc_health_v1.HealthCheckResponse) error {
	return nil
}

type checkHealthService struct{}

func (*checkHealthService) Descriptor() protoreflect.FileDescriptor {
	return grpc_health_v1.File_grpc_health_v1_health_proto
}

func (*checkHealthService) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func TestServerWithJRPCMultipleServices(t *testing.T) {
	server := New().WithJRPC("/rpc/v1/",
		jrpc.Register(&watchOnlyHealthService{}),
		jrpc.Register(&checkHealthService{}),
	)
	if server.Error != nil {
		t.Fatalf("unexpected error registering jRPC services: %v", server.Error)
	}

	request := httptest.NewRequest(http.MethodPost, "/rpc/v1/Health/Check", nil)
	response := httptest.NewRecorder()
	server.router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected second service to handle Check with status %d, got %d: %s", http.StatusOK, response.Code, response.Body.String())
	}
}
