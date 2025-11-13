package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// SMTP errors
var (
	ErrServerClosed    = errors.New("smtp: server closed")
	ErrAuthFailed      = errors.New("smtp: authentication failed")
	ErrAuthRequired    = errors.New("smtp: authentication required")
	ErrAuthUnsupported = errors.New("smtp: authentication method not supported")
)

// SMTP response codes
const (
	StatusReady          = 220
	StatusClosing        = 221
	StatusOK             = 250
	StatusStartData      = 354
	StatusAuthSuccessful = 235
	StatusAuthContinue   = 334

	StatusBadCommand     = 500
	StatusBadSyntax      = 501
	StatusNotImplemented = 502
	StatusBadSequence    = 503
	StatusTempFailure    = 451
	StatusPermFailure    = 550
	StatusAuthFailed     = 535
	StatusAuthRequired   = 530
)

// Options represents MAIL command options
type Options struct {
	Size int64
	Body string
	UTF8 bool
}

// RcptOptions represents RCPT command options
type RcptOptions struct {
	Notify string
}

// Backend defines the interface for SMTP backends
type Backend interface {
	NewSession(conn *Conn) (Session, error)
}

// Session defines the interface for SMTP sessions
type Session interface {
	AuthPlain(username, password string) error
	Mail(from string, opts *Options) error
	Rcpt(to string, opts *RcptOptions) error
	Data(r io.Reader) error
	Reset()
	Logout() error
}

// Conn represents an SMTP connection
type Conn struct {
	net.Conn
	scanner       *bufio.Scanner
	writer        *bufio.Writer
	hostname      string
	ehlo          bool
	tls           bool
	authenticated bool
	mutex         sync.RWMutex
}

// NewConn creates a new SMTP connection wrapper
func NewConn(conn net.Conn) *Conn {
	return &Conn{
		Conn:    conn,
		scanner: bufio.NewScanner(conn),
		writer:  bufio.NewWriter(conn),
	}
}

// Hostname returns the hostname provided by the client in HELO/EHLO
func (c *Conn) Hostname() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.hostname
}

// TLS returns true if the connection is using TLS
func (c *Conn) TLS() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.tls
}

// smtpServer manages SMTP server operations and handles incoming messages
type smtpServer struct {
	config           ServerConfig
	manager          *Manager
	handlers         []NotificationHandler
	running          int32
	mutex            sync.RWMutex
	handlerSemaphore chan struct{}      // Semaphore to limit concurrent handlers
	handlerQueue     chan handlerTask   // Queue for pending handler tasks
	ctx              context.Context    // Context for server lifecycle
	cancel           context.CancelFunc // Cancel function for server lifecycle
	wg               sync.WaitGroup     // WaitGroup for graceful shutdown
	security         *SecurityManager   // Security manager for validations

	listener   net.Listener
	shutdown   chan struct{}
	closed     bool
	protocolWg sync.WaitGroup // Separate WaitGroup for protocol connections
}

// handlerTask represents a pending notification handler task
type handlerTask struct {
	handler NotificationHandler
	ctx     context.Context
	from    string
	to      []string
	data    io.Reader
}

// NewSMTPServer creates a new SMTP server
func NewSMTPServer(config ServerConfig, manager *Manager) Server {
	// Set default for MaxConcurrentHandlers if not specified
	if config.MaxConcurrentHandlers <= 0 {
		config.MaxConcurrentHandlers = 50
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := &smtpServer{
		config:           config,
		manager:          manager,
		handlers:         make([]NotificationHandler, 0),
		handlerSemaphore: make(chan struct{}, config.MaxConcurrentHandlers),
		handlerQueue:     make(chan handlerTask, config.MaxConcurrentHandlers*2), // Queue size = 2x semaphore size
		ctx:              ctx,
		cancel:           cancel,
		security:         NewSecurityManager(config.Security),
		shutdown:         make(chan struct{}),
	}

	// Start worker pool for handling queued notification tasks
	server.startWorkerPool()
	return server
}

// Start starts the SMTP server
func (s *smtpServer) Start(_ context.Context) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return apperror.NewError("SMTP server is already running")
	}
	// Start server in goroutine
	serverReady := make(chan error, 1)
	go func() {
		var err error
		if s.config.TLS {
			err = s.ListenAndServeTLS()
		} else {
			err = s.ListenAndServe()
		}

		if err != nil && !errors.Is(err, ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			logger.Error().Err(err).Msg("SMTP server error")
			select {
			case serverReady <- err:
			default:
			}
		}

		atomic.StoreInt32(&s.running, 0)
	}()

	// Check if server started successfully by attempting to connect
	go func() {
		maxAttempts := 10
		for i := 0; i < maxAttempts; i++ {
			time.Sleep(10 * time.Millisecond)

			// Try to connect to the server
			addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				apperror.Catch(conn.Close, "failed to close connection")
				select {
				case serverReady <- nil:
				default:
				}
				return
			}

			// Check if we should stop trying
			if !s.IsRunning() {
				select {
				case serverReady <- apperror.NewError("server stopped before becoming ready"):
				default:
				}
				return
			}
		}

		// Timeout waiting for server to be ready
		select {
		case serverReady <- apperror.NewError("timeout waiting for server to become ready"):
		default:
		}
	}()

	// Wait for server to be ready or fail
	return <-serverReady
}

// Stop stops the SMTP server
func (s *smtpServer) Stop(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&s.running, 1, 0) {
		return apperror.NewError("SMTP server is not running")
	}

	// Cancel context to stop worker pool
	s.cancel()

	// Close SMTP server
	s.mutex.Lock()
	if s.listener != nil {
		err := s.listener.Close()
		if err != nil {
			logger.Error().Err(err).Msg("failed to close SMTP server")
			s.mutex.Unlock()
			return apperror.Wrap(err)
		}
	}
	if !s.closed {
		s.closed = true
		close(s.shutdown)
	}
	s.mutex.Unlock()

	// Wait for worker pool to finish with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()         // Wait for handler workers
		s.protocolWg.Wait() // Wait for SMTP protocol connections
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		logger.Warn().Msg("SMTP server stop timed out waiting for worker pool")
		return ctx.Err()
	}
}

// AddHandler adds a notification handler
func (s *smtpServer) AddHandler(handler NotificationHandler) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.handlers = append(s.handlers, handler)
}

// IsRunning returns true if the server is running
func (s *smtpServer) IsRunning() bool {
	return atomic.LoadInt32(&s.running) == 1
}

// loadTLSConfig loads or generates TLS configuration
func (s *smtpServer) loadTLSConfig() (*tls.Config, error) {
	tlsConfig := s.config.TLSConfig()

	// Try to load existing certificates
	if s.config.CertFile != "" && s.config.KeyFile != "" {
		_, certErr := os.Stat(s.config.CertFile)
		if certErr == nil {
			_, keyErr := os.Stat(s.config.KeyFile)
			if keyErr == nil {
				cert, err := tls.LoadX509KeyPair(s.config.CertFile, s.config.KeyFile)
				if err == nil {
					tlsConfig.Certificates = []tls.Certificate{cert}
					return tlsConfig, nil
				}
			}
		}
	}

	cert, err := s.generateSelfSignedCert()
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	tlsConfig.Certificates = []tls.Certificate{cert}
	return tlsConfig, nil
}

// generateSelfSignedCert generates a self-signed certificate
func (s *smtpServer) generateSelfSignedCert() (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, apperror.NewError("failed to generate private key").AddError(err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Mail Server"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(1, 0, 0), // Valid for 1 year
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:    []string{"localhost", s.config.Domain},
	}

	// Generate certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, apperror.NewError("failed to create certificate").AddError(err)
	}

	// Encode certificate
	certPEM := &bytes.Buffer{}
	err = pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err != nil {
		return tls.Certificate{}, apperror.NewError("failed to encode certificate").AddError(err)
	}

	// Encode private key
	keyPEM := &bytes.Buffer{}
	err = pem.Encode(keyPEM, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err != nil {
		return tls.Certificate{}, apperror.NewError("failed to encode private key").AddError(err)
	}

	// Save certificates if paths are specified
	if s.config.CertFile != "" && s.config.KeyFile != "" {
		err = os.MkdirAll(filepath.Dir(s.config.CertFile), 0750)
		if err != nil {
			logger.Warn().Err(err).Msg("failed to create certificate directory")
			goto createTLS
		}

		err = os.WriteFile(s.config.CertFile, certPEM.Bytes(), 0600)
		if err != nil {
			logger.Warn().Err(err).Msg("failed to save certificate file")
		}

		err = os.WriteFile(s.config.KeyFile, keyPEM.Bytes(), 0600)
		if err != nil {
			logger.Warn().Err(err).Msg("failed to save key file")
		}
	}

createTLS:
	// Create TLS certificate
	cert, err := tls.X509KeyPair(certPEM.Bytes(), keyPEM.Bytes())
	if err != nil {
		return tls.Certificate{}, apperror.NewError("failed to create TLS certificate").AddError(err)
	}

	return cert, nil
}

// notifyHandlers notifies all registered handlers using worker pool with queueing
func (s *smtpServer) notifyHandlers(ctx context.Context, from string, to []string, data []byte) {
	s.mutex.RLock()
	handlers := make([]NotificationHandler, len(s.handlers))
	copy(handlers, s.handlers)
	s.mutex.RUnlock()

	for _, handler := range handlers {
		task := handlerTask{
			handler: handler,
			ctx:     ctx,
			from:    from,
			to:      to,
			data:    bytes.NewReader(data),
		}

		// Try to queue the task, with timeout to avoid blocking
		select {
		case s.handlerQueue <- task:
			// Successfully queued
		case <-time.After(100 * time.Millisecond):
			// Queue is full and timed out - log warning but continue
			logger.Warn().
				Field("from", from).
				Field("to", to).
				Field("queue_size", len(s.handlerQueue)).
				Field("queue_capacity", cap(s.handlerQueue)).
				Msg("notification handler queue is full, dropping task")
		case <-ctx.Done():
			// Context cancelled
			logger.Warn().
				Err(ctx.Err()).
				Field("from", from).
				Field("to", to).
				Msg("notification handler cancelled due to context")
			return
		case <-s.ctx.Done():
			// Server context cancelled
			logger.Warn().
				Field("from", from).
				Field("to", to).
				Msg("notification handler cancelled due to server shutdown")
			return
		}
	}
}

// startWorkerPool starts the worker pool for processing notification handler tasks
func (s *smtpServer) startWorkerPool() {
	// Start worker pool based on MaxConcurrentHandlers
	for i := 0; i < s.config.MaxConcurrentHandlers; i++ {
		s.wg.Add(1)
		go func(workerID int) {
			defer s.wg.Done()

			for {
				select {
				case task := <-s.handlerQueue:
					// Process the handler task
					handlerErr := task.handler(task.ctx, task.from, task.to, task.data)
					if handlerErr != nil {
						logger.Error().
							Err(handlerErr).
							Field("from", task.from).
							Field("to", task.to).
							Field("worker_id", workerID).
							Msg("notification handler failed")
					}
				case <-s.ctx.Done():
					return
				}
			}
		}(i)
	}
}

// session implements the Session interface
type session struct {
	server        *smtpServer
	conn          *Conn
	remoteAddr    string
	authenticated bool
	from          string
	to            []string
	heloValidated bool
}

// validateHeloIfNeeded validates HELO hostname on first SMTP command
func (s *session) validateHeloIfNeeded() error {
	if s.heloValidated || !s.server.config.Security.HeloValidation {
		return nil
	}

	// Get the HELO hostname from the connection
	// The internal SMTP implementation stores this in the connection's Hostname() method
	hostname := s.conn.Hostname()

	err := s.server.security.ValidateHelo(hostname, s.remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", s.remoteAddr).
			Field("hostname", hostname).
			Err(err).
			Msg("HELO validation failed")
		return err
	}

	s.heloValidated = true
	return nil
}

// AuthPlain handles PLAIN authentication
func (s *session) AuthPlain(username, password string) error {
	// Validate HELO first if needed
	err := s.validateHeloIfNeeded()
	if err != nil {
		return err
	}

	// Check rate limiting first
	err = s.server.security.CheckRateLimit(s.remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", s.remoteAddr).
			Field("username", username).
			Msg("authentication rate limit exceeded")
		return ErrAuthFailed
	}

	if !s.server.config.Auth {
		return ErrAuthUnsupported
	}

	logger.Trace().
		Field("username", username).
		Msg("SMTP PLAIN authentication attempt")

	if username == s.server.config.Username && password == s.server.config.Password {
		s.authenticated = true
		s.server.security.RecordAuthSuccess(s.remoteAddr)
		logger.Trace().Field("username", username).Msg("SMTP authentication successful")
		return nil
	}

	// Record auth failure and apply delay
	s.server.security.RecordAuthFailure(s.remoteAddr)
	if delay := s.server.security.GetAuthFailureDelay(); delay > 0 {
		time.Sleep(delay)
	}

	logger.Warn().Field("username", username).Msg("SMTP authentication failed")
	return ErrAuthFailed
}

// Mail handles the MAIL FROM command
func (s *session) Mail(from string, _ *Options) error {
	// Validate HELO first if needed
	err := s.validateHeloIfNeeded()
	if err != nil {
		return err
	}

	// Check rate limiting
	err = s.server.security.CheckRateLimit(s.remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", s.remoteAddr).
			Field("from", from).
			Msg("MAIL command rate limit exceeded")
		return err
	}

	if s.server.config.Auth && !s.authenticated {
		logger.Warn().Field("from", from).Msg("unauthenticated MAIL command rejected")
		return ErrAuthRequired
	}

	logger.Trace().Field("from", from).Msg("MAIL FROM")
	s.from = from
	return nil
}

// Rcpt handles the RCPT TO command
func (s *session) Rcpt(to string, _ *RcptOptions) error {
	// Validate HELO first if needed
	err := s.validateHeloIfNeeded()
	if err != nil {
		return err
	}

	// Check rate limiting
	err = s.server.security.CheckRateLimit(s.remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", s.remoteAddr).
			Field("to", to).
			Msg("RCPT command rate limit exceeded")
		return err
	}

	if s.server.config.Auth && !s.authenticated {
		logger.Warn().Field("to", to).Msg("unauthenticated RCPT command rejected")
		return ErrAuthRequired
	}

	s.to = append(s.to, to)
	return nil
}

// Data handles the email data
func (s *session) Data(r io.Reader) error {
	// Validate HELO first if needed
	err := s.validateHeloIfNeeded()
	if err != nil {
		return err
	}

	if s.server.config.Auth && !s.authenticated {
		logger.Warn().Msg("unauthenticated DATA command rejected")
		return ErrAuthRequired
	}

	// Read message data
	data, err := io.ReadAll(r)
	if err != nil {
		return apperror.NewError("failed to read email data").AddError(err)
	}

	// Notify manager
	if s.server.manager != nil {
		s.server.manager.NotifyMessageReceived()
	}

	// Notify handlers
	ctx := context.Background()
	s.server.notifyHandlers(ctx, s.from, s.to, data)

	return nil
}

// Reset resets the session
func (s *session) Reset() {
	logger.Trace().Msg("SMTP session reset")
	s.from = ""
	s.to = nil
}

// Logout handles session logout
func (s *session) Logout() error {
	// Clean up connection tracking in security manager
	s.server.security.CloseConnection(s.remoteAddr)
	logger.Trace().Msg("SMTP session logout")
	return nil
}

// ListenAndServe starts the server on the configured address
func (s *smtpServer) ListenAndServe() error {
	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

// ListenAndServeTLS starts the server with TLS on the configured address
func (s *smtpServer) ListenAndServeTLS() error {
	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	tlsConfig, err := s.loadTLSConfig()
	if err != nil {
		return err
	}
	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

// Serve accepts incoming connections on the listener
func (s *smtpServer) Serve(listener net.Listener) error {
	s.mutex.Lock()
	s.listener = listener
	s.mutex.Unlock()

	defer func() {
		s.mutex.Lock()
		s.listener = nil
		s.mutex.Unlock()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-s.shutdown:
				return ErrServerClosed
			default:
				return err
			}
		}

		s.protocolWg.Add(1)
		go func() {
			defer s.protocolWg.Done()
			s.handleConnection(conn)
		}()
	}
}

// handleConnection handles a single SMTP connection
func (s *smtpServer) handleConnection(netConn net.Conn) {
	defer func() {
		err := netConn.Close()
		if err != nil {
			// Check if this is a normal connection close/reset
			if isConnectionClosed(err) {
				logger.Trace().Err(err).Msg("connection closed")
				return
			}
			logger.Error().Err(err).Msg("failed to close connection")
		}
	}()

	// Set timeouts
	if s.config.ReadTimeout > 0 {
		err := netConn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
		if err != nil {
			if isConnectionClosed(err) {
				logger.Trace().Err(err).Msg("connection closed while setting read deadline")
				return
			}
			logger.Error().Err(err).Msg("failed to set read deadline")
			return
		}
	}
	if s.config.WriteTimeout > 0 {
		err := netConn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
		if err != nil {
			if isConnectionClosed(err) {
				logger.Trace().Err(err).Msg("connection closed while setting write deadline")
				return
			}
			logger.Error().Err(err).Msg("failed to set write deadline")
			return
		}
	}

	conn := NewConn(netConn)
	session, err := s.NewSession(conn)
	if err != nil {
		s.writeResponse(conn, StatusPermFailure, "connection rejected")
		return
	}
	defer apperror.Catch(session.Logout, "failed to logout session")

	// Send greeting
	greeting := s.config.Domain + " ESMTP Service Ready"
	if s.config.Domain == "" {
		greeting = "ESMTP Service Ready"
	}
	s.writeResponse(conn, StatusReady, greeting)

	// Handle commands
	s.handleCommands(conn, session)
}

// NewSession creates a new SMTP session (implements Backend interface)
func (s *smtpServer) NewSession(conn *Conn) (Session, error) {
	remoteAddr := conn.RemoteAddr().String()

	logger.Trace().
		Field("remote_addr", remoteAddr).
		Msg("new SMTP session")

	// Validate connection using security manager
	err := s.security.ValidateConnection(remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", remoteAddr).
			Err(err).
			Msg("connection rejected by security manager")
		return nil, err
	}

	return &session{
		server:     s,
		conn:       conn,
		remoteAddr: remoteAddr,
	}, nil
}

// handleCommands processes SMTP commands from the client
func (s *smtpServer) handleCommands(conn *Conn, session Session) {
	for {
		// Update read deadline
		if s.config.ReadTimeout > 0 {
			err := conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
			if err != nil {
				if isConnectionClosed(err) {
					logger.Trace().Err(err).Field("remote_addr", conn.RemoteAddr()).Msg("connection closed while updating read deadline")
					return
				}
				logger.Error().Err(err).Field("remote_addr", conn.RemoteAddr()).Msg("failed to update read deadline")
				return
			}
		}

		if !conn.scanner.Scan() {
			scanErr := conn.scanner.Err()
			if scanErr != nil {
				// Check if this is a normal connection close/reset
				if isConnectionClosed(scanErr) {
					logger.Trace().Err(scanErr).Field("remote_addr", conn.RemoteAddr()).Msg("connection closed")
					return
				}
				logger.Error().Err(scanErr).Field("remote_addr", conn.RemoteAddr()).Msg("connection read error")
			}
			return
		}

		line := strings.TrimSpace(conn.scanner.Text())
		if line == "" {
			continue
		}

		logger.Trace().Field("command", line).Field("remote_addr", conn.RemoteAddr()).Msg("received SMTP command")

		parts := strings.SplitN(line, " ", 2)
		command := strings.ToUpper(parts[0])
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		switch command {
		case "HELO":
			s.handleHelo(conn, args, false)
		case "EHLO":
			s.handleHelo(conn, args, true)
		case "STARTTLS":
			s.handleStartTLS(conn)
		case "AUTH":
			s.handleAuth(conn, session, args)
		case "MAIL":
			s.handleMail(conn, session, args)
		case "RCPT":
			s.handleRcpt(conn, session, args)
		case "DATA":
			s.handleData(conn, session)
		case "RSET":
			session.Reset()
			s.writeResponse(conn, StatusOK, "OK")
		case "NOOP":
			s.writeResponse(conn, StatusOK, "OK")
		case "QUIT":
			s.writeResponse(conn, StatusClosing, "Bye")
			return
		default:
			s.writeResponse(conn, StatusNotImplemented, "Command not implemented")
		}
	}
}

// writeResponse writes a single-line SMTP response
func (s *smtpServer) writeResponse(conn *Conn, code int, message string) {
	response := fmt.Sprintf("%d %s\r\n", code, message)
	s.writeRaw(conn, response)
}

// writeMultiResponse writes a multi-line SMTP response
func (s *smtpServer) writeMultiResponse(conn *Conn, code int, messages []string) {
	for i, message := range messages {
		var response string
		if i == len(messages)-1 {
			response = fmt.Sprintf("%d %s\r\n", code, message)
		} else {
			response = fmt.Sprintf("%d-%s\r\n", code, message)
		}
		s.writeRaw(conn, response)
	}
}

// writeRaw writes raw data to the connection
func (s *smtpServer) writeRaw(conn *Conn, data string) {
	if s.config.WriteTimeout > 0 {
		err := conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
		if err != nil {
			if isConnectionClosed(err) {
				logger.Trace().Err(err).Msg("connection closed while setting write deadline")
				return
			}
			logger.Warn().Err(err).Msg("failed to set write deadline, continuing anyway")
		}
	}

	_, err := conn.writer.WriteString(data)
	if err != nil {
		if isConnectionClosed(err) {
			logger.Trace().Err(err).Msg("connection closed while writing response")
			return
		}
		logger.Error().Err(err).Msg("failed to write SMTP response")
		return
	}
	err = conn.writer.Flush()
	if err != nil {
		if isConnectionClosed(err) {
			logger.Trace().Err(err).Msg("connection closed while flushing response")
			return
		}
		logger.Error().Err(err).Msg("failed to flush SMTP response")
		return
	}

	logger.Trace().Field("response", strings.TrimSpace(data)).Msg("sent SMTP response")
}

// isConnectionClosed checks if an error indicates a normal connection close/reset
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Common connection close/reset patterns
	patterns := []string{
		"connection reset by peer",
		"broken pipe",
		"connection aborted",
		"wsarecv:",
		"wsasend:",
		"connection refused",
		"network is unreachable",
		"no route to host",
		"connection timed out",
		"EOF",
	}

	errStrLower := strings.ToLower(errStr)
	for _, pattern := range patterns {
		if strings.Contains(errStrLower, pattern) {
			return true
		}
	}

	// Check for specific error types
	if errors.Is(err, io.EOF) {
		return true
	}

	return false
}

// handleHelo handles HELO/EHLO commands
func (s *smtpServer) handleHelo(conn *Conn, hostname string, extended bool) {
	if hostname == "" {
		s.writeResponse(conn, StatusBadSyntax, "Hostname required")
		return
	}

	conn.mutex.Lock()
	conn.hostname = hostname
	conn.ehlo = extended
	conn.mutex.Unlock()

	if extended {
		responses := []string{s.config.Domain + " Hello " + hostname}

		// Add supported extensions
		if s.config.TLS && !conn.TLS() {
			responses = append(responses, "STARTTLS")
		}
		responses = append(responses, "AUTH PLAIN")
		if s.config.MaxMessageBytes > 0 {
			responses = append(responses, fmt.Sprintf("SIZE %d", s.config.MaxMessageBytes))
		}

		s.writeMultiResponse(conn, StatusOK, responses)
	} else {
		s.writeResponse(conn, StatusOK, s.config.Domain+" Hello "+hostname)
	}
}

// handleStartTLS handles STARTTLS command
func (s *smtpServer) handleStartTLS(conn *Conn) {
	tlsConfig, err := s.loadTLSConfig()
	if err != nil {
		s.writeResponse(conn, StatusNotImplemented, "TLS not available")
		return
	}

	if conn.TLS() {
		s.writeResponse(conn, StatusBadSequence, "Already using TLS")
		return
	}

	s.writeResponse(conn, StatusReady, "Ready to start TLS")

	// Upgrade connection to TLS
	tlsConn := tls.Server(conn, tlsConfig)
	err = tlsConn.Handshake()
	if err != nil {
		logger.Error().Err(err).Msg("TLS handshake failed")
		return
	}

	conn.mutex.Lock()
	conn.Conn = tlsConn
	conn.scanner = bufio.NewScanner(tlsConn)
	conn.writer = bufio.NewWriter(tlsConn)
	conn.tls = true
	conn.authenticated = false // Reset auth after STARTTLS
	conn.mutex.Unlock()
}

// handleAuth handles AUTH command
func (s *smtpServer) handleAuth(conn *Conn, session Session, args string) {
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 1 {
		s.writeResponse(conn, StatusBadSyntax, "AUTH method required")
		return
	}

	method := strings.ToUpper(parts[0])
	switch method {
	case "PLAIN":
		s.handleAuthPlain(conn, session, parts)
	default:
		s.writeResponse(conn, StatusNotImplemented, "Authentication method not supported")
	}
}

// handleAuthPlain handles PLAIN authentication
func (s *smtpServer) handleAuthPlain(conn *Conn, session Session, parts []string) {
	var username, password string

	if len(parts) == 2 {
		// Credentials provided in initial command
		decoded, err := s.decodePlainAuth(parts[1])
		if err != nil {
			s.writeResponse(conn, StatusBadSyntax, "Invalid authentication data")
			return
		}
		username, password = decoded[0], decoded[1]
	} else {
		// Request credentials
		s.writeResponse(conn, StatusAuthContinue, "")

		if !conn.scanner.Scan() {
			return
		}

		decoded, err := s.decodePlainAuth(strings.TrimSpace(conn.scanner.Text()))
		if err != nil {
			s.writeResponse(conn, StatusBadSyntax, "Invalid authentication data")
			return
		}
		username, password = decoded[0], decoded[1]
	}

	err := session.AuthPlain(username, password)
	if err != nil {
		switch {
		case errors.Is(err, ErrAuthFailed):
			s.writeResponse(conn, StatusAuthFailed, "Authentication failed")
		case errors.Is(err, ErrAuthUnsupported):
			s.writeResponse(conn, StatusNotImplemented, "Authentication method not supported")
		default:
			s.writeResponse(conn, StatusTempFailure, "Authentication error")
		}
		return
	}

	conn.mutex.Lock()
	conn.authenticated = true
	conn.mutex.Unlock()

	s.writeResponse(conn, StatusAuthSuccessful, "Authentication successful")
}

// decodePlainAuth decodes PLAIN authentication data
func (s *smtpServer) decodePlainAuth(data string) ([]string, error) {
	// Base64 decode the authentication data
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, apperror.NewError("failed to decode base64").AddError(err)
	}

	// Format: \0username\0password
	parts := strings.Split(string(decoded), "\x00")
	if len(parts) != 3 {
		return nil, apperror.NewError("invalid plain auth format")
	}
	return []string{parts[1], parts[2]}, nil
}

// handleMail handles MAIL command
func (s *smtpServer) handleMail(conn *Conn, session Session, args string) {
	if !strings.HasPrefix(strings.ToUpper(args), "FROM:") {
		s.writeResponse(conn, StatusBadSyntax, "MAIL FROM required")
		return
	}

	from := strings.TrimSpace(args[5:])
	from = strings.Trim(from, "<>")

	opts := &Options{}
	err := session.Mail(from, opts)
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.writeResponse(conn, StatusAuthRequired, "Authentication required")
		} else {
			s.writeResponse(conn, StatusPermFailure, "MAIL command failed")
		}
		return
	}

	s.writeResponse(conn, StatusOK, "OK")
}

// handleRcpt handles RCPT command
func (s *smtpServer) handleRcpt(conn *Conn, session Session, args string) {
	if !strings.HasPrefix(strings.ToUpper(args), "TO:") {
		s.writeResponse(conn, StatusBadSyntax, "RCPT TO required")
		return
	}

	to := strings.TrimSpace(args[3:])
	to = strings.Trim(to, "<>")

	opts := &RcptOptions{}
	err := session.Rcpt(to, opts)
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.writeResponse(conn, StatusAuthRequired, "Authentication required")
		} else {
			s.writeResponse(conn, StatusPermFailure, "RCPT command failed")
		}
		return
	}

	s.writeResponse(conn, StatusOK, "OK")
}

// handleData handles DATA command
func (s *smtpServer) handleData(conn *Conn, session Session) {
	s.writeResponse(conn, StatusStartData, "Start mail input; end with <CRLF>.<CRLF>")

	// Read message data until ".\r\n"
	var data strings.Builder
	for {
		if !conn.scanner.Scan() {
			return
		}

		line := conn.scanner.Text()
		if line == "." {
			break
		}

		// Handle dot-stuffing
		line = strings.TrimPrefix(line, ".")

		data.WriteString(line)
		data.WriteString("\r\n")
	}

	err := session.Data(strings.NewReader(data.String()))
	if err != nil {
		if errors.Is(err, ErrAuthRequired) {
			s.writeResponse(conn, StatusAuthRequired, "Authentication required")
		} else {
			s.writeResponse(conn, StatusPermFailure, "Message rejected")
		}
		return
	}

	s.writeResponse(conn, StatusOK, "Message accepted")
}
