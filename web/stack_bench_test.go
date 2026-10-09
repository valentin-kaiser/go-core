package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

var benchPayload = strings.Repeat(`{"id":1,"name":"example","tags":["a","b","c"]},`, 220) // about 10 KB

func benchStack(b *testing.B, configure func(*Server) *Server) *Server {
	b.Helper()
	benchLogger(b, 1) // info level: the log middleware runs, its debug event is dropped
	s := configure(New())
	s.WithHandlerFunc("/data", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, benchPayload)
	})
	if s.Error != nil {
		b.Fatal(s.Error)
	}
	return s
}

func runStack(b *testing.B, s *Server, acceptEncoding string) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, "/data", nil)
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
	s := benchStack(b, func(s *Server) *Server { return s.WithLog().WithSecurityHeaders().WithCORSHeaders(&CORSConfig{AllowOrigin: "*"}) })
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
	s := New().WithWebsocket("/ws", func(_ http.ResponseWriter, _ *http.Request, conn *websocket.Conn) {
		defer func() { _ = conn.Close() }()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	})
	if s.Error != nil {
		b.Fatal(s.Error)
	}
	srv := httptest.NewServer(s.router)
	b.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		b.Skipf("websocket dial failed: %v", err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	msg := []byte(strings.Repeat("x", 100))
	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			b.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			b.Fatal(err)
		}
	}
}
