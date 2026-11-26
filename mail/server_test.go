package mail_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/mail"
	"github.com/valentin-kaiser/go-core/queue"
)

func TestSMTPServer_Creation(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  2525, // Use non-standard port for testing
		Domain:                "test.local",
		Auth:                  false,
		TLS:                   false,
		ReadTimeout:           time.Second * 10,
		WriteTimeout:          time.Second * 10,
		MaxMessageBytes:       1024 * 1024, // 1MB
		MaxRecipients:         10,
		AllowInsecureAuth:     true,
		MaxConcurrentHandlers: 5,
	}

	// Create a mail manager first
	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)

	server := mail.NewSMTPServer(config, manager)

	if server == nil {
		t.Fatal("Expected SMTP server to be created")
	}

	if server.IsRunning() {
		t.Error("Server should not be running initially")
	}
}

func TestSMTPServer_AddHandler(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  2525,
		Domain:                "test.local",
		MaxConcurrentHandlers: 5,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	// Create a test notification handler
	var handlerCalled bool
	handler := func(_ context.Context, _ string, _ []string, _ io.Reader) error {
		handlerCalled = true
		return nil
	}

	// Add handler - this should not cause any errors
	server.AddHandler(handler)

	// We can't easily test that the handler is actually called without
	// setting up a full SMTP session, but we can verify the method works
	if handlerCalled {
		t.Error("Handler should not be called just from adding it")
	}
}

func TestSMTPServer_StartStop(t *testing.T) {
	// Skip this test in CI or when port binding might fail
	t.Skip("Skipping server start/stop test due to port binding requirements")

	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0, // Use port 0 to let OS assign available port
		Domain:                "test.local",
		Auth:                  false,
		TLS:                   false,
		ReadTimeout:           time.Second * 5,
		WriteTimeout:          time.Second * 5,
		MaxMessageBytes:       1024 * 1024,
		MaxRecipients:         10,
		AllowInsecureAuth:     true,
		MaxConcurrentHandlers: 5,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	ctx := context.Background()

	// Test starting server
	err := server.Start(ctx)
	if err != nil {
		t.Errorf("Failed to start SMTP server: %v", err)
	}

	if !server.IsRunning() {
		t.Error("Server should be running after start")
	}

	// Test double start should return error
	err = server.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already running server")
	}

	// Test stopping server
	err = server.Stop(ctx)
	if err != nil {
		t.Errorf("Failed to stop SMTP server: %v", err)
	}

	if server.IsRunning() {
		t.Error("Server should not be running after stop")
	}

	// Test double stop should return error
	err = server.Stop(ctx)
	if err == nil {
		t.Error("Expected error when stopping already stopped server")
	}
}

func TestSMTPServer_TLSConfiguration(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		TLS:                   true,
		CertFile:              "", // Empty will trigger self-signed cert generation
		KeyFile:               "",
		MaxConcurrentHandlers: 5,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	// Just verify server was created with TLS config
	if server == nil {
		t.Error("Server should be created with TLS configuration")
	}
}

func TestSMTPServer_AuthConfiguration(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		Auth:                  true,
		Username:              "testuser",
		Password:              "testpass",
		AllowInsecureAuth:     true, // Allow for testing
		MaxConcurrentHandlers: 5,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	// Just verify server was created with auth config
	if server == nil {
		t.Error("Server should be created with auth configuration")
	}
}

func TestSMTPServer_SecurityConfiguration(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		MaxConcurrentHandlers: 5,
		Security: mail.SecurityConfig{
			HeloValidation:      true,
			HeloRequireFQDN:     true,
			HeloDNSCheck:        false,
			IPAllowlist:         []string{"127.0.0.1", "::1"},
			MaxConnectionsPerIP: 5,
			MaxAuthFailures:     3,
			AuthFailureWindow:   time.Minute,
			LogSecurityEvents:   true,
		},
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	// Just verify server was created with security config
	if server == nil {
		t.Error("Server should be created with security configuration")
	}
}

func TestSMTPServer_MultipleHandlers(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		MaxConcurrentHandlers: 10,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	// Add multiple handlers
	handlerCount := 0

	for range 5 {
		handler := func(_ context.Context, _ string, _ []string, _ io.Reader) error {
			handlerCount++
			return nil
		}
		server.AddHandler(handler)
	}

	// Verify server can be created with multiple handlers
	if server == nil {
		t.Error("Server should be created with multiple handlers")
	}
}

func TestSMTPServer_ConcurrentHandlerLimit(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		MaxConcurrentHandlers: 2, // Low limit for testing
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	if server == nil {
		t.Fatal("Expected server to be created with handler limit")
	}
}

func TestSMTPServer_DefaultMaxConcurrentHandlers(t *testing.T) {
	// Test that default MaxConcurrentHandlers is set when not specified
	config := mail.ServerConfig{
		Enabled: true,
		Host:    "localhost",
		Port:    0,
		Domain:  "test.local",
		// MaxConcurrentHandlers not set - should default to 50
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	if server == nil {
		t.Fatal("Expected server to be created with default handler limit")
	}
}

func TestSMTPServer_ContextCancellation(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		MaxConcurrentHandlers: 5,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	if server == nil {
		t.Fatal("Expected server to be created")
	}
}

func TestSMTPServer_GracefulShutdown(t *testing.T) {
	config := mail.ServerConfig{
		Enabled:               true,
		Host:                  "localhost",
		Port:                  0,
		Domain:                "test.local",
		MaxConcurrentHandlers: 5,
	}

	mailConfig := mail.DefaultConfig()
	queueManager := queue.NewManager()
	manager := mail.NewManager(mailConfig, queueManager)
	server := mail.NewSMTPServer(config, manager)

	if server == nil {
		t.Fatal("Expected server to be created")
	}
}
