package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/valentin-kaiser/go-core/v2/internal/await"
)

const (
	benchDataPath      = "/data"
	benchWebsocketPath = "/ws"
	benchPayloadRepeat = 220
	benchEchoSize      = 100
	benchHandlerWait   = 5 * time.Second
)

var benchPayload = strings.Repeat(`{"id":1,"name":"example","tags":["a","b","c"]},`, benchPayloadRepeat) // about 10 KB

func benchStack(b *testing.B, configure func(*Server) *Server) *Server {
	b.Helper()
	benchLogger(b, 1) // info level: the log middleware runs, its debug event is dropped
	s := configure(New())
	s.WithHandlerFunc(benchDataPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, benchPayload)
		if err != nil {
			b.Errorf("writing response failed: %v", err)
		}
	})
	if s.Error != nil {
		b.Fatal(s.Error)
	}
	return s
}

func runStack(b *testing.B, s *Server, acceptEncoding string) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, benchDataPath, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	w := &benchWriter{h: http.Header{}}
	b.SetBytes(int64(len(benchPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(w.h)
		s.router.ServeHTTP(w, req)
	}
}

// A response of about 10 KB through the router with the log, security header and CORS middlewares
func BenchmarkStackPlain(b *testing.B) {
	s := benchStack(b, func(s *Server) *Server {
		return s.WithLog().WithSecurityHeaders().WithCORSHeaders(&CORSConfig{AllowOrigin: "*"})
	})
	runStack(b, s, "")
}

// The same with gzip compression for a client that accepts it
func BenchmarkStackGzip(b *testing.B) {
	s := benchStack(b, func(s *Server) *Server {
		return s.WithLog().WithSecurityHeaders().WithCORSHeaders(&CORSConfig{AllowOrigin: "*"}).WithGzip()
	})
	runStack(b, s, "gzip")
}

// Echo of a 100 byte message over a WebSocket on the loopback interface
func BenchmarkWebsocketEcho(b *testing.B) {
	benchLogger(b, 1)
	handlerClosed := make(chan error, 1)
	s := New().WithWebsocket(benchWebsocketPath, func(_ http.ResponseWriter, _ *http.Request, conn *websocket.Conn) {
		defer func() { handlerClosed <- conn.Close() }()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			err = conn.WriteMessage(mt, msg)
			if err != nil {
				return
			}
		}
	})
	if s.Error != nil {
		b.Fatal(s.Error)
	}
	srv := httptest.NewServer(s.router)
	b.Cleanup(srv.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+benchWebsocketPath, nil)
	if err != nil {
		b.Skipf("websocket dial failed: %v", err)
	}
	b.Cleanup(func() {
		err := resp.Body.Close()
		if err != nil {
			b.Errorf("closing handshake response body failed: %v", err)
		}
		err = conn.Close()
		if err != nil {
			b.Errorf("closing connection failed: %v", err)
		}
		err = await.Receive(b, handlerClosed, benchHandlerWait, "the websocket handler to finish")
		if err != nil {
			b.Errorf("closing server side connection failed: %v", err)
		}
	})

	msg := []byte(strings.Repeat("x", benchEchoSize))
	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := conn.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			b.Fatal(err)
		}
		_, _, err = conn.ReadMessage()
		if err != nil {
			b.Fatal(err)
		}
	}
}
