package xrpc

import (
	"context"
	"net/http"
	"reflect"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *Service) grpcServer() *grpc.Server {
	s.grpcOnce.Do(func() {
		srv := grpc.NewServer(s.grpcOpts...)
		descs := make(map[string]*grpc.ServiceDesc)
		var order []string
		for _, m := range s.list {
			d := descs[m.full]
			if d == nil {
				d = &grpc.ServiceDesc{ServiceName: m.full, HandlerType: (*any)(nil), Metadata: m.desc.ParentFile().Path()}
				descs[m.full] = d
				order = append(order, m.full)
			}
			if m.kind == kindUnary {
				d.Methods = append(d.Methods, grpc.MethodDesc{MethodName: m.name, Handler: unaryHandler(m)})
				continue
			}
			d.Streams = append(d.Streams, grpc.StreamDesc{
				StreamName:    m.name,
				Handler:       streamHandler(m),
				ServerStreams: m.kind == kindServerStream || m.kind == kindBidi,
				ClientStreams: m.kind == kindClientStream || m.kind == kindBidi,
			})
		}
		for _, name := range order {
			srv.RegisterService(descs[name], nil)
		}
		if s.reflect {
			reflection.Register(srv)
		}
		s.grpcSrv = srv
	})
	return s.grpcSrv
}

func (s *Service) serveGRPC(w http.ResponseWriter, r *http.Request) {
	ctx := withProtocol(WithHTTPContext(r.Context(), w, r), ProtocolGRPC)
	s.grpcServer().ServeHTTP(w, r.WithContext(ctx))
}

func unaryHandler(m *method) func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
	return func(_ any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := m.newIn()
		if err := dec(in); err != nil {
			return nil, err
		}

		call := func(ctx context.Context, req any) (any, error) {
			msg, ok := req.(proto.Message)
			if !ok || reflect.TypeOf(req) != m.in {
				return nil, status.Error(codes.InvalidArgument, "unexpected request type")
			}
			out, err := m.invoke(ctx, msg, nil, nil)
			if err != nil {
				return nil, toGRPCError(err)
			}
			return out, nil
		}
		if interceptor == nil {
			return call(ctx, in)
		}
		return interceptor(ctx, in, &grpc.UnaryServerInfo{FullMethod: "/" + m.full + "/" + m.name}, call)
	}
}

func streamHandler(m *method) grpc.StreamHandler {
	return func(_ any, stream grpc.ServerStream) error {
		var first proto.Message
		if m.kind == kindServerStream {
			first = m.newIn()
			if err := stream.RecvMsg(first); err != nil {
				return err
			}
		}

		recv := func() (proto.Message, error) {
			in := m.newIn()
			if err := stream.RecvMsg(in); err != nil {
				return nil, err
			}
			return in, nil
		}
		send := func(msg proto.Message) error { return stream.SendMsg(msg) }
		out, err := m.invoke(stream.Context(), first, recv, send)
		if err != nil {
			return toGRPCError(err)
		}
		if m.kind == kindClientStream {
			return stream.SendMsg(out)
		}
		return nil
	}
}

// reflectionPaths are the request paths of the gRPC reflection services registered by WithGRPCReflection.
var reflectionPaths = []string{
	"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
	"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
}
