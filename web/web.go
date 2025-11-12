// Package web provides a configurable HTTP web server with built-in support for
// common middleware patterns, file serving, and WebSocket handling.
//
// This package offers a singleton Server instance that can be customized through
// fluent-style methods for setting host, port, headers, middleware, handlers, and WebSocket routes.
//
// Features include:
//   - Serving static files from embedded file systems
//   - Middleware support including security headers, CORS, gzip compression, and request logging
//   - Easy registration of HTTP handlers and handler functions
//   - WebSocket support with connection management and custom handlers
//   - Multi-port support with HTTP and HTTPS protocols
//   - Configurable redirect rules between ports and routes
//   - Graceful shutdown and restart capabilities
//
// Example usage:
//
// package main
//
// import (
//
//	"net/http"
//
//	"github.com/valentin-kaiser/go-core/web"
//
// )
//
//	func main() {
//		done := make(chan error)
//		web.Instance().
//			WithHost("localhost").
//			WithHTTPPort(8080).
//			WithHTTPSPort(8443).
//			WithSelfSignedTLS().
//			Redirect(8080, 8443, "/", "/").  // Redirect HTTP root to HTTPS
//			WithSecurityHeaders().
//			WithCORSHeaders().
//			WithGzip().
//			WithLog().
//			WithHandlerFunc("/", handler).
//			StartAsync(done)
//
//		if err := <-done; err != nil {
//			panic(err)
//		}
//
//		err := web.Instance().Stop()
//		if err != nil {
//			panic(err)
//		}
//	}
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//		w.Header().Set("Content-Type", "text/plain")
//		w.Write([]byte("Hello, World!"))
//	}
package web

import (
	"context"
	"crypto/tls"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"io/fs"
	l "log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/interruption"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/security"
	"github.com/valentin-kaiser/go-core/web/jrpc"
	"github.com/valentin-kaiser/go-core/zlog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

var (
	mutex    sync.Mutex
	instance *Server
	logger   = logging.GetPackageLogger("web")
)

// Protocol represents the protocol type for a server port
type Protocol string

const (
	// ProtocolHTTP represents HTTP protocol
	ProtocolHTTP Protocol = "http"
	// ProtocolHTTPS represents HTTPS protocol
	ProtocolHTTPS Protocol = "https"
)

// ServerPort represents a port configuration with protocol
type ServerPort struct {
	Port     uint16
	Protocol Protocol
}

// RedirectRule represents a redirect configuration
type RedirectRule struct {
	SourcePort  uint16
	TargetPort  uint16
	SourceRoute string
	TargetRoute string
	Code        int                        // HTTP status code for the redirect
	Exceptions  []string                   // List of paths that should not be redirected
	Conditions  []func(*http.Request) bool // List of conditions that must be met for the redirect to occur
}

// Server represents a web server with a set of middlewares and handlers
type Server struct {
	// Error is the error that occurred during the server's operation
	// It will be nil if no error occurred
	Error             error
	servers           map[string]*http.Server // Map of "protocol:port" -> server
	router            *Router
	mutex             sync.RWMutex
	host              string
	ports             []ServerPort   // Multiple ports with protocols
	redirectRules     []RedirectRule // Configured redirect rules
	https             bool
	upgrader          websocket.Upgrader
	tlsConfig         *tls.Config
	readTimeout       time.Duration
	readHeaderTimeout time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	errorLog          *l.Logger
	handler           map[string]http.Handler
	websockets        map[string]func(http.ResponseWriter, *http.Request, *websocket.Conn)
	onHTTPCode        map[string]map[int]func(http.ResponseWriter, *http.Request)
	cacheControl      string // Custom cache control header value
}

func init() {
	New()
}

// Instance returns the singleton instance of the web server
func Instance() *Server {
	mutex.Lock()
	defer mutex.Unlock()
	return instance
}

// New creates a new singleton instance of the web server
func New() *Server {
	mutex.Lock()
	defer mutex.Unlock()
	instance = &Server{
		servers: make(map[string]*http.Server),
		router:  NewRouter(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
		readTimeout:       15 * time.Second,
		readHeaderTimeout: 5 * time.Second,
		writeTimeout:      15 * time.Second,
		idleTimeout:       120 * time.Second,
		errorLog:          l.New(io.Discard, "", 0),
		handler:           make(map[string]http.Handler),
		websockets:        make(map[string]func(http.ResponseWriter, *http.Request, *websocket.Conn)),
		onHTTPCode:        make(map[string]map[int]func(http.ResponseWriter, *http.Request)),
	}

	return instance
}

// Start starts the web server
// All middlewares and handlers that should be registered must be registered before calling this function
func (s *Server) Start() *Server {
	defer interruption.Catch()

	if s.Error != nil {
		return s
	}

	if len(s.ports) == 0 {
		s.Error = apperror.NewError("no ports configured - use WithPort(), WithHTTPPort(), or WithHTTPSPort() to configure at least one port")
		return s
	}

	if s.https && s.tlsConfig == nil {
		s.Error = apperror.NewError("HTTPS ports configured but no TLS configuration provided - use WithTLS() or WithSelfSignedTLS()")
		return s
	}

	group, _ := errgroup.WithContext(context.Background())

	s.mutex.Lock()
	s.servers = make(map[string]*http.Server)
	for _, config := range s.ports {
		serverKey := fmt.Sprintf("%s:%d", config.Protocol, config.Port)

		server := &http.Server{
			Addr:              net.JoinHostPort(s.host, strconv.FormatUint(uint64(config.Port), 10)),
			ErrorLog:          s.errorLog,
			ReadTimeout:       s.readTimeout,
			ReadHeaderTimeout: s.readHeaderTimeout,
			WriteTimeout:      s.writeTimeout,
			IdleTimeout:       s.idleTimeout,
		}

		if config.Protocol == ProtocolHTTP {
			server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rule := s.redirect(config.Port, r.URL.Path)
				if rule != nil {
					for _, exception := range rule.Exceptions {
						if strings.HasPrefix(r.URL.Path, exception) {
							s.router.ServeHTTP(w, r)
							return
						}
					}

					for _, condition := range rule.Conditions {
						if !condition(r) {
							s.router.ServeHTTP(w, r)
							return
						}
					}

					h, _, err := net.SplitHostPort(r.Host)
					if err != nil {
						logger.Error().Fields(logging.F("error", err)).Msg("failed to split host and port from request")
						http.Error(w, "Bad Request", http.StatusBadRequest)
						return
					}
					ip := net.ParseIP(h)
					if ip != nil && ip.To16() != nil && ip.To4() == nil {
						h = "[" + ip.String() + "]"
					}

					targetProtocol := s.getProtocolForPort(rule.TargetPort)
					targetURL := fmt.Sprintf("%s://%s:%d%s", targetProtocol, h, rule.TargetPort, rule.TargetRoute)

					logger.Trace().Fields(
						logging.F("source_path", r.URL.Path),
						logging.F("target_url", targetURL),
						logging.F("redirect_code", rule.Code),
					).Msg("redirecting request")

					http.Redirect(w, r, targetURL, rule.Code)
					return
				}

				s.router.ServeHTTP(w, r)
			})

			s.servers[serverKey] = server
			continue
		}

		server.Handler = s.router
		server.TLSConfig = s.tlsConfig
		s.servers[serverKey] = server
	}
	s.mutex.Unlock()

	for key, server := range s.servers {
		group.Go(func() error {
			protocol := strings.Split(key, ":")[0]
			addr := server.Addr

			logger.Info().Fields(logging.F("addr", addr), logging.F("protocol", protocol)).Msgf("listening on %s", protocol)

			var err error
			if protocol == "https" {
				err = server.ListenAndServeTLS("", "")
				if err != nil && err != http.ErrServerClosed {
					return apperror.NewErrorf("failed to start https server").AddError(err)
				}
				return nil
			}

			err = server.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				return apperror.NewErrorf("failed to start http server").AddError(err)
			}
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		s.Error = err
	}
	return s
}

// StartAsync starts the web server asynchronously
// It will return immediately and the server will run in the background
func (s *Server) StartAsync(done chan error) {
	defer interruption.Catch()

	if s.Error != nil {
		done <- s.Error
		return
	}

	go func() {
		err := s.Start().Error
		if err != nil {
			done <- err
			s.Error = nil
			return
		}

		done <- nil
	}()
}

// Stop stops the web server
// Close does not attempt to close any hijacked connections, such as WebSockets.
func (s *Server) Stop() error {
	defer interruption.Catch()

	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for serverKey, server := range s.servers {
		if server != nil {
			err := server.Close()
			if err != nil {
				return apperror.NewErrorf("failed to stop %s server", serverKey).AddError(err)
			}
		}
	}

	return nil
}

// Shutdown gracefully shuts down the web server
// It will wait for all active connections to finish before shutting down
// Make sure the program doesn't exit and waits instead for Shutdown to return
func (s *Server) Shutdown() error {
	defer interruption.Catch()

	s.mutex.RLock()
	defer s.mutex.RUnlock()
	if len(s.servers) > 0 {
		logger.Trace().Msg("shutting down webservers...")
		shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownRelease()

		for serverKey, server := range s.servers {
			if server != nil {
				err := server.Shutdown(shutdownCtx)
				if err != nil {
					return apperror.NewErrorf("failed to shutdown %s server", serverKey).AddError(err)
				}
			}
		}
	}
	return nil
}

// Restart gracefully shuts down the web server and starts it again
// It will wait for all active connections to finish before shutting down
func (s *Server) Restart() error {
	defer interruption.Catch()

	s.mutex.RLock()
	if len(s.servers) > 0 {
		logger.Trace().Msg("restarting webservers...")
		shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownRelease()

		for key, server := range s.servers {
			if server != nil {
				err := server.Shutdown(shutdownCtx)
				if err != nil {
					s.mutex.RUnlock()
					return apperror.NewErrorf("failed to shutdown %s server", key).AddError(err)
				}
			}
		}
	}
	s.mutex.RUnlock()

	err := s.Start().Error
	if err != nil {
		return apperror.NewError("failed to start webserver").AddError(err)
	}

	return nil
}

// RestartAsync gracefully shuts down the web server and starts it again asynchronously
// It will wait for all active connections to finish before shutting down
func (s *Server) RestartAsync(done chan error) {
	defer interruption.Catch()

	s.mutex.RLock()
	if len(s.servers) > 0 {
		logger.Trace().Msg("restarting webservers...")
		shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownRelease()

		for key, server := range s.servers {
			if server != nil {
				err := server.Shutdown(shutdownCtx)
				if err != nil {
					done <- apperror.NewErrorf("failed to shutdown %s server", key).AddError(err)
					s.mutex.RUnlock()
					return
				}
			}
		}
	}
	s.mutex.RUnlock()

	s.StartAsync(done)
}

// WithHandler adds a custom handler to the server
// It will return an error in the Error field if the path is already registered as a handler or a websocket
func (s *Server) WithHandler(path string, handler http.Handler) *Server {
	if s.Error != nil {
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.handler[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a handler", path)
		return s
	}
	if _, ok := s.websockets[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a websocket", path)
		return s
	}
	s.handler[path] = handler
	s.router.Handle(path, handler)
	return s
}

// WithHandlerFunc adds a custom handler function to the server
// It will return an error in the Error field if the path is already registered as a handler or a websocket
func (s *Server) WithHandlerFunc(path string, handler http.HandlerFunc) *Server {
	if s.Error != nil {
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.handler[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a handler", path)
		return s
	}
	if _, ok := s.websockets[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a websocket", path)
		return s
	}
	s.handler[path] = handler
	s.router.HandleFunc(path, handler)
	return s
}

// WithFS serves files from the specified filesystem
// It will return an error in the Error field if the entrypoint is already registered as a handler or a websocket
func (s *Server) WithFS(entrypoints []string, filesystem fs.FS) *Server {
	if s.Error != nil {
		return s
	}

	for _, entrypoint := range entrypoints {
		err := s.WithHandler(entrypoint, http.FileServer(http.FS(filesystem))).Error
		if err != nil {
			s.Error = apperror.Wrap(err)
			return s
		}
	}
	return s
}

// WithFileServer serves files from the specified directory
// It will fail if the directory does not exist
func (s *Server) WithFileServer(entrypoints []string, path string) *Server {
	if s.Error != nil {
		return s
	}

	_, err := os.Stat(path)
	if err != nil {
		s.Error = apperror.NewErrorf("directory %s does not exist", path).AddError(err)
		return s
	}

	fs := http.FileServer(http.Dir(filepath.Clean(path)))
	for _, entrypoint := range entrypoints {
		s.WithHandler(entrypoint, fs)
		if s.Error != nil {
			return s
		}
	}
	return s
}

// WithWebsocket adds a websocket handler to the server
// It will return an error in the Error field if the path is already registered as a handler or a websocket
// The handler function will be called with the http.ResponseWriter, http.Request and *websocket.Conn
// and will be responsible for handling and closing the connection
func (s *Server) WithWebsocket(path string, handler func(http.ResponseWriter, *http.Request, *websocket.Conn)) *Server {
	if s.Error != nil {
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.handler[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a handler", path)
		return s
	}
	if _, ok := s.websockets[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a websocket", path)
		return s
	}

	s.websockets[path] = handler
	s.router.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error().Fields(logging.F("error", err)).Msg("could not upgrade websocket connection")
			return
		}

		handler(w, r, conn)
	})

	return s
}

func (s *Server) WithJRPC(path string, service *jrpc.Service) *Server {
	if s.Error != nil {
		return s
	}

	path = strings.TrimSuffix(path, "/") + "/{service}/{method}"
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, ok := s.handler[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a handler", path)
		return s
	}
	if _, ok := s.websockets[path]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a websocket", path)
		return s
	}
	s.handler[path] = http.HandlerFunc(service.HandlerFunc)
	s.router.HandleFunc(path, service.HandlerFunc)
	return s
}

// UnregisterHandler removes multiple handlers from the server
func (s *Server) UnregisterHandler(paths []string) *Server {
	if s.Error != nil {
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	var notFound []string
	for _, path := range paths {
		_, ok := s.handler[path]
		if !ok {
			notFound = append(notFound, path)
		}
	}

	if len(notFound) > 0 {
		return s
	}

	for _, path := range paths {
		delete(s.handler, path)
	}

	s.router.UnregisterHandler(paths)
	return s
}

// UnregisterAllHandler removes all handlers from the server
func (s *Server) UnregisterAllHandler() *Server {
	if s.Error != nil {
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.handler = make(map[string]http.Handler)
	s.router.UnregisterAllHandler()
	return s
}

// GetRegisteredRoutes returns a slice of all currently registered route patterns
func (s *Server) GetRegisteredRoutes() []string {
	if s.Error != nil {
		return nil
	}

	return s.router.GetRegisteredRoutes()
}

// WithHost sets the address of the web server
func (s *Server) WithHost(address string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.host = address
	return s
}

// WithPort adds a port with the specified protocol to the server
// This method can be called multiple times to configure multiple ports
func (s *Server) WithPort(port uint16, protocol Protocol) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Check if this port is already configured
	for _, p := range s.ports {
		if p.Port == port {
			s.Error = apperror.NewErrorf("port %d is already configured", port)
			return s
		}
	}

	if protocol == ProtocolHTTPS {
		s.https = true
	}

	s.ports = append(s.ports, ServerPort{
		Port:     port,
		Protocol: protocol,
	})
	return s
}

// WithHTTPPort adds an HTTP port to the server (convenience method)
func (s *Server) WithHTTPPort(port uint16) *Server {
	return s.WithPort(port, ProtocolHTTP)
}

// WithHTTPSPort adds an HTTPS port to the server (convenience method)
func (s *Server) WithHTTPSPort(port uint16) *Server {
	return s.WithPort(port, ProtocolHTTPS)
}

// WithRedirect adds a redirect rule from source port/route to target port/route
func (s *Server) WithRedirect(rule RedirectRule) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if rule.SourcePort == 0 || rule.TargetPort == 0 {
		s.Error = apperror.NewError("source and target port must be specified for redirect rule")
		return s
	}

	if rule.SourcePort == rule.TargetPort && rule.SourceRoute == rule.TargetRoute {
		s.Error = apperror.NewError("source and target port and route cannot be the same for redirect rule")
		return s
	}

	if rule.SourceRoute == "" || rule.TargetRoute == "" {
		s.Error = apperror.NewError("source and target route must be specified for redirect rule")
		return s
	}

	if rule.Code == 0 || (rule.Code < 300 || rule.Code > 399) {
		rule.Code = http.StatusMovedPermanently
	}

	s.redirectRules = append(s.redirectRules, rule)
	return s
}

// Redirect adds a redirect rule from source port/route to target port/route (convenience method)
func (s *Server) Redirect(sourcePort uint16, targetPort uint16, sourceRoute string, targetRoute string) *Server {
	return s.WithRedirect(RedirectRule{
		SourcePort:  sourcePort,
		TargetPort:  targetPort,
		SourceRoute: sourceRoute,
		TargetRoute: targetRoute,
		Code:        http.StatusMovedPermanently,
	})
}

// getRedirectTarget finds a matching redirect rule for the given port and path
func (s *Server) redirect(sourcePort uint16, path string) *RedirectRule {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, rule := range s.redirectRules {
		if rule.SourcePort == sourcePort && strings.HasPrefix(path, rule.SourceRoute) {
			return &rule
		}
	}
	return nil
}

// getProtocolForPort returns the protocol (http/https) for a given port
func (s *Server) getProtocolForPort(port uint16) string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for _, p := range s.ports {
		if p.Port == port {
			return string(p.Protocol)
		}
	}
	// Default fallback
	if port == 443 || port == 8443 {
		return "https"
	}
	return "http"
}

// WithTLS sets the TLS configuration for the web server
func (s *Server) WithTLS(config *tls.Config) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.tlsConfig = config
	return s
}

// WithSelfSignedTLS generates a self-signed TLS certificate for the web server
// It will use the host set by WithHost as the common name for the certificate
func (s *Server) WithSelfSignedTLS() *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.Error != nil {
		return s
	}

	cert, pool, err := security.GenerateSelfSignedCertificate(pkix.Name{CommonName: s.host})
	if err != nil {
		s.Error = apperror.Wrap(err)
		return s
	}
	s.tlsConfig = security.NewTLSConfig(cert, pool, tls.NoClientCert)
	return s
}

// WithSecurityHeaders adds security headers to the server
func (s *Server) WithSecurityHeaders() *Server {
	s.router.Use(MiddlewareOrderSecurity, securityHeaderMiddlewareWithServer(s))
	return s
}

// WithCORSHeaders adds CORS headers to the server
func (s *Server) WithCORSHeaders() *Server {
	s.router.Use(MiddlewareOrderCors, corsHeaderMiddleware)
	return s
}

// WithHeader adds a custom header to the server
func (s *Server) WithHeader(key, value string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderDefault, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(key, value)
			next.ServeHTTP(w, r)
		})
	})
	return s
}

// WithHeaders adds multiple custom headers to the server
func (s *Server) WithHeaders(headers map[string]string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderDefault, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for key, value := range headers {
				w.Header().Set(key, value)
			}
			next.ServeHTTP(w, r)
		})
	})
	return s
}

// WithCacheControl sets a custom Cache-Control header value that will override the default
// cache control setting in security headers
func (s *Server) WithCacheControl(cacheControl string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.cacheControl = cacheControl
	return s
}

// WithGzip enables gzip compression for the server
func (s *Server) WithGzip() *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderGzip, gziphandler.GzipHandler)
	return s
}

// WithGzipLevel enables gzip compression with a specific level for the server
func (s *Server) WithGzipLevel(level int) *Server {
	if s.Error != nil {
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	handler, err := gziphandler.NewGzipLevelHandler(level)
	if err != nil {
		s.Error = apperror.NewError("failed to create gzip handler").AddError(err)
		return s
	}

	s.router.Use(MiddlewareOrderGzip, handler)
	return s
}

// WithLog adds a logging middleware to the server
func (s *Server) WithLog() *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderLog, logMiddleware)
	return s
}

// WithRateLimit applies a rate limit to the given pattern
// The limiter allows `count` events per `period` duration
// Example: WithRateLimit("/api", 10, time.Minute) allows 10 requests per minute
func (s *Server) WithRateLimit(pattern string, count int, period time.Duration) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.Error != nil {
		return s
	}

	if count <= 0 || period <= 0 {
		s.Error = apperror.NewErrorf("invalid rate limit parameters: count=%d, period=%v", count, period)
		return s
	}

	err := s.router.registerRateLimit(pattern, rate.Every(period/time.Duration(count)), count)
	if err != nil {
		s.Error = apperror.Wrap(err)
	}
	return s
}

// WithCanonicalRedirect sets a canonical redirect domain for the server
// If a request is made to the server with a different domain, it will be redirected to the canonical domain
// This is useful for SEO purposes and to avoid duplicate content issues
func (s *Server) WithCanonicalRedirect(domain string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.Error != nil {
		return s
	}

	if s.router.canonicalDomain != "" {
		s.Error = apperror.NewErrorf("canonical redirect domain is already set to %s", s.router.canonicalDomain)
		return s
	}

	s.router.canonicalDomain = domain
	return s
}

// WithHoneypot registers a handler for the given pattern
// Clients that access this pattern will be blocked
// This is useful for detecting and blocking malicious bots or crawlers
func (s *Server) WithHoneypot(pattern string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.Error != nil {
		return s
	}

	if _, ok := s.handler[pattern]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a handler", pattern)
		return s
	}
	if _, ok := s.websockets[pattern]; ok {
		s.Error = apperror.NewErrorf("path %s is already registered as a websocket", pattern)
		return s
	}

	s.router.HandleFunc(pattern, s.router.honeypot)
	return s
}

// WithOnHoneypot registers a callback function that will be called when a honeypot is triggered
// The callback will receives the blocklist used. Useful for persisting the blocklist
func (s *Server) WithOnHoneypot(callback func(map[string]*net.IPNet)) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.Error != nil {
		return s
	}

	if s.router.honeypotCallback != nil {
		s.Error = apperror.NewError("honeypot callback is already set")
		return s
	}

	s.router.honeypotCallback = callback
	return s
}

// SetWhitelist registers a whitelist for the server
// Addresses in the whitelist will be ignored by the rate limit and honey pot
func (s *Server) SetWhitelist(list []string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.Error != nil {
		return s
	}

	err := s.router.setWhitelist(list)
	if err != nil {
		s.Error = apperror.Wrap(err)
	}
	return s
}

// SetBlacklist registers a blacklist for the server
// Addresses in the blacklist will be blocked from accessing the server
// This is useful for blocking known malicious IPs or ranges
func (s *Server) SetBlacklist(list []string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.Error != nil {
		return s
	}

	err := s.router.setBlacklist(list)
	if err != nil {
		s.Error = apperror.Wrap(err)
	}
	return s
}

// WithVaryHeader adds a Vary header to the server
// The Vary header indicates which request headers a server uses when selecting the representation of a resource
// This is important for caching behavior and content negotiation
func (s *Server) WithVaryHeader(header string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderDefault, varyHeaderMiddleware(header))
	return s
}

// WithVaryHeaders adds multiple Vary headers to the server
// The headers will be properly combined with any existing Vary headers
func (s *Server) WithVaryHeaders(headers []string) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderDefault, varyHeaderMiddleware(headers...))
	return s
}

// WithCustomMiddleware adds a custom middleware to the server
func (s *Server) WithCustomMiddleware(middleware Middleware) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(MiddlewareOrderDefault, middleware)
	return s
}

// WithCustomMiddlewareOrder adds a custom middleware to the server with a specific order
// The order is a int8 value that determines the order of the middleware 0 is the original handler,
// -128 is the beginning of the chain, and 127 is the end of the chain
func (s *Server) WithCustomMiddlewareOrder(order MiddlewareOrder, middleware Middleware) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.router.Use(order, middleware)
	return s
}

// WithReadTimeout sets the read timeout for the server
// It will be used for all requests and connections
func (s *Server) WithReadTimeout(timeout time.Duration) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.readTimeout = timeout
	return s
}

// WithReadHeaderTimeout sets the read header timeout for the server
// It will be used for all requests and connections
func (s *Server) WithReadHeaderTimeout(timeout time.Duration) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.readHeaderTimeout = timeout
	return s
}

// WithWriteTimeout sets the write timeout for the server
// It will be used for all requests and connections
func (s *Server) WithWriteTimeout(timeout time.Duration) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.writeTimeout = timeout
	return s
}

// WithIdleTimeout sets the idle timeout for the server
// It will be used for all requests and connections
func (s *Server) WithIdleTimeout(timeout time.Duration) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.idleTimeout = timeout
	return s
}

// WithErrorLog sets the default error log for the server
// It will log errors at the Error level using the zerolog logger
func (s *Server) WithErrorLog() *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.errorLog = l.New(zlog.Logger().WithLevel(zerolog.ErrorLevel), "", 0)
	return s
}

// WithCustomErrorLog sets the error log for the server
func (s *Server) WithCustomErrorLog(logger *l.Logger) *Server {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.errorLog = logger
	return s
}

// WithOnHTTPCode adds a custom handler for a specific HTTP status code and pattern.
// It will be called after the request is handled and before the response is sent,
// and is called with the http.ResponseWriter and the http.Request
func (s *Server) WithOnHTTPCode(code int, pattern []string, handler func(http.ResponseWriter, *http.Request)) *Server {
	if s.Error != nil {
		return s
	}

	if code < http.StatusContinue || code > http.StatusNetworkAuthenticationRequired {
		s.Error = apperror.NewErrorf("invalid HTTP status code %d", code)
		return s
	}

	if len(pattern) == 0 {
		s.Error = apperror.NewError("no pattern provided for HTTP status code handler")
		return s
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, p := range pattern {
		_, ok := s.onHTTPCode[p]
		if !ok {
			s.onHTTPCode[p] = make(map[int]func(http.ResponseWriter, *http.Request))
		}

		_, ok = s.onHTTPCode[p][code]
		if ok {
			s.Error = apperror.NewErrorf("http code %d is already registered for path %s", code, p)
			return s
		}

		s.onHTTPCode[p][code] = handler
		s.router.OnStatus(p, code, handler)
	}
	return s
}
