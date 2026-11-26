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
	"syscall"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
)

// Server tuning and protocol constants.
const (
	networkTCP = "tcp"

	// startupProbeAttempts is the number of connection probes used to detect a started server.
	startupProbeAttempts = 10
	startupProbeInterval = 10 * time.Millisecond
	startupDialTimeout   = 100 * time.Millisecond
	// handlerQueueTimeout is the time to wait for a free slot in the handler queue.
	handlerQueueTimeout = 100 * time.Millisecond
	// handlerQueueFactor sizes the handler queue as a multiple of the semaphore size.
	handlerQueueFactor = 2

	selfSignedKeyBits = 2048
	// selfSignedValidYears is the validity of generated self-signed certificates.
	selfSignedValidYears = 1
	certDirMode          = 0o750
	certFileMode         = 0o600

	// commandSplitParts splits a command line into the verb and its arguments.
	commandSplitParts = 2
	// crlfLen is the length of the line terminator appended to every message line.
	crlfLen = 2
	// plainAuthParts is the number of NUL separated fields (authzid, authcid, password) of PLAIN auth.
	plainAuthParts     = 3
	plainAuthSeparator = "\x00"

	commandHelo     = "HELO"
	commandEhlo     = "EHLO"
	commandStartTLS = "STARTTLS"
	commandAuth     = "AUTH"
	commandMail     = "MAIL"
	commandRcpt     = "RCPT"
	commandData     = "DATA"
	commandRset     = "RSET"
	commandNoop     = "NOOP"
	commandQuit     = "QUIT"

	mailFromPrefix = "FROM:"
	rcptToPrefix   = "TO:"
	addrBrackets   = "<>"
)

// SMTP response codes
const (
	StatusReady          = 220
	StatusClosing        = 221
	StatusOK             = 250
	StatusStartData      = 354
	StatusAuthSuccessful = 235
	StatusAuthContinue   = 334

	StatusBadCommand      = 500
	StatusBadSyntax       = 501
	StatusNotImplemented  = 502
	StatusBadSequence     = 503
	StatusTempFailure     = 451
	StatusPermFailure     = 550
	StatusMessageTooLarge = 552
	StatusAuthFailed      = 535
	StatusAuthRequired    = 530
)

// SMTP errors
var (
	ErrServerClosed    = errors.New("smtp: server closed")
	ErrAuthFailed      = errors.New("smtp: authentication failed")
	ErrAuthRequired    = errors.New("smtp: authentication required")
	ErrAuthUnsupported = errors.New("smtp: authentication method not supported")
)

// closedConnectionErrors are the errors that mean the peer closed or reset the connection or cannot be reached
var closedConnectionErrors = []error{
	io.EOF,
	io.ErrUnexpectedEOF,
	syscall.ECONNRESET,
	syscall.EPIPE,
	syscall.ECONNABORTED,
	syscall.ECONNREFUSED,
	syscall.ENETUNREACH,
	syscall.EHOSTUNREACH,
	syscall.ETIMEDOUT,
}

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

// NotificationHandler is a function that handles incoming SMTP messages
type NotificationHandler func(ctx context.Context, from string, to []string, data io.Reader) error

// Server is the interface for SMTP servers
type Server interface {
	// Start starts the SMTP server
	Start(ctx context.Context) error
	// Stop stops the SMTP server
	Stop(ctx context.Context) error
	// AddHandler adds a notification handler
	AddHandler(handler NotificationHandler)
	// IsRunning returns true if the server is running
	IsRunning() bool
}

// NewSMTPServer creates a new SMTP server
func NewSMTPServer(config ServerConfig, manager *Manager) Server {
	if config.MaxConcurrentHandlers <= 0 {
		config.MaxConcurrentHandlers = defaultServerMaxConcurrentHandlers
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := &smtpServer{
		config:           config,
		manager:          manager,
		handlers:         make([]NotificationHandler, 0),
		handlerSemaphore: make(chan struct{}, config.MaxConcurrentHandlers),
		handlerQueue:     make(chan handlerTask, config.MaxConcurrentHandlers*handlerQueueFactor),
		ctx:              ctx,
		cancel:           cancel,
		security:         NewSecurityManager(config.Security),
		shutdown:         make(chan struct{}),
	}

	server.startWorkerPool()
	return server
}

// handlerTask represents a pending notification handler task
type handlerTask struct {
	handler NotificationHandler
	ctx     context.Context
	from    string
	to      []string
	data    io.Reader
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

// AuthPlain handles PLAIN authentication
func (s *session) AuthPlain(username, password string) error {
	err := s.validateHeloIfNeeded()
	if err != nil {
		return apperror.NewError("validating helo before authentication failed").AddError(err)
	}

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

	s.server.security.RecordAuthFailure(s.remoteAddr)
	if delay := s.server.security.GetAuthFailureDelay(); delay > 0 {
		time.Sleep(delay)
	}

	logger.Warn().Field("username", username).Msg("SMTP authentication failed")
	return ErrAuthFailed
}

// Mail handles the MAIL FROM command
func (s *session) Mail(from string, _ *Options) error {
	err := s.validateHeloIfNeeded()
	if err != nil {
		return apperror.NewError("validating helo before MAIL command failed").AddError(err)
	}

	err = s.server.security.CheckRateLimit(s.remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", s.remoteAddr).
			Field("from", from).
			Msg("MAIL command rate limit exceeded")
		return apperror.NewError("checking rate limit of MAIL command failed").AddError(err)
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
	err := s.validateHeloIfNeeded()
	if err != nil {
		return apperror.NewError("validating helo before RCPT command failed").AddError(err)
	}

	err = s.server.security.CheckRateLimit(s.remoteAddr)
	if err != nil {
		logger.Warn().
			Field("remote_addr", s.remoteAddr).
			Field("to", to).
			Msg("RCPT command rate limit exceeded")
		return apperror.NewError("checking rate limit of RCPT command failed").AddError(err)
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
	err := s.checkData()
	if err != nil {
		return apperror.NewError("checking data command failed").AddError(err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return apperror.NewError("reading email data failed").AddError(err)
	}

	return s.receive(data)
}

// Reset resets the session
func (s *session) Reset() {
	logger.Trace().Msg("SMTP session reset")
	s.from = ""
	s.to = nil
}

// Logout handles session logout
func (s *session) Logout() error {
	s.server.security.CloseConnection(s.remoteAddr)
	logger.Trace().Msg("SMTP session logout")
	return nil
}

// validateHeloIfNeeded validates HELO hostname on first SMTP command
func (s *session) validateHeloIfNeeded() error {
	if s.heloValidated || !s.server.config.Security.HeloValidation {
		return nil
	}

	// The internal SMTP implementation stores this in the connection's Hostname() method
	hostname := s.conn.Hostname()

	err := s.server.security.ValidateHelo(hostname, s.remoteAddr)
	if err != nil {
		return apperror.NewErrorf("validating helo hostname %q failed", hostname).AddError(err)
	}

	s.heloValidated = true
	return nil
}

// checkData makes the checks that come before a message is accepted: HELO and authentication
func (s *session) checkData() error {
	err := s.validateHeloIfNeeded()
	if err != nil {
		return apperror.NewError("validating helo before data failed").AddError(err)
	}

	if s.server.config.Auth && !s.authenticated {
		logger.Warn().Msg("unauthenticated DATA command rejected")
		return ErrAuthRequired
	}
	return nil
}

// receive hands a complete message to the manager and the handlers. The caller has already called
// checkData.
func (s *session) receive(data []byte) error {
	if s.server.manager != nil {
		s.server.manager.NotifyMessageReceived()
	}

	ctx := context.Background()
	s.server.notifyHandlers(ctx, s.from, s.to, data)

	return nil
}

// smtpServer manages SMTP server operations and handles incoming messages
type smtpServer struct {
	config           ServerConfig
	manager          *Manager
	handlers         []NotificationHandler
	running          atomic.Int32
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

// Start starts the SMTP server
func (s *smtpServer) Start(_ context.Context) error {
	if !s.running.CompareAndSwap(0, 1) {
		return apperror.NewError("smtp server is already running")
	}

	serverReady := make(chan error, 1)
	go s.serve(serverReady)
	go s.probeStartup(serverReady)

	return <-serverReady
}

// Stop stops the SMTP server
func (s *smtpServer) Stop(ctx context.Context) error {
	if !s.running.CompareAndSwap(1, 0) {
		return apperror.NewError("smtp server is not running")
	}

	s.cancel()

	s.mutex.Lock()
	if s.listener != nil {
		err := s.listener.Close()
		if err != nil {
			s.mutex.Unlock()
			return apperror.NewError("closing smtp listener failed").AddError(err)
		}
	}
	if !s.closed {
		s.closed = true
		close(s.shutdown)
	}
	s.mutex.Unlock()

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
	return s.running.Load() == 1
}

// ListenAndServe starts the server on the configured address
func (s *smtpServer) ListenAndServe() error {
	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	listener, err := net.Listen(networkTCP, addr)
	if err != nil {
		return apperror.NewErrorf("listening on %s failed", addr).AddError(err)
	}
	return s.Serve(listener)
}

// ListenAndServeTLS starts the server with TLS on the configured address
func (s *smtpServer) ListenAndServeTLS() error {
	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	tlsConfig, err := s.loadTLSConfig()
	if err != nil {
		return apperror.NewError("loading TLS configuration failed").AddError(err)
	}
	listener, err := tls.Listen(networkTCP, addr, tlsConfig)
	if err != nil {
		return apperror.NewErrorf("listening with TLS on %s failed", addr).AddError(err)
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
				return apperror.NewError("accepting connection failed").AddError(err)
			}
		}
		s.protocolWg.Go(func() {
			s.handleConnection(conn)
		})
	}
}

// NewSession creates a new SMTP session (implements Backend interface)
func (s *smtpServer) NewSession(conn *Conn) (Session, error) {
	remoteAddr := conn.RemoteAddr().String()

	logger.Trace().
		Field("remote_addr", remoteAddr).
		Msg("new SMTP session")

	err := s.security.ValidateConnection(remoteAddr)
	if err != nil {
		return nil, apperror.NewErrorf("validating connection from %s failed", remoteAddr).AddError(err)
	}

	return &session{
		server:     s,
		conn:       conn,
		remoteAddr: remoteAddr,
	}, nil
}

// serve runs the SMTP server until it is closed and reports a failure through serverReady
func (s *smtpServer) serve(serverReady chan<- error) {
	var err error
	if s.config.TLS {
		err = s.ListenAndServeTLS()
	} else {
		err = s.ListenAndServe()
	}

	if err != nil && !errors.Is(err, ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		logger.Error().Err(err).Msg("SMTP server error")
		reportStartup(serverReady, err)
	}

	s.running.Store(0)
}

// probeStartup reports through serverReady whether the server accepts connections
func (s *smtpServer) probeStartup(serverReady chan<- error) {
	for range startupProbeAttempts {
		time.Sleep(startupProbeInterval)
		if s.isAcceptingConnections() {
			reportStartup(serverReady, nil)
			return
		}
		if !s.IsRunning() {
			reportStartup(serverReady, apperror.NewError("server stopped before becoming ready"))
			return
		}
	}

	reportStartup(serverReady, apperror.NewError("timeout waiting for server to become ready"))
}

// isAcceptingConnections tries to connect to the server
func (s *smtpServer) isAcceptingConnections() bool {
	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))
	conn, err := net.DialTimeout(networkTCP, addr, startupDialTimeout)
	if err != nil {
		return false
	}

	apperror.Catch(conn.Close, "closing connection failed")
	return true
}

// loadTLSConfig loads or generates TLS configuration
func (s *smtpServer) loadTLSConfig() (*tls.Config, error) {
	tlsConfig := s.config.TLSConfig()

	cert, loaded := s.loadCertificateFiles()
	if loaded {
		tlsConfig.Certificates = []tls.Certificate{cert}
		return tlsConfig, nil
	}

	cert, err := s.generateSelfSignedCert()
	if err != nil {
		return nil, apperror.NewError("generating self-signed certificate failed").AddError(err)
	}

	tlsConfig.Certificates = []tls.Certificate{cert}
	return tlsConfig, nil
}

// loadCertificateFiles loads the configured certificate and key files and reports whether that succeeded.
func (s *smtpServer) loadCertificateFiles() (tls.Certificate, bool) {
	if s.config.CertFile == "" || s.config.KeyFile == "" {
		return tls.Certificate{}, false
	}

	_, err := os.Stat(s.config.CertFile)
	if err != nil {
		return tls.Certificate{}, false
	}

	_, err = os.Stat(s.config.KeyFile)
	if err != nil {
		return tls.Certificate{}, false
	}

	cert, err := tls.LoadX509KeyPair(s.config.CertFile, s.config.KeyFile)
	if err != nil {
		logger.Warn().Err(err).Msg("loading TLS key pair failed, generating a self-signed certificate")
		return tls.Certificate{}, false
	}
	return cert, true
}

// generateSelfSignedCert generates a self-signed certificate
func (s *smtpServer) generateSelfSignedCert() (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, selfSignedKeyBits)
	if err != nil {
		return tls.Certificate{}, apperror.NewError("generating private key failed").AddError(err)
	}

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
		NotAfter:    time.Now().AddDate(selfSignedValidYears, 0, 0),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:    []string{"localhost", s.config.Domain},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, apperror.NewError("creating certificate failed").AddError(err)
	}

	certPEM := &bytes.Buffer{}
	err = pem.Encode(certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err != nil {
		return tls.Certificate{}, apperror.NewError("encoding certificate failed").AddError(err)
	}

	keyPEM := &bytes.Buffer{}
	err = pem.Encode(keyPEM, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err != nil {
		return tls.Certificate{}, apperror.NewError("encoding private key failed").AddError(err)
	}

	if s.config.CertFile != "" && s.config.KeyFile != "" {
		err = os.MkdirAll(filepath.Dir(s.config.CertFile), certDirMode)
		if err != nil {
			logger.Warn().Err(err).Msg("creating certificate directory failed")
			goto createTLS
		}
		err = os.WriteFile(s.config.CertFile, certPEM.Bytes(), certFileMode)
		if err != nil {
			logger.Warn().Err(err).Msg("saving certificate file failed")
		}
		err = os.WriteFile(s.config.KeyFile, keyPEM.Bytes(), certFileMode)
		if err != nil {
			logger.Warn().Err(err).Msg("saving key file failed")
		}
	}

createTLS:
	cert, err := tls.X509KeyPair(certPEM.Bytes(), keyPEM.Bytes())
	if err != nil {
		return tls.Certificate{}, apperror.NewError("creating TLS certificate failed").AddError(err)
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
		case <-time.After(handlerQueueTimeout):
			logger.Warn().
				Field("from", from).
				Field("to", to).
				Field("queue_size", len(s.handlerQueue)).
				Field("queue_capacity", cap(s.handlerQueue)).
				Msg("notification handler queue is full, dropping task")
		case <-ctx.Done():
			logger.Warn().
				Err(ctx.Err()).
				Field("from", from).
				Field("to", to).
				Msg("notification handler cancelled due to context")
			return
		case <-s.ctx.Done():
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
	for i := 0; i < s.config.MaxConcurrentHandlers; i++ {
		s.wg.Add(1)
		go s.runHandlerWorker(i)
	}
}

// runHandlerWorker executes queued notification handler tasks until the server context is cancelled
func (s *smtpServer) runHandlerWorker(workerID int) {
	defer s.wg.Done()

	for {
		select {
		case task := <-s.handlerQueue:
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
}

// handleConnection handles a single SMTP connection
func (s *smtpServer) handleConnection(netConn net.Conn) {
	defer func() {
		err := netConn.Close()
		if err != nil {
			if isConnectionClosed(err) {
				logger.Trace().Err(err).Msg("connection closed")
				return
			}
			logger.Error().Err(err).Msg("closing connection failed")
		}
	}()

	if s.config.ReadTimeout > 0 {
		err := netConn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
		if err != nil {
			if isConnectionClosed(err) {
				logger.Trace().Err(err).Msg("connection closed while setting read deadline")
				return
			}
			logger.Error().Err(err).Msg("setting read deadline failed")
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
			logger.Error().Err(err).Msg("setting write deadline failed")
			return
		}
	}

	conn := NewConn(netConn)
	session, err := s.NewSession(conn)
	if err != nil {
		logger.Warn().Err(err).Field("remote_addr", netConn.RemoteAddr().String()).Msg("connection rejected by security manager")
		s.writeResponse(conn, StatusPermFailure, "connection rejected")
		return
	}
	defer apperror.Catch(session.Logout, "logging out session failed")

	greeting := s.config.Domain + " ESMTP Service Ready"
	if s.config.Domain == "" {
		greeting = "ESMTP Service Ready"
	}
	s.writeResponse(conn, StatusReady, greeting)

	s.handleCommands(conn, session)
}

// handleCommands processes SMTP commands from the client
func (s *smtpServer) handleCommands(conn *Conn, session Session) {
	for {
		if !s.updateReadDeadline(conn) {
			return
		}
		line, ok := s.readCommandLine(conn)
		if !ok {
			return
		}
		if line == "" {
			continue
		}
		if logging.IsEnabled(logger, logging.TraceLevel) {
			logger.Trace().Field("command", line).Field("remote_addr", conn.RemoteAddr()).Msg("received SMTP command")
		}
		parts := strings.SplitN(line, " ", commandSplitParts)
		command := strings.ToUpper(parts[0])
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		switch command {
		case commandHelo:
			s.handleHelo(conn, args, false)
		case commandEhlo:
			s.handleHelo(conn, args, true)
		case commandStartTLS:
			s.handleStartTLS(conn)
		case commandAuth:
			s.handleAuth(conn, session, args)
		case commandMail:
			s.handleMail(conn, session, args)
		case commandRcpt:
			s.handleRcpt(conn, session, args)
		case commandData:
			s.handleData(conn, session)
		case commandRset:
			session.Reset()
			s.writeResponse(conn, StatusOK, "OK")
		case commandNoop:
			s.writeResponse(conn, StatusOK, "OK")
		case commandQuit:
			s.writeResponse(conn, StatusClosing, "Bye")
			return
		default:
			s.writeResponse(conn, StatusNotImplemented, "Command not implemented")
		}
	}
}

// updateReadDeadline extends the read deadline of the connection and reports whether the connection is still usable.
func (s *smtpServer) updateReadDeadline(conn *Conn) bool {
	if s.config.ReadTimeout <= 0 {
		return true
	}

	err := conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
	if err == nil {
		return true
	}

	if isConnectionClosed(err) {
		logger.Trace().Err(err).Field("remote_addr", conn.RemoteAddr()).Msg("connection closed while updating read deadline")
		return false
	}
	logger.Error().Err(err).Field("remote_addr", conn.RemoteAddr()).Msg("updating read deadline failed")
	return false
}

// readCommandLine reads the next line from the client and reports false once the connection ended.
func (s *smtpServer) readCommandLine(conn *Conn) (string, bool) {
	if conn.scanner.Scan() {
		return strings.TrimSpace(conn.scanner.Text()), true
	}

	scanErr := conn.scanner.Err()
	if scanErr == nil {
		return "", false
	}

	if isConnectionClosed(scanErr) {
		logger.Trace().Err(scanErr).Field("remote_addr", conn.RemoteAddr()).Msg("connection closed")
		return "", false
	}
	logger.Error().Err(scanErr).Field("remote_addr", conn.RemoteAddr()).Msg("connection read error")
	return "", false
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
			logger.Warn().Err(err).Msg("setting write deadline failed, continuing anyway")
		}
	}

	_, err := conn.writer.WriteString(data)
	if err != nil {
		if isConnectionClosed(err) {
			logger.Trace().Err(err).Msg("connection closed while writing response")
			return
		}
		logger.Error().Err(err).Msg("writing SMTP response failed")
		return
	}
	err = conn.writer.Flush()
	if err != nil {
		if isConnectionClosed(err) {
			logger.Trace().Err(err).Msg("connection closed while flushing response")
			return
		}
		logger.Error().Err(err).Msg("flushing SMTP response failed")
		return
	}

	logger.Trace().Field("response", strings.TrimSpace(data)).Msg("sent SMTP response")
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
		if s.config.TLS && !conn.TLS() {
			responses = append(responses, commandStartTLS)
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
	parts := strings.SplitN(args, " ", commandSplitParts)
	if len(parts) < 1 {
		s.writeResponse(conn, StatusBadSyntax, "AUTH method required")
		return
	}

	method := strings.ToUpper(parts[0])
	switch method {
	case string(AuthMethodPlain):
		s.handleAuthPlain(conn, session, parts)
	default:
		s.writeResponse(conn, StatusNotImplemented, "Authentication method not supported")
	}
}

// handleAuthPlain handles PLAIN authentication
func (s *smtpServer) handleAuthPlain(conn *Conn, session Session, parts []string) {
	var username, password string

	if len(parts) == commandSplitParts {
		decoded, err := s.decodePlainAuth(parts[1])
		if err != nil {
			s.writeResponse(conn, StatusBadSyntax, "Invalid authentication data")
			return
		}
		username, password = decoded[0], decoded[1]
	} else {
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
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, apperror.NewError("decoding base64 failed").AddError(err)
	}

	// Format: \0username\0password
	parts := strings.Split(string(decoded), plainAuthSeparator)
	if len(parts) != plainAuthParts {
		return nil, apperror.NewError("invalid plain auth format")
	}
	return []string{parts[1], parts[2]}, nil
}

// handleMail handles MAIL command
func (s *smtpServer) handleMail(conn *Conn, session Session, args string) {
	if !strings.HasPrefix(strings.ToUpper(args), mailFromPrefix) {
		s.writeResponse(conn, StatusBadSyntax, "MAIL FROM required")
		return
	}

	from := strings.TrimSpace(args[len(mailFromPrefix):])
	from = strings.Trim(from, addrBrackets)

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
	if !strings.HasPrefix(strings.ToUpper(args), rcptToPrefix) {
		s.writeResponse(conn, StatusBadSyntax, "RCPT TO required")
		return
	}

	to := strings.TrimSpace(args[len(rcptToPrefix):])
	to = strings.Trim(to, addrBrackets)

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
func (s *smtpServer) handleData(conn *Conn, current Session) {
	s.writeResponse(conn, StatusStartData, "Start mail input; end with <CRLF>.<CRLF>")

	// Read message data until a line with a single dot. The limit is advertised in the EHLO response, so it has to
	// hold: without it a client can make the server collect data without end.
	var data []byte
	tooLarge := false
	for {
		if !conn.scanner.Scan() {
			return
		}
		line := conn.scanner.Bytes()
		if len(line) == 1 && line[0] == '.' {
			break
		}
		if tooLarge {
			continue // keep reading to the end of the message, so the connection stays in step
		}
		// Handle dot-stuffing
		if len(line) > 0 && line[0] == '.' {
			line = line[1:]
		}
		if s.config.MaxMessageBytes > 0 && int64(len(data)+len(line)+crlfLen) > s.config.MaxMessageBytes {
			tooLarge = true
			data = nil
			continue
		}
		data = append(data, line...)
		data = append(data, '\r', '\n')
	}

	if tooLarge {
		logger.Warn().Field("remote_addr", conn.RemoteAddr().String()).Field("limit", s.config.MaxMessageBytes).Msg("message exceeds the size limit")
		s.writeResponse(conn, StatusMessageTooLarge, "Message size exceeds fixed maximum message size")
		return
	}

	var err error
	sess, ok := current.(*session)
	if ok {
		// The message is complete in memory already; do not copy it through a reader again
		err = sess.checkData()
		if err == nil {
			err = sess.receive(data)
		}
	} else {
		err = current.Data(bytes.NewReader(data))
	}
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

// reportStartup delivers the first startup result and drops every later one instead of blocking
func reportStartup(serverReady chan<- error, err error) {
	select {
	case serverReady <- err:
	default:
	}
}

// isConnectionClosed checks if an error indicates a normal connection close/reset
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}

	for _, target := range closedConnectionErrors {
		if errors.Is(err, target) {
			return true
		}
	}

	// Windows reports socket failures as winsock errors that do not map to the errors above
	sysErr, ok := errors.AsType[*os.SyscallError](err)
	if ok {
		return sysErr.Syscall == "wsarecv" || sysErr.Syscall == "wsasend"
	}

	return false
}
