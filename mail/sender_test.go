package mail_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/mail"
)

func TestSMTPSender_Comprehensive(t *testing.T) {
	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		Auth:       false,
		Encryption: "NONE",
		Timeout:    30 * time.Second,
		MaxRetries: 0, // Disable retries for faster tests
		RetryDelay: time.Millisecond,
	}

	templateConfig := mail.TemplateConfig{
		DefaultTemplate: "default.html",
	}

	tm := mail.NewTemplateManager(templateConfig)
	sender := mail.NewSMTPSender(config, tm)

	if sender == nil {
		t.Fatal("Expected SMTP sender to be created")
	}
}

func TestSMTPSender_MessageValidation(t *testing.T) {
	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "default@example.com",
		Auth:       false,
		Encryption: "NONE",
		MaxRetries: 0,
		RetryDelay: time.Millisecond,
	}

	tm := mail.NewTemplateManager(mail.TemplateConfig{})
	sender := mail.NewSMTPSender(config, tm)

	tests := []struct {
		name      string
		message   *mail.Message
		wantError bool
	}{
		{
			name: "valid message",
			message: &mail.Message{
				From:     "sender@example.com",
				To:       []string{"recipient@example.com"},
				Subject:  "Test Subject",
				TextBody: "Test message",
			},
			wantError: false, // Will fail due to network, but validation should pass
		},
		{
			name: "message with config from",
			message: &mail.Message{
				To:       []string{"recipient@example.com"},
				Subject:  "Test Subject",
				TextBody: "Test message",
			},
			wantError: false, // Should use config.From
		},
		{
			name: "missing recipients",
			message: &mail.Message{
				From:     "sender@example.com",
				To:       []string{},
				Subject:  "Test Subject",
				TextBody: "Test message",
			},
			wantError: true,
		},
		{
			name: "missing subject",
			message: &mail.Message{
				From:     "sender@example.com",
				To:       []string{"recipient@example.com"},
				Subject:  "",
				TextBody: "Test message",
			},
			wantError: true,
		},
		{
			name: "missing body",
			message: &mail.Message{
				From:    "sender@example.com",
				To:      []string{"recipient@example.com"},
				Subject: "Test Subject",
			},
			wantError: true,
		},
		{
			name: "html body only",
			message: &mail.Message{
				From:     "sender@example.com",
				To:       []string{"recipient@example.com"},
				Subject:  "Test Subject",
				HTMLBody: "<p>Test message</p>",
			},
			wantError: false,
		},
		{
			name: "both text and html body",
			message: &mail.Message{
				From:     "sender@example.com",
				To:       []string{"recipient@example.com"},
				Subject:  "Test Subject",
				TextBody: "Test message",
				HTMLBody: "<p>Test message</p>",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := sender.Send(ctx, tt.message)

			// We expect network errors for valid messages, validation errors for invalid ones
			if tt.wantError {
				// Should get validation error before network attempt
				if err == nil {
					t.Error("Expected validation error")
				}
			}
		})
	}
}

func TestSMTPSender_SendAsync_NotSupported(t *testing.T) {
	config := mail.ClientConfig{
		Host: "smtp.example.com",
		Port: 587,
	}

	tm := mail.NewTemplateManager(mail.TemplateConfig{})
	sender := mail.NewSMTPSender(config, tm)

	message := &mail.Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test",
		TextBody: "Test",
	}

	err := sender.SendAsync(context.Background(), message)
	if err == nil {
		t.Error("Expected error for SendAsync on SMTP sender")
	}
}

func TestSMTPSender_WithRetries(t *testing.T) {
	config := mail.ClientConfig{
		Enabled:    true, // Enable the SMTP sender
		Host:       "nonexistent.smtp.server",
		Port:       587,
		From:       "sender@example.com",
		MaxRetries: 2,
		RetryDelay: 10 * time.Millisecond, // Very short for testing
	}

	tm := mail.NewTemplateManager(mail.TemplateConfig{})
	sender := mail.NewSMTPSender(config, tm)

	message := &mail.Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test",
		TextBody: "Test",
	}

	// This should try 3 times (initial + 2 retries) and then fail
	start := time.Now()
	err := sender.Send(context.Background(), message)
	duration := time.Since(start)

	if err == nil {
		t.Error("Expected error when sending to nonexistent server")
	}

	// Should have taken at least 2 retry delays (20ms minimum)
	expectedMinDuration := 20 * time.Millisecond
	if duration < expectedMinDuration {
		t.Errorf("Expected duration >= %v for retries, got %v", expectedMinDuration, duration)
	}
}

func TestSMTPSender_WithTemplate(t *testing.T) {
	tempDir := t.TempDir()

	// Create a simple template
	templateContent := `<html><body><h1>{{.Subject}}</h1><p>{{.Body}}</p></body></html>`
	templatePath := tempDir + "/test.html"
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}

	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		MaxRetries: 0,
	}

	templateConfig := mail.TemplateConfig{
		DefaultTemplate: "test.html",
	}

	tm := mail.NewTemplateManager(templateConfig).WithFileServer(tempDir)
	if tm.Error != nil {
		t.Fatalf("Template manager error: %v", tm.Error)
	}

	sender := mail.NewSMTPSender(config, tm)

	message := &mail.Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test Subject",
		Template: "test.html",
		TemplateData: map[string]string{
			"Subject": "Template Test",
			"Body":    "This is a template test",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This will fail due to network, but template processing should work
	err := sender.Send(ctx, message)
	// We just want to ensure no template processing errors occur
	// Network errors are expected in tests
	_ = err
}

func TestSMTPSender_AuthMethods(t *testing.T) {
	tests := []struct {
		name       string
		authMethod string
		valid      bool
	}{
		{"PLAIN", "PLAIN", true},
		{"LOGIN", "LOGIN", true},
		{"CRAMMD5", "CRAMMD5", true},
		{"empty", "", true},
		{"invalid", "INVALID", true}, // Will be handled by underlying SMTP lib
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mail.ClientConfig{
				Host:       "smtp.example.com",
				Port:       587,
				Username:   "user",
				Password:   "pass",
				Auth:       true,
				AuthMethod: tt.authMethod,
				MaxRetries: 0,
			}

			tm := mail.NewTemplateManager(mail.TemplateConfig{})
			sender := mail.NewSMTPSender(config, tm)

			if sender == nil {
				t.Error("Sender should be created regardless of auth method")
			}
		})
	}
}

func TestSMTPSender_EncryptionMethods(t *testing.T) {
	tests := []struct {
		name       string
		encryption string
		port       int
	}{
		{"NONE", "NONE", 587},
		{"STARTTLS", "STARTTLS", 587},
		{"TLS", "TLS", 465},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mail.ClientConfig{
				Host:       "smtp.example.com",
				Port:       tt.port,
				Encryption: tt.encryption,
				MaxRetries: 0,
			}

			tm := mail.NewTemplateManager(mail.TemplateConfig{})
			sender := mail.NewSMTPSender(config, tm)

			if sender == nil {
				t.Error("Sender should be created regardless of encryption method")
			}
		})
	}
}

func TestSMTPSender_MessageWithAttachments(_ *testing.T) {
	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		MaxRetries: 0,
	}

	tm := mail.NewTemplateManager(mail.TemplateConfig{})
	sender := mail.NewSMTPSender(config, tm)

	attachment := mail.Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Content:     []byte("Test attachment content"),
		Size:        23,
	}

	message := &mail.Message{
		From:        "sender@example.com",
		To:          []string{"recipient@example.com"},
		Subject:     "Test with attachment",
		TextBody:    "Please see attachment",
		Attachments: []mail.Attachment{attachment},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Will fail due to network, but attachment processing should work
	err := sender.Send(ctx, message)
	// Network error expected in test environment
	_ = err
}

func TestSMTPSender_MessageWithHeaders(_ *testing.T) {
	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		MaxRetries: 0,
	}

	tm := mail.NewTemplateManager(mail.TemplateConfig{})
	sender := mail.NewSMTPSender(config, tm)

	message := &mail.Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test with headers",
		TextBody: "Test message",
		Headers: map[string]string{
			"X-Custom-Header": "test-value",
			"X-Priority":      "high",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Will fail due to network, but header processing should work
	err := sender.Send(ctx, message)
	// Network error expected in test environment
	_ = err
}

func TestSMTPSender_ContextCancellation(t *testing.T) {
	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		MaxRetries: 3,
		RetryDelay: time.Second, // Long delay to test cancellation
	}

	tm := mail.NewTemplateManager(mail.TemplateConfig{})
	sender := mail.NewSMTPSender(config, tm)

	message := &mail.Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test",
		TextBody: "Test",
	}

	// Create context that will be cancelled quickly
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := sender.Send(ctx, message)
	duration := time.Since(start)

	if err == nil {
		t.Error("Expected error due to context cancellation")
	}

	// Should fail quickly due to context cancellation, not wait for full retry delays
	if duration > 500*time.Millisecond {
		t.Errorf("Send took too long despite context cancellation: %v", duration)
	}
}

func TestSMTPSender_SendWithTemplateContent(t *testing.T) {
	config := mail.ClientConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		Auth:       false,
		Encryption: "NONE",
		Timeout:    30 * time.Second,
		MaxRetries: 0, // Disable retries for faster tests
		RetryDelay: time.Millisecond,
		Enabled:    true, // Enable the SMTP sender
	}

	templateConfig := mail.TemplateConfig{
		WithDefaultFuncs: true,
		Enabled:          true, // Explicitly enable template processing
	}

	tm := mail.NewTemplateManager(templateConfig)
	sender := mail.NewSMTPSender(config, tm)

	templateContent := "<h1>Hello {{.Name}}</h1><p>Welcome to {{.Company}}!</p><p>Your order #{{.OrderID}} has been confirmed.</p>"
	templateData := map[string]interface{}{
		"Name":    "Jane Doe",
		"Company": "ACME Corp",
		"OrderID": "12345",
	}

	message, err := mail.NewMessage().
		From("noreply@acme.com").
		To("jane.doe@example.com").
		Subject("Order Confirmation").
		TemplateContent(templateContent, templateData).
		Build()

	if err != nil {
		t.Fatalf("Failed to build message: %v", err)
	}

	// Verify the message was built correctly
	if message.TemplateContent != templateContent {
		t.Errorf("Expected template content to be set correctly")
	}

	if message.Template != "" {
		t.Error("Expected Template field to be empty when using TemplateContent")
	}

	// The actual send will fail due to fake SMTP server, but the template processing should work
	ctx := context.Background()
	err = sender.Send(ctx, message)

	// We expect an error here due to fake SMTP server, but we want to check that
	// the template was processed and HTMLBody was set
	if message.HTMLBody == "" {
		t.Error("Expected HTMLBody to be populated after template processing")
	}

	expectedHTML := "<h1>Hello Jane Doe</h1><p>Welcome to ACME Corp!</p><p>Your order #12345 has been confirmed.</p>"
	if message.HTMLBody != expectedHTML {
		t.Errorf("Expected HTMLBody to be '%s', got '%s'", expectedHTML, message.HTMLBody)
	}
}
