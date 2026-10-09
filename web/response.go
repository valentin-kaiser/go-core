package web

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// ResponseWriter is a wrapper around http.ResponseWriter that captures the status code
type ResponseWriter struct {
	w http.ResponseWriter
	r *http.Request
	// status is the HTTP status code to be sent
	status int
	// buf is a buffer to hold the response body before sending it
	buf bytes.Buffer
	// history is a slice to hold the response body history
	history [][]byte
	// header is a custom header map to hold response headers
	header http.Header
	// hijacked indicates if the connection has been hijacked
	hijacked bool
	// start is the time when the response writer was created
	start time.Time
	// sent indicates that headers have been written to the underlying writer
	sent bool
}

func newResponseWriter(w http.ResponseWriter, r *http.Request) *ResponseWriter {
	return &ResponseWriter{
		w:       w,
		r:       r,
		status:  http.StatusOK, // Default status code
		// header and history are allocated on first use; most responses never need history
		start:   time.Now(),
	}
}

// Header returns the custom header map
func (rw *ResponseWriter) Header() http.Header {
	if rw.header == nil {
		rw.header = make(http.Header)
	}
	return rw.header
}

// WriteHeader captures the status code but does not send it immediately
// It is send when Flush is called
func (rw *ResponseWriter) WriteHeader(status int) {
	rw.status = status
}

// Write buffers the response body
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	return rw.buf.Write(b)
}

// WriteString buffers a string body. Without it io.WriteString converts the string to a new
// byte slice first, which for a large response is a copy of the whole body.
func (rw *ResponseWriter) WriteString(s string) (int, error) {
	return rw.buf.WriteString(s)
}

// History returns the history of response bodies written
func (rw *ResponseWriter) History() [][]byte {
	if rw.history == nil {
		return [][]byte{}
	}
	return rw.history
}

// Status returns the status code of the response
func (rw *ResponseWriter) Status() int {
	return rw.status
}

// Hijack is a wrapper around the http.Hijacker interface
func (rw *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	conn, rwbuf, err := h.Hijack()
	if err != nil {
		return nil, nil, err
	}
	rw.hijacked = true
	rw.clear() // Clear the buffer and history when hijacking
	return conn, rwbuf, nil
}

// flush writes the buffered response to the original ResponseWriter
func (rw *ResponseWriter) flush() {
	if rw.hijacked {
		// If the connection has been hijacked, we do not send the response
		return
	}
	if rw.sent {
		rw.copyTrailers()
	} else {
		for k, vv := range rw.header {
			for _, v := range vv {
				rw.w.Header().Add(k, v)
			}
		}
		rw.w.WriteHeader(rw.status)
		rw.sent = true
	}
	_, err := rw.w.Write(rw.buf.Bytes())
	if err != nil {
		http.Error(rw.w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// copyTrailers forwards trailers set after the headers were sent, either announced via the Trailer header or prefixed with http.TrailerPrefix.
func (rw *ResponseWriter) copyTrailers() {
	declared := make(map[string]struct{})
	for _, v := range rw.w.Header().Values("Trailer") {
		for _, name := range strings.Split(v, ",") {
			declared[http.CanonicalHeaderKey(strings.TrimSpace(name))] = struct{}{}
		}
	}
	for k, vv := range rw.header {
		_, ok := declared[k]
		if ok || strings.HasPrefix(k, http.TrailerPrefix) {
			rw.w.Header()[k] = append([]string(nil), vv...)
		}
	}
}

// Flush sends the buffered response so far to the client, enabling streaming responses such as gRPC.
func (rw *ResponseWriter) Flush() {
	if rw.hijacked {
		return
	}
	rw.flush()
	if rw.buf.Len() > 0 {
		rw.history = append(rw.history, append([]byte(nil), rw.buf.Bytes()...))
		rw.buf.Reset()
	}
	if f, ok := rw.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying http.ResponseWriter for http.ResponseController.
func (rw *ResponseWriter) Unwrap() http.ResponseWriter {
	return rw.w
}

func (rw *ResponseWriter) clear() {
	rw.history = append(rw.history, rw.buf.Bytes())
	rw.buf.Reset()
	rw.header = make(http.Header)
}
