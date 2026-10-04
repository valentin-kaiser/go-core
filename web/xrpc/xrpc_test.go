package xrpc_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valentin-kaiser/go-core/web/xrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// fdp is a convenient proto2 message with scalar, enum and nested message fields:
// name=1, number=3, type_name=6.
type fdp = descriptorpb.FieldDescriptorProto

const fdpName = ".google.protobuf.FieldDescriptorProto"

var testFile = func() protoreflect.FileDescriptor {
	m := func(name, in, out string, cs, ss bool) *descriptorpb.MethodDescriptorProto {
		return &descriptorpb.MethodDescriptorProto{
			Name:            proto.String(name),
			InputType:       proto.String(in),
			OutputType:      proto.String(out),
			ClientStreaming: proto.Bool(cs),
			ServerStreaming: proto.Bool(ss),
		}
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/test.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto", "google/protobuf/empty.proto"},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Test"),
			Method: []*descriptorpb.MethodDescriptorProto{
				m("Echo", fdpName, fdpName, false, false),
				m("Fail", ".google.protobuf.Empty", ".google.protobuf.Empty", false, false),
				m("Repeat", fdpName, fdpName, false, true),
				m("Collect", fdpName, fdpName, true, false),
				m("Chat", fdpName, fdpName, true, true),
			},
		}},
	}, protoregistry.GlobalFiles)
	if err != nil {
		panic(err)
	}
	return fd
}()

type testServer struct{}

func (testServer) Descriptor() protoreflect.FileDescriptor { return testFile }

func (testServer) Echo(ctx context.Context, in *fdp) (*fdp, error) {
	if in.GetName() == "status" {
		xrpc.SetStatus(ctx, http.StatusAccepted)
	}
	return in, nil
}

func (testServer) Fail(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	return nil, xrpc.NewError(42, "boom")
}

func (testServer) Repeat(_ context.Context, in *fdp, out chan<- *fdp) error {
	for i := int32(0); i < in.GetNumber(); i++ {
		out <- &fdp{Name: in.Name, Number: proto.Int32(i)}
	}
	if in.GetName() == "fail" {
		return status.Error(5, "stream failed")
	}
	return nil
}

func (testServer) Collect(_ context.Context, in <-chan *fdp) (*fdp, error) {
	var names []string
	for m := range in {
		names = append(names, m.GetName())
	}
	return &fdp{Name: proto.String(strings.Join(names, ","))}, nil
}

func (testServer) Chat(_ context.Context, in <-chan *fdp, out chan<- *fdp) error {
	for m := range in {
		out <- &fdp{Name: proto.String("echo:" + m.GetName())}
	}
	return nil
}

func newServer(t *testing.T, svc *xrpc.Service) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(svc)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	ts.Config.Protocols = protocols
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url, contentType, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(url, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func TestJSONRPC(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}))

	tests := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{
			name:   "unary uses field numbers",
			body:   `{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"foo","3":5},"id":1}`,
			status: 200,
			want:   `{"jsonrpc":"2.0","result":{"1":"foo","3":5},"id":1}`,
		},
		{
			name:   "string id and full method name",
			body:   `{"jsonrpc":"2.0","method":"test.v1.Test/Echo","params":{"1":"foo"},"id":"abc"}`,
			status: 200,
			want:   `{"jsonrpc":"2.0","result":{"1":"foo"},"id":"abc"}`,
		},
		{
			name:   "named fields are accepted",
			body:   `{"jsonrpc":"2.0","method":"Test.Echo","params":{"name":"foo","number":5},"id":1}`,
			status: 200,
			want:   `{"jsonrpc":"2.0","result":{"1":"foo","3":5},"id":1}`,
		},
		{
			name:   "positional params",
			body:   `{"jsonrpc":"2.0","method":"Test.Echo","params":[{"1":"foo"}],"id":1}`,
			status: 200,
			want:   `{"jsonrpc":"2.0","result":{"1":"foo"},"id":1}`,
		},
		{
			name:   "missing params",
			body:   `{"jsonrpc":"2.0","method":"Test.Echo","id":1}`,
			status: 200,
			want:   `{"jsonrpc":"2.0","result":{},"id":1}`,
		},
		{
			name:   "handler selects http status",
			body:   `{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"status"},"id":1}`,
			status: 202,
			want:   `{"jsonrpc":"2.0","result":{"1":"status"},"id":1}`,
		},
		{
			name:   "method not found",
			body:   `{"jsonrpc":"2.0","method":"Test.Nope","id":1}`,
			status: 404,
			want:   `{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"},"id":1}`,
		},
		{
			name:   "parse error",
			body:   `{"jsonrpc":"2.0","method":`,
			status: 400,
			want:   `{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error"},"id":null}`,
		},
		{
			name:   "invalid request version",
			body:   `{"jsonrpc":"1.0","method":"Test.Echo","id":7}`,
			status: 400,
			want:   `{"jsonrpc":"2.0","error":{"code":-32600,"message":"invalid request"},"id":7}`,
		},
		{
			name:   "empty batch",
			body:   `[]`,
			status: 400,
			want:   `{"jsonrpc":"2.0","error":{"code":-32600,"message":"invalid request"},"id":null}`,
		},
		{
			name:   "invalid params",
			body:   `{"jsonrpc":"2.0","method":"Test.Echo","params":{"3":"abc"},"id":1}`,
			status: 400,
		},
		{
			name:   "handler error",
			body:   `{"jsonrpc":"2.0","method":"Test.Fail","id":"x"}`,
			status: 500,
			want:   `{"jsonrpc":"2.0","error":{"code":42,"message":"boom"},"id":"x"}`,
		},
		{
			name:   "streaming over http",
			body:   `{"jsonrpc":"2.0","method":"Test.Chat","id":1}`,
			status: 426,
			want:   `{"jsonrpc":"2.0","error":{"code":-32001,"message":"streaming method requires WebSocket or gRPC"},"id":1}`,
		},
		{
			name:   "batch with notification and invalid entry",
			body:   `[{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"a"},"id":1},{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"b"}},{"foo":1}]`,
			status: 200,
			want:   `[{"jsonrpc":"2.0","result":{"1":"a"},"id":1},{"jsonrpc":"2.0","error":{"code":-32600,"message":"invalid request"},"id":null}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := post(t, ts.URL, "application/json", tt.body)
			if status != tt.status {
				t.Errorf("status = %d, want %d (%s)", status, tt.status, body)
			}
			if tt.want != "" && body != tt.want {
				t.Errorf("body = %s\nwant   %s", body, tt.want)
			}
			if tt.want == "" && !strings.Contains(body, "-32602") {
				t.Errorf("expected invalid params error, got %s", body)
			}
		})
	}

	t.Run("notification", func(t *testing.T) {
		status, body := post(t, ts.URL, "application/json", `{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"a"}}`)
		if status != http.StatusNoContent || body != "" {
			t.Errorf("status = %d, body = %q", status, body)
		}
	})

	t.Run("unsupported content type", func(t *testing.T) {
		status, _ := post(t, ts.URL, "text/plain", `{}`)
		if status != http.StatusUnsupportedMediaType {
			t.Errorf("status = %d", status)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		resp, err := http.Get(ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("status = %d", resp.StatusCode)
		}
	})

	t.Run("body limit", func(t *testing.T) {
		small := newServer(t, xrpc.Register(testServer{}).WithMaxBodySize(16))
		status, _ := post(t, small.URL, "application/json", `{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"a"},"id":1}`)
		if status != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d", status)
		}
	})
}

func TestCaseInsensitiveMethods(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"test.ECHO","params":{"1":"a"},"id":1}`

	strict := newServer(t, xrpc.Register(testServer{}))
	if status, _ := post(t, strict.URL, "application/json", body); status != http.StatusNotFound {
		t.Errorf("strict status = %d, want 404", status)
	}

	svc := xrpc.Register(testServer{}).WithCaseInsensitiveMethods()
	if !svc.Handles("TEST", "echo") {
		t.Error("expected Handles to ignore case")
	}
	ts := newServer(t, svc)
	if status, resp := post(t, ts.URL, "application/json", `{"jsonrpc":"2.0","method":"TEST.echo","params":{"1":"a"},"id":1}`); status != 200 {
		t.Errorf("status = %d (%s)", status, resp)
	}
}

func TestFieldNamesHeader(t *testing.T) {
	const header = "X-Field-Names"
	body := `{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"foo","3":5},"id":1}`
	numbers := `{"jsonrpc":"2.0","result":{"1":"foo","3":5},"id":1}`
	names := `{"jsonrpc":"2.0","result":{"name":"foo","number":5},"id":1}`

	do := func(t *testing.T, url, value string) (string, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if value != "" {
			req.Header.Set(header, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), resp.Header.Get("Vary")
	}

	t.Run("numbers by default", func(t *testing.T) {
		ts := newServer(t, xrpc.Register(testServer{}).WithFieldNamesHeader(header))
		for value, want := range map[string]string{"": numbers, "true": names, "false": numbers, "garbage": numbers} {
			got, vary := do(t, ts.URL, value)
			if got != want {
				t.Errorf("header %q: got %s, want %s", value, got, want)
			}
			if vary != header {
				t.Errorf("Vary = %q", vary)
			}
		}
	})

	t.Run("names by default", func(t *testing.T) {
		ts := newServer(t, xrpc.Register(testServer{}).WithFieldNames().WithFieldNamesHeader(header))
		for value, want := range map[string]string{"": names, "true": names, "false": numbers} {
			if got, _ := do(t, ts.URL, value); got != want {
				t.Errorf("header %q: got %s, want %s", value, got, want)
			}
		}
	})

	t.Run("header is ignored when not configured", func(t *testing.T) {
		ts := newServer(t, xrpc.Register(testServer{}))
		if got, vary := do(t, ts.URL, "true"); got != numbers || vary != "" {
			t.Errorf("got %s (Vary %q)", got, vary)
		}
	})
}

func TestFieldNamesQueryOnWebSocket(t *testing.T) {
	read := func(t *testing.T, svc *xrpc.Service, query string, header http.Header) string {
		t.Helper()
		ts := newServer(t, svc)
		dialer := websocket.Dialer{Subprotocols: []string{"xrpc.json"}}
		conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+query, header)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		req := `{"jsonrpc":"2.0","method":"Test.Repeat","params":{"1":"r","3":1},"id":1}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(req)); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, frame, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		return string(frame)
	}

	svc := func() *xrpc.Service {
		return xrpc.Register(testServer{}).WithFieldNamesHeader("X-Field-Names").WithFieldNamesQuery("names")
	}

	if got := read(t, svc(), "", nil); !strings.Contains(got, `"data":{"1":"r","3":0}`) {
		t.Errorf("default: %s", got)
	}
	if got := read(t, svc(), "?names=true", nil); !strings.Contains(got, `"data":{"name":"r","number":0}`) {
		t.Errorf("query: %s", got)
	}
	if got := read(t, svc(), "?names=true", http.Header{"X-Field-Names": {"false"}}); !strings.Contains(got, `"data":{"1":"r","3":0}`) {
		t.Errorf("header precedence: %s", got)
	}
	if got := read(t, xrpc.Register(testServer{}).WithFieldNames().WithFieldNamesQuery("names"), "?names=false", nil); !strings.Contains(got, `"data":{"1":"r","3":0}`) {
		t.Errorf("query overriding default names: %s", got)
	}
}

func TestJSONRPCFieldNames(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}).WithFieldNames())
	_, body := post(t, ts.URL, "application/json", `{"jsonrpc":"2.0","method":"Test.Echo","params":{"1":"foo","3":5},"id":1}`)
	if want := `{"jsonrpc":"2.0","result":{"name":"foo","number":5},"id":1}`; body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

func TestXML(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}))

	tests := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{
			name:   "unary",
			body:   `<request id="1" method="Test.Echo"><params><_1>foo &amp; bar</_1><_3>5</_3></params></request>`,
			status: 200,
			want:   `<response id="1"><result><_1>foo &amp; bar</_1><_3>5</_3></result></response>`,
		},
		{
			name:   "error",
			body:   `<request id="2" method="Test.Fail"></request>`,
			status: 500,
			want:   `<response id="2"><error code="42" message="boom"></error></response>`,
		},
		{
			name:   "method not found",
			body:   `<request id="3" method="Nope"/>`,
			status: 404,
			want:   `<response id="3"><error code="-32601" message="method not found"></error></response>`,
		},
		{
			name:   "parse error",
			body:   `<request`,
			status: 400,
			want:   `<response><error code="-32700" message="parse error"></error></response>`,
		},
		{
			name:   "batch",
			body:   `<batch><request id="1" method="Test.Echo"><params><_1>a</_1></params></request><request method="Test.Echo"/><request id="2" method="Test.Echo"><params><_1>b</_1></params></request></batch>`,
			status: 200,
			want:   `<batch><response id="1"><result><_1>a</_1></result></response><response id="2"><result><_1>b</_1></result></response></batch>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := post(t, ts.URL, "application/xml", tt.body)
			if status != tt.status || body != tt.want {
				t.Errorf("status = %d, body = %s\nwant %d, %s", status, body, tt.status, tt.want)
			}
		})
	}
}

// clients returns one client per protocol connected to the same server.
func clients(t *testing.T, ts *httptest.Server) map[string]*xrpc.Client {
	t.Helper()

	conn, err := grpc.NewClient("passthrough:///"+strings.TrimPrefix(ts.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	out := map[string]*xrpc.Client{}
	for name, opts := range map[string][]xrpc.ClientOption{
		"json": {xrpc.WithProtocol(xrpc.ProtocolJSON)},
		"xml":  {xrpc.WithProtocol(xrpc.ProtocolXML)},
		"grpc": {xrpc.WithGRPCConn(conn)},
	} {
		c, err := xrpc.NewClient(ts.URL, opts...)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = c
	}
	return out
}

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestClientUnary(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}))
	for name, c := range clients(t, ts) {
		t.Run(name, func(t *testing.T) {
			out := &fdp{}
			if err := c.Call(timeout(t), "test.v1.Test/Echo", &fdp{Name: proto.String("héllo <&>"), Number: proto.Int32(9)}, out); err != nil {
				t.Fatal(err)
			}
			if out.GetName() != "héllo <&>" || out.GetNumber() != 9 {
				t.Errorf("unexpected response %v", out)
			}

			err := c.Call(timeout(t), "test.v1.Test/Fail", &emptypb.Empty{}, &emptypb.Empty{})
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Errorf("expected boom error, got %v", err)
			}
			if name != "grpc" {
				var xe *xrpc.Error
				if !errors.As(err, &xe) || xe.Code != 42 {
					t.Errorf("expected *xrpc.Error with code 42, got %#v", err)
				}
			}

			err = c.Call(timeout(t), "test.v1.Test/Nope", &emptypb.Empty{}, &emptypb.Empty{})
			if err == nil {
				t.Error("expected method not found error")
			}
		})
	}
}

func TestClientStreams(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}))
	newFDP := func() *fdp { return &fdp{} }

	for name, c := range clients(t, ts) {
		t.Run(name+"/server", func(t *testing.T) {
			out := make(chan *fdp, 8)
			err := xrpc.ServerStream(timeout(t), c, "test.v1.Test/Repeat", &fdp{Name: proto.String("r"), Number: proto.Int32(3)}, out, newFDP)
			if err != nil {
				t.Fatal(err)
			}
			var got []int32
			for m := range out {
				got = append(got, m.GetNumber())
			}
			if len(got) != 3 || got[0] != 0 || got[2] != 2 {
				t.Errorf("got %v", got)
			}
		})

		t.Run(name+"/server error", func(t *testing.T) {
			out := make(chan *fdp, 8)
			err := xrpc.ServerStream(timeout(t), c, "test.v1.Test/Repeat", &fdp{Name: proto.String("fail"), Number: proto.Int32(1)}, out, newFDP)
			if err == nil || !strings.Contains(err.Error(), "stream failed") {
				t.Errorf("expected stream error, got %v", err)
			}
		})

		t.Run(name+"/client", func(t *testing.T) {
			in := make(chan *fdp)
			go func() {
				defer close(in)
				for _, n := range []string{"a", "b", "c"} {
					in <- &fdp{Name: proto.String(n)}
				}
			}()
			out := &fdp{}
			if err := xrpc.ClientStream(timeout(t), c, "test.v1.Test/Collect", in, out); err != nil {
				t.Fatal(err)
			}
			if out.GetName() != "a,b,c" {
				t.Errorf("got %v", out)
			}
		})

		t.Run(name+"/bidi", func(t *testing.T) {
			in := make(chan *fdp)
			out := make(chan *fdp)
			errc := make(chan error, 1)
			go func() { errc <- xrpc.BidiStream(timeout(t), c, "test.v1.Test/Chat", in, out, newFDP) }()

			for _, n := range []string{"x", "y"} {
				in <- &fdp{Name: proto.String(n)}
				if got := (<-out).GetName(); got != "echo:"+n {
					t.Errorf("got %s", got)
				}
			}
			close(in)
			if err := <-errc; err != nil {
				t.Fatal(err)
			}
			if _, ok := <-out; ok {
				t.Error("expected out to be closed")
			}
		})
	}
}

func TestStreamCancel(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}))
	for name, c := range clients(t, ts) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(timeout(t))
			in := make(chan *fdp)
			out := make(chan *fdp)
			errc := make(chan error, 1)
			go func() { errc <- xrpc.BidiStream(ctx, c, "test.v1.Test/Chat", in, out, func() *fdp { return &fdp{} }) }()

			in <- &fdp{Name: proto.String("x")}
			<-out
			cancel()

			select {
			case err := <-errc:
				if err == nil {
					t.Error("expected error after cancel")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stream did not stop after cancel")
			}
		})
	}
}

func TestConcurrentWebSocketStreams(t *testing.T) {
	ts := newServer(t, xrpc.Register(testServer{}))
	c := clients(t, ts)["json"]

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := make(chan *fdp, 16)
			err := xrpc.ServerStream(timeout(t), c, "test.v1.Test/Repeat", &fdp{Name: proto.String("r"), Number: proto.Int32(10)}, out, func() *fdp { return &fdp{} })
			if err != nil {
				t.Error(err)
				return
			}
			n := 0
			for range out {
				n++
			}
			if n != 10 {
				t.Errorf("got %d messages", n)
			}
		}()
	}
	wg.Wait()
}

func TestGRPCInterceptor(t *testing.T) {
	var called bool
	svc := xrpc.Register(testServer{}).WithGRPCOptions(grpc.UnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			called = true
			return h(ctx, req)
		}))
	ts := newServer(t, svc)
	if err := clients(t, ts)["grpc"].Call(timeout(t), "test.v1.Test/Echo", &fdp{}, &fdp{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("interceptor not called")
	}
}

func TestPaths(t *testing.T) {
	svc := xrpc.Register(testServer{})
	if !svc.Handles("Test", "Echo") || svc.Handles("Test", "Nope") {
		t.Error("unexpected Handles result")
	}
	paths := svc.Paths()
	if len(paths) != 5 || paths[0] != "/test.v1.Test/Echo" {
		t.Errorf("unexpected paths %v", paths)
	}
}

var otherFile = func() protoreflect.FileDescriptor {
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("other/v1/other.proto"),
		Package:    proto.String("other.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Test"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("Echo"),
				InputType:  proto.String(fdpName),
				OutputType: proto.String(fdpName),
			}},
		}},
	}, protoregistry.GlobalFiles)
	if err != nil {
		panic(err)
	}
	return fd
}()

type otherServer struct{}

func (otherServer) Descriptor() protoreflect.FileDescriptor { return otherFile }

func (otherServer) Echo(_ context.Context, in *fdp) (*fdp, error) {
	return &fdp{Name: proto.String("other:" + in.GetName())}, nil
}

func TestSameShortServiceName(t *testing.T) {
	svc := xrpc.Register(testServer{}, otherServer{})
	if !svc.Handles("test.v1.Test", "Echo") || !svc.Handles("other.v1.Test", "Echo") {
		t.Fatal("fully-qualified names must address both services")
	}
	if svc.Handles("Test", "Echo") {
		t.Fatal("ambiguous short name must not be exposed")
	}
	if got := len(svc.Paths()); got != 6 {
		t.Fatalf("expected 6 paths, got %d", got)
	}
}

func TestGRPCReflection(t *testing.T) {
	svc := xrpc.Register(testServer{}).WithGRPCReflection()
	if len(svc.Paths()) != 7 {
		t.Fatalf("expected reflection paths to be mounted, got %v", svc.Paths())
	}
	ts := newServer(t, svc)
	conn, err := grpc.NewClient(strings.TrimPrefix(ts.URL, "http://"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stream, err := rpb.NewServerReflectionClient(conn).ServerReflectionInfo(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&rpb.ServerReflectionRequest{MessageRequest: &rpb.ServerReflectionRequest_ListServices{}}); err != nil {
		t.Fatal(err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range resp.GetListServicesResponse().GetService() {
		found = found || s.GetName() == "test.v1.Test"
	}
	if !found {
		t.Errorf("test.v1.Test not listed: %v", resp)
	}
}
