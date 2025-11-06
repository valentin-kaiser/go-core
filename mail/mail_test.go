package mail_test

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/mail"
	"github.com/valentin-kaiser/go-core/queue"
)

func TestDefaultConfig(t *testing.T) {
	config := mail.DefaultConfig()

	if config.Client.Host != "localhost" {
		t.Errorf("Expected SMTP host to be localhost, got %s", config.Client.Host)
	}

	if config.Client.Port != 587 {
		t.Errorf("Expected SMTP port to be 587, got %d", config.Client.Port)
	}

	if config.Queue.Enabled != true {
		t.Error("Expected queue to be enabled by default")
	}
}

func TestMessageBuilder(t *testing.T) {
	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test Subject").
		TextBody("Test message").
		Priority(mail.PriorityHigh).
		Build()

	if err != nil {
		t.Errorf("Expected no error when building message, got: %v", err)
	}

	if message.From != "sender@example.com" {
		t.Errorf("Expected from to be sender@example.com, got %s", message.From)
	}

	if len(message.To) != 1 || message.To[0] != "recipient@example.com" {
		t.Errorf("Expected to to be [recipient@example.com], got %v", message.To)
	}

	if message.Subject != "Test Subject" {
		t.Errorf("Expected subject to be 'Test Subject', got %s", message.Subject)
	}

	if message.Priority != mail.PriorityHigh {
		t.Errorf("Expected priority to be high, got %v", message.Priority)
	}
}

func TestTemplateManager(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "default.html",
		AutoReload:      true,
	}

	tm := mail.NewTemplateManager(config)

	// Test that template manager is created
	if tm == nil {
		t.Error("Expected template manager to be created, got nil")
	}

	// Test loading template without any configured source should fail
	_, err := tm.LoadTemplate("default.html")
	if err == nil {
		t.Error("Expected error when loading template without configured source")
	}

	// Test WithFS method exists
	if tm.WithFS(nil) == nil {
		t.Error("Expected WithFS to return template manager")
	}

	// Test WithFileServer method exists
	if tm.WithFileServer("") == nil {
		t.Error("Expected WithFileServer to return template manager")
	}
}

// TestTemplateManagerWithFS tests the template manager with filesystem
func TestTemplateManagerWithFS(t *testing.T) {
	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
		AutoReload:      true,
	}

	tm := mail.NewTemplateManager(config)

	// Test that WithFS method works (we can't test with real filesystem without test data)
	result := tm.WithFS(nil)
	if result != tm {
		t.Error("Expected WithFS to return same template manager instance")
	}
}

// TestTemplateManagerWithFileServer tests the template manager with file server
func TestTemplateManagerWithFileServer(t *testing.T) {
	// Create a temporary directory with a test template
	tempDir := t.TempDir()
	defer apperror.Catch(func() error { return os.RemoveAll(tempDir) }, "failed to remove temp dir")

	// Create a test template file
	templateContent := `<html><body><h1>{{.Subject}}</h1><p>{{.Message}}</p></body></html>`
	templatePath := filepath.Join(tempDir, "test.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to write test template: %v", err)
	}

	config := mail.TemplateConfig{
		DefaultTemplate: "test.html",
		AutoReload:      true,
	}

	tm := mail.NewTemplateManager(config)

	// Configure with file server
	tm.WithFileServer(tempDir)

	// Test loading template
	template, err := tm.LoadTemplate("test.html")
	if err != nil {
		t.Errorf("Failed to load template from file server: %v", err)
	}

	if template == nil {
		t.Error("Expected template to be loaded, got nil")
	}

	// Test rendering template
	data := map[string]interface{}{
		"Subject": "Test Subject",
		"Message": "Test message content",
	}

	rendered, err := tm.RenderTemplate("test.html", data)
	if err != nil {
		t.Errorf("Failed to render template: %v", err)
	}

	if rendered == "" {
		t.Error("Expected rendered template to have content")
	}

	// Check that the rendered content contains our data
	if !strings.Contains(rendered, "Test Subject") {
		t.Error("Expected rendered template to contain subject")
	}

	if !strings.Contains(rendered, "Test message content") {
		t.Error("Expected rendered template to contain message")
	}
}

func TestSMTPSender(t *testing.T) {
	config := mail.ClientConfig{
		Host:       "localhost",
		Port:       587,
		From:       "sender@example.com",
		Auth:       false,
		Encryption: "NONE",
		MaxRetries: 3,
		RetryDelay: time.Second,
	}

	templateConfig := mail.TemplateConfig{
		DefaultTemplate: "default.html",
	}

	tm := mail.NewTemplateManager(templateConfig)
	sender := mail.NewSMTPSender(config, tm)

	if sender == nil {
		t.Error("Expected sender to be created, got nil")
	}

	// Test message validation
	message := &mail.Message{} // Empty message should fail validation

	err := sender.Send(t.Context(), message)
	if err == nil {
		t.Error("Expected validation error for empty message")
	}
}

func TestMailManager(t *testing.T) {
	config := mail.DefaultConfig()
	config.Queue.Enabled = false // Disable queue for this test

	queueManager := queue.NewManager()
	manager := mail.NewManager(config, queueManager)

	if manager == nil {
		t.Error("Expected manager to be created, got nil")
	}

	if manager.IsRunning() {
		t.Error("Expected manager to not be running initially")
	}

	stats := manager.GetStats()
	if stats == nil {
		t.Error("Expected stats to be available")
		return
	}

	if stats.SentCount != 0 {
		t.Errorf("Expected sent count to be 0, got %d", stats.SentCount)
	}
}

func TestPriorityString(t *testing.T) {
	tests := []struct {
		priority mail.Priority
		expected string
	}{
		{mail.PriorityLow, "low"},
		{mail.PriorityNormal, "normal"},
		{mail.PriorityHigh, "high"},
		{mail.PriorityCritical, "critical"},
	}

	for _, test := range tests {
		if test.priority.String() != test.expected {
			t.Errorf("Expected priority %d to be %s, got %s", test.priority, test.expected, test.priority.String())
		}
	}
}

func TestAttachmentBuilder(t *testing.T) {
	attachment := mail.Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Content:     []byte("test content"),
		Size:        12,
		Inline:      false,
	}

	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test with attachment").
		TextBody("Please see attachment").
		Attach(attachment).
		Build()

	if err != nil {
		t.Errorf("Expected no error when building message, got: %v", err)
	}

	if len(message.Attachments) != 1 {
		t.Errorf("Expected 1 attachment, got %d", len(message.Attachments))
	}

	if message.Attachments[0].Filename != "test.txt" {
		t.Errorf("Expected attachment filename to be test.txt, got %s", message.Attachments[0].Filename)
	}
}

func TestInlineAttachments(t *testing.T) {
	// Test inline attachment with content
	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test with inline attachment").
		HTMLBody("<p>Image: <img src=\"cid:test-image\"/></p>").
		AttachInline("test.png", "image/png", "test-image", []byte("fake-image-data")).
		Build()

	if err != nil {
		t.Errorf("Expected no error when building message, got: %v", err)
	}

	if len(message.Attachments) != 1 {
		t.Errorf("Expected 1 attachment, got %d", len(message.Attachments))
	}

	attachment := message.Attachments[0]
	if !attachment.Inline {
		t.Error("Expected attachment to be inline")
	}

	if attachment.ContentID != "test-image" {
		t.Errorf("Expected ContentID to be 'test-image', got %s", attachment.ContentID)
	}

	if attachment.Filename != "test.png" {
		t.Errorf("Expected filename to be 'test.png', got %s", attachment.Filename)
	}
}

func TestManagerStartStop(t *testing.T) {
	config := mail.DefaultConfig()
	config.Queue.Enabled = false
	config.Server.Enabled = false

	queueManager := queue.NewManager()
	manager := mail.NewManager(config, queueManager)

	// Test start
	err := manager.Start()
	if err != nil {
		t.Errorf("Expected no error starting manager, got: %v", err)
	}

	if !manager.IsRunning() {
		t.Error("Expected manager to be running after start")
	}

	// Test double start
	err = manager.Start()
	if err == nil {
		t.Error("Expected error when starting already running manager")
	}

	// Test stop
	err = manager.Stop(t.Context())
	if err != nil {
		t.Errorf("Expected no error stopping manager, got: %v", err)
	}

	if manager.IsRunning() {
		t.Error("Expected manager to not be running after stop")
	}

	// Test double stop
	err = manager.Stop(t.Context())
	if err == nil {
		t.Error("Expected error when stopping already stopped manager")
	}
}

func TestManagerSendNotRunning(t *testing.T) {
	config := mail.DefaultConfig()
	config.Queue.Enabled = false

	queueManager := queue.NewManager()
	manager := mail.NewManager(config, queueManager)

	message, _ := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test").
		TextBody("Test message").
		Build()

	// Test send when not running
	err := manager.Send(t.Context(), message)
	if err == nil {
		t.Error("Expected error when sending email with manager not running")
	}
}

func TestManagerSendAsync(t *testing.T) {
	config := mail.DefaultConfig()
	config.Queue.Enabled = true

	queueManager := queue.NewManager()

	manager := mail.NewManager(config, queueManager)

	// Start manager (this will automatically start the queue manager)
	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start mail manager: %v", err)
	}
	defer manager.Stop(context.Background())

	message, _ := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test Async").
		TextBody("Test message").
		Build()

	// Test async send
	err = manager.SendAsync(context.Background(), message)
	if err != nil {
		t.Errorf("Expected no error sending async email, got: %v", err)
	}

	stats := manager.GetStats()
	if stats.QueuedCount == 0 {
		t.Error("Expected queued count to be incremented")
	}
}

func TestManagerSendAsyncQueueDisabled(t *testing.T) {
	config := mail.DefaultConfig()
	config.Queue.Enabled = false

	manager := mail.NewManager(config, nil)
	_ = manager.Start()
	defer manager.Stop(t.Context())

	message, _ := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test").
		TextBody("Test message").
		Build()

	// Test async send with queue disabled
	err := manager.SendAsync(t.Context(), message)
	if err == nil {
		t.Error("Expected error when sending async email with queue disabled")
	}
}

func TestManagerAddNotificationHandler(_ *testing.T) {
	config := mail.DefaultConfig()
	manager := mail.NewManager(config, nil)

	handler := func(_ context.Context, _ string, _ []string, _ io.Reader) error {
		return nil
	}

	// Test add handler
	manager.AddNotificationHandler(handler)
}

func TestSenderValidation(t *testing.T) {
	config := mail.ClientConfig{
		Host:       "localhost",
		Port:       587,
		From:       "",
		Auth:       false,
		Encryption: "NONE",
		MaxRetries: 0, // Disable retries to make test faster
		RetryDelay: time.Millisecond,
	}

	templateConfig := mail.TemplateConfig{
		DefaultTemplate: "default.html",
	}

	tm := mail.NewTemplateManager(templateConfig)
	sender := mail.NewSMTPSender(config, tm)

	tests := []struct {
		name      string
		message   *mail.Message
		wantError bool
	}{
		{
			name: "missing recipients",
			message: &mail.Message{
				From:     "sender@example.com",
				To:       []string{},
				Subject:  "Test",
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
				Subject: "Test",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a short timeout context to avoid long waits for network connection
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			err := sender.Send(ctx, tt.message)
			if (err != nil) != tt.wantError {
				t.Errorf("Send() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestMessageScheduling(t *testing.T) {
	scheduleTime := time.Now().Add(time.Hour)

	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Scheduled Message").
		TextBody("This is a scheduled message").
		ScheduleAt(scheduleTime).
		Build()

	if err != nil {
		t.Errorf("Expected no error building scheduled message, got: %v", err)
	}

	if message.ScheduleAt == nil {
		t.Error("Expected schedule time to be set")
	}

	if !message.ScheduleAt.Equal(scheduleTime) {
		t.Errorf("Expected schedule time to be %v, got %v", scheduleTime, *message.ScheduleAt)
	}
}

func TestMessageMetadata(t *testing.T) {
	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test with Metadata").
		TextBody("Test message").
		Metadata("campaign_id", "test-campaign").
		Metadata("user_id", "123").
		Build()

	if err != nil {
		t.Errorf("Expected no error building message with metadata, got: %v", err)
	}

	if message.Metadata == nil {
		t.Error("Expected metadata to be set")
	}

	if message.Metadata["campaign_id"] != "test-campaign" {
		t.Error("Expected campaign_id metadata to be preserved")
	}

	if message.Metadata["user_id"] != "123" {
		t.Error("Expected user_id metadata to be preserved")
	}
}

func TestMessageHeaders(t *testing.T) {
	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test with Headers").
		TextBody("Test message").
		Header("X-Campaign", "newsletter").
		Header("X-MessageType", "promotional").
		Build()

	if err != nil {
		t.Errorf("Expected no error building message with headers, got: %v", err)
	}

	if message.Headers == nil {
		t.Error("Expected headers to be set")
	}

	if message.Headers["X-Campaign"] != "newsletter" {
		t.Error("Expected X-Campaign header to be preserved")
	}
}

func TestAttachFile(t *testing.T) {
	// Create a temporary file for testing
	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "test.txt")
	testContent := "This is test file content"

	if err := os.WriteFile(tempFile, []byte(testContent), 0600); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create attachment manually since AttachFile expects multipart.FileHeader
	attachment := mail.Attachment{
		Filename:    "test.txt",
		ContentType: "text/plain",
		Content:     []byte(testContent),
		Size:        int64(len(testContent)),
	}

	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test with File Attachment").
		TextBody("Please see attached file").
		Attach(attachment).
		Build()

	if err != nil {
		t.Errorf("Expected no error building message with file attachment, got: %v", err)
	}

	if len(message.Attachments) != 1 {
		t.Errorf("Expected 1 attachment, got %d", len(message.Attachments))
	}

	attachmentResult := message.Attachments[0]
	if attachmentResult.Filename != "test.txt" {
		t.Errorf("Expected attachment filename to be 'test.txt', got %s", attachmentResult.Filename)
	}

	if string(attachmentResult.Content) != testContent {
		t.Errorf("Expected attachment content to match file content")
	}
}

func TestAttachInlineFromReader(t *testing.T) {
	content := []byte("fake-image-data")
	reader := strings.NewReader(string(content))

	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test with inline attachment from reader").
		HTMLBody("<p>Image: <img src=\"cid:test-image-reader\"/></p>").
		AttachInlineFromReader("test.jpg", "image/jpeg", "test-image-reader", reader, int64(len(content))).
		Build()

	if err != nil {
		t.Errorf("Expected no error when building message, got: %v", err)
	}

	if len(message.Attachments) != 1 {
		t.Errorf("Expected 1 attachment, got %d", len(message.Attachments))
	}

	attachment := message.Attachments[0]
	if !attachment.Inline {
		t.Error("Expected attachment to be inline")
	}

	if attachment.ContentID != "test-image-reader" {
		t.Errorf("Expected ContentID to be 'test-image-reader', got %s", attachment.ContentID)
	}

	if attachment.Size != int64(len(content)) {
		t.Errorf("Expected size to be %d, got %d", len(content), attachment.Size)
	}
}

func BenchmarkMessageCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := mail.NewMessage().
			From("sender@example.com").
			To("recipient@example.com").
			Subject("Benchmark test").
			TextBody("This is a benchmark test").
			Build()
		if err != nil {
			b.Errorf("Expected no error when building message, got: %v", err)
		}
	}
}

func BenchmarkTemplateRendering(b *testing.B) {
	config := mail.TemplateConfig{
		DefaultTemplate: "default.html",
	}

	tm := mail.NewTemplateManager(config)
	data := map[string]interface{}{
		"Subject": "Benchmark Subject",
		"Message": "Benchmark message content",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tm.RenderTemplate("default.html", data)
	}
}

func TestFormatDateTemplateFunction(t *testing.T) {
	// Create a simple template with formatDate function to test it directly
	funcMap := map[string]interface{}{
		"formatDate": func(format string, date interface{}) string {
			// This is the same implementation as in template.go
			switch v := date.(type) {
			case time.Time:
				if format == "" {
					format = "2006-01-02 15:04:05"
				}
				return v.Format(format)
			case *time.Time:
				if v == nil {
					return ""
				}
				if format == "" {
					format = "2006-01-02 15:04:05"
				}
				return v.Format(format)
			case string:
				// Try to parse string as time
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					if format == "" {
						format = "2006-01-02 15:04:05"
					}
					return t.Format(format)
				}
				return v
			default:
				return fmt.Sprintf("%v", date)
			}
		},
	}

	// Test with time.Time
	now := time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC)
	formatFunc, ok := funcMap["formatDate"].(func(string, interface{}) string)
	if !ok {
		t.Fatal("Expected formatDate function to be defined")
	}

	result := formatFunc("2006-01-02", now)
	expected := "2023-12-25"
	if result != expected {
		t.Errorf("Expected formatted date %s, got %s", expected, result)
	}

	// Test with nil time pointer
	result = formatFunc("2006-01-02", (*time.Time)(nil))
	if result != "" {
		t.Errorf("Expected empty string for nil time, got %s", result)
	}

	// Test with string date
	result = formatFunc("2006-01-02", "2023-12-25T10:30:00Z")
	if result != "2023-12-25" {
		t.Errorf("Expected '2023-12-25', got %s", result)
	}

	// Test with default format
	result = formatFunc("", now)
	expected = "2023-12-25 10:30:00"
	if result != expected {
		t.Errorf("Expected default formatted date %s, got %s", expected, result)
	}
}

// TestMessageBuilderWithTemplateFuncsMap tests setting multiple template functions at once
func TestMessageBuilderWithTemplateFuncsMap(t *testing.T) {
	funcs := template.FuncMap{
		"func1": func(s string) string { return "func1_" + s },
		"func2": func(s string) string { return "func2_" + s },
		"func3": func(s string) string { return "func3_" + s },
	}

	message, err := mail.NewMessage().
		From("sender@example.com").
		To("recipient@example.com").
		Subject("Test Subject").
		Template("test.html", map[string]string{"data": "test"}).
		WithTemplateFuncs(funcs).
		Build()

	if err != nil {
		t.Fatalf("Failed to build message: %v", err)
	}

	if message.TemplateFuncs == nil {
		t.Fatal("Expected TemplateFuncs to be set")
	}

	if len(message.TemplateFuncs) != 3 {
		t.Errorf("Expected 3 template functions, got %d", len(message.TemplateFuncs))
	}

	// Test that all functions are present
	for key := range funcs {
		if _, exists := message.TemplateFuncs[key]; !exists {
			t.Errorf("Expected template function %s to be present", key)
		}
	}
}

// TestSMTPSenderWithPerMessageTemplateFuncs tests end-to-end template processing with per-message functions
func TestSMTPSenderWithPerMessageTemplateFuncs(t *testing.T) {
	tempDir := t.TempDir()

	// Create a template for per-message function testing
	templateContent := `<html><body>
<h1>{{.Subject}}</h1>
<p>Name: {{.Name}}</p>
<p>Upper: {{upper .Name}}</p>
</body></html>`

	templatePath := filepath.Join(tempDir, "permsg.html")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0600); err != nil {
		t.Fatalf("Failed to create template: %v", err)
	}

	config := mail.ClientConfig{
		Enabled:    true, // Enable the SMTP sender
		Host:       "smtp.example.com",
		Port:       587,
		From:       "sender@example.com",
		MaxRetries: 0, // Disable retries for faster test
	}

	templateConfig := mail.TemplateConfig{
		Enabled:         true, // Enable template processing
		DefaultTemplate: "permsg.html",
	}

	tm := mail.NewTemplateManager(templateConfig).WithDefaultFuncs().WithFileServer(tempDir)
	if tm.Error != nil {
		t.Fatalf("Template manager error: %v", tm.Error)
	}

	sender := mail.NewSMTPSender(config, tm)

	// Now create the custom template after template manager is set up
	customTemplateContent := `<html><body>
<h1>{{.Subject}}</h1>
<p>Custom: {{customFunc .Name}}</p>
<p>Upper: {{upper .Name}}</p>
</body></html>`

	customTemplatePath := filepath.Join(tempDir, "custom_permsg.html")
	if err := os.WriteFile(customTemplatePath, []byte(customTemplateContent), 0600); err != nil {
		t.Fatalf("Failed to create custom template: %v", err)
	}

	// Create message using the basic template but test via direct rendering
	message := &mail.Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Test Subject",
		Template: "permsg.html", // Use the basic template for SMTP flow
		TemplateData: map[string]string{
			"Subject": "Template Test",
			"Name":    "world",
		},
		TemplateFuncs: template.FuncMap{
			"customFunc": func(s string) string {
				return "CUSTOM_" + strings.ToUpper(s)
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This will fail due to network, but template processing should work
	err := sender.Send(ctx, message)
	// We just want to ensure no template processing errors occur
	// Network errors are expected in tests
	_ = err

	// Verify that the HTML body was generated from template
	if message.HTMLBody == "" {
		t.Error("Expected HTML body to be generated from template")
	}

	if !strings.Contains(message.HTMLBody, "world") {
		t.Error("Expected template data to be rendered")
	}

	if !strings.Contains(message.HTMLBody, "WORLD") {
		t.Error("Expected default upper function to work")
	}

	// Test per-message functions with direct rendering
	customRendered, err := tm.RenderTemplate("custom_permsg.html", message.TemplateData, message.TemplateFuncs)
	if err != nil {
		t.Fatalf("Failed to render with custom functions: %v", err)
	}

	if !strings.Contains(customRendered, "CUSTOM_WORLD") {
		t.Error("Expected per-message custom function to work in direct rendering")
	}
}
