package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/web/xrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type streamingHealthService struct {
	release chan struct{}
}

func (*streamingHealthService) Descriptor() protoreflect.FileDescriptor {
	return grpc_health_v1.File_grpc_health_v1_health_proto
}

func (*streamingHealthService) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func (s *streamingHealthService) Watch(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest, out chan<- *grpc_health_v1.HealthCheckResponse) error {
	out <- &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING}
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	out <- &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}
	return nil
}

const (
	rpcPath             = "/rpc"
	callTimeout         = 10 * time.Second
	firstMessageTimeout = 5 * time.Second
)

func TestServerWithXRPC(t *testing.T) {
	svc := &streamingHealthService{release: make(chan struct{})}
	server := New().WithXRPC(rpcPath, xrpc.Register(svc))
	if server.Error != nil {
		t.Fatalf("unexpected error registering xRPC service: %v", server.Error)
	}

	ts := httptest.NewUnstartedServer(server.router)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	ts.Config.Protocols = protocols
	ts.Start()
	defer ts.Close()

	t.Run("json", func(t *testing.T) {
		resp, err := http.Post(ts.URL+rpcPath, "application/json", strings.NewReader(`{"jsonrpc":"2.0","method":"Health.Check","params":{"1":"x"},"id":1}`))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			err := resp.Body.Close()
			if err != nil {
				t.Errorf("closing response body failed: %v", err)
			}
		}()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading response body failed: %v", err)
		}
		if want := `{"jsonrpc":"2.0","result":{"1":1},"id":1}`; string(body) != want {
			t.Errorf("body = %s, want %s", body, want)
		}
	})

	conn, err := grpc.NewClient("passthrough:///"+strings.TrimPrefix(ts.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := conn.Close()
		if err != nil {
			t.Errorf("closing connection failed: %v", err)
		}
	}()
	client, err := xrpc.NewClient("", xrpc.WithGRPCConn(conn))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	t.Run("grpc unary", func(t *testing.T) {
		out := &grpc_health_v1.HealthCheckResponse{}
		err := client.Call(ctx, "grpc.health.v1.Health/Check", &grpc_health_v1.HealthCheckRequest{}, out)
		if err != nil {
			t.Fatal(err)
		}
		if out.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Errorf("unexpected status %v", out.GetStatus())
		}
	})

	t.Run("grpc streaming is flushed through the router", func(t *testing.T) {
		out := make(chan *grpc_health_v1.HealthCheckResponse)
		errc := make(chan error, 1)
		go func() {
			errc <- xrpc.ServerStream(ctx, client, "grpc.health.v1.Health/Watch", &grpc_health_v1.HealthCheckRequest{}, out, func() *grpc_health_v1.HealthCheckResponse {
				return &grpc_health_v1.HealthCheckResponse{}
			})
		}()

		select {
		case m := <-out:
			if m.GetStatus() != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
				t.Errorf("unexpected first message %v", m)
			}
		case <-time.After(firstMessageTimeout):
			t.Fatal("first message was not delivered before the stream finished")
		}

		close(svc.release)
		if m := <-out; m.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Errorf("unexpected second message %v", m)
		}
		err := <-errc
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestServerWithXRPCConflicts(t *testing.T) {
	svc := xrpc.Register(&streamingHealthService{})
	server := New().WithXRPC(rpcPath, svc).WithXRPC("/other", svc)
	if server.Error == nil {
		t.Fatal("expected error for duplicate gRPC paths")
	}

	if New().WithXRPC(rpcPath, nil).Error == nil {
		t.Fatal("expected error for nil service")
	}
}
