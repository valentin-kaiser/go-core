// Package mail provides comprehensive email sending and SMTP server functionality
// with support for templates, queuing, and notification handling.
//
// Features:
//   - SMTP client for sending emails with various authentication methods
//   - SMTP server for receiving emails with notification handlers
//   - HTML template support with embedded and custom templates
//   - Queue integration for asynchronous email processing
//   - TLS/STARTTLS encryption support
//   - Attachment support
//   - Statistics tracking
//   - Retry mechanisms with exponential backoff
//   - Configurable security features for both client and server (HELO validation, IP filtering, rate limiting)
//
// Example Usage:
//
//	package main
//
//	import (
//		"context"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/mail"
//		"github.com/valentin-kaiser/go-core/queue"
//	)
//
//	func main() {
//		// Create mail configuration
//		config := mail.DefaultConfig()
//		config.Client.Host = "smtp.gmail.com"
//		config.Client.Port = 587
//		config.Client.Username = "your-email@gmail.com"
//		config.Client.Password = "your-password"
//		config.Client.From = "noreply@example.com"
//		config.Client.Auth = true
//		config.Client.Encryption = "STARTTLS"
//
//		// Create queue manager
//		queueManager := queue.NewManager()
//
//		// Create mail manager
//		mailManager := mail.NewManager(config, queueManager)
//
//		// Start the manager
//		ctx := context.Background()
//		if err := mailManager.Start(ctx); err != nil {
//			panic(err)
//		}
//		defer mailManager.Stop(ctx)
//
//		// Send a simple email
//		message := mail.NewMessage().
//			To("recipient@example.com").
//			Subject("Test Email").
//			TextBody("Hello, this is a test email!").
//			Build()
//
//		if err := mailManager.Send(ctx, message); err != nil {
//			panic(err)
//		}
//
//		// Send an email with template
//		templateMessage := mail.NewMessage().
//			To("user@example.com").
//			Subject("Welcome!").
//			Template("welcome.html", map[string]interface{}{
//				"FirstName":     "John",
//				"ActivationURL": "https://example.com/activate?token=abc123",
//			}).
//			Build()
//
//		if err := mailManager.SendAsync(ctx, templateMessage); err != nil {
//			panic(err)
//		}
//	}
//
// SMTP Server Example:
//
//	config := mail.DefaultConfig()
//	config.Server.Enabled = true
//	config.Server.Host = "localhost"
//	config.Server.Port = 2525
//	config.Server.Domain = "example.com"
//	config.Server.Auth = true
//	config.Server.Username = "admin"
//	config.Server.Password = "password"
//
//	mailManager := mail.NewManager(config, nil)
//
//	// Add notification handler
//	mailManager.AddNotificationHandler(func(ctx context.Context, from string, to []string, data []byte) error {
//		log.Printf("Received email from %s to %v", from, to)
//		return nil
//	})
//
//	if err := mailManager.Start(ctx); err != nil {
//		panic(err)
//	}
package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
	"github.com/valentin-kaiser/go-core/queue"
)

var logger = logging.GetPackageLogger("mail")

// Manager manages email sending and SMTP server functionality
type Manager struct {
	config          *Config
	sender          Sender
	server          Server
	TemplateManager *TemplateManager
	queueManager    *queue.Manager
	stats           *Stats
	statsMutex      sync.RWMutex
	running         int32
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

// NewManager creates a new mail manager with the specified configuration and queue manager.
// The mail manager handles email sending operations, template processing, and integrates
// with a queue system for asynchronous email delivery. It sets up SMTP senders,
// template managers, and provides features like retry logic, delivery tracking, and statistics.
//
// Example usage:
//
//	config := &mail.Config{
//		Client: mail.ClientConfig{
//			Host:     "smtp.gmail.com",
//			Port:     587,
//			Username: "your-email@gmail.com",
//			Password: "your-password",
//		},
//	}
//	queueMgr := queue.NewManager()
//	mailMgr := mail.NewManager(config, queueMgr)
func NewManager(config *Config, queueManager *queue.Manager) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	manager := &Manager{
		config:       config,
		queueManager: queueManager,
		stats:        &Stats{},
		ctx:          ctx,
		cancel:       cancel,
	}

	manager.TemplateManager = NewTemplateManager(config.Templates)
	manager.sender = NewSMTPSender(config.Client, manager.TemplateManager)

	if config.Server.Enabled {
		manager.server = NewSMTPServer(config.Server, manager)
	}

	return manager
}

// Start starts the mail manager
func (m *Manager) Start() error {
	if !atomic.CompareAndSwapInt32(&m.running, 0, 1) {
		return apperror.NewError("mail manager is already running")
	}

	if m.config.Queue.Enabled && m.queueManager != nil {
		err := m.queueManager.Start(m.ctx)
		if err != nil {
			atomic.StoreInt32(&m.running, 0)
			return apperror.Wrap(err)
		}
		m.queueManager.RegisterHandler("mail", m.handleMailJob)
	}

	if m.config.Server.Enabled && m.server != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			err := m.server.Start(m.ctx)
			if err != nil {
				logger.Error().Err(err).Msg("failed to start SMTP server")
			}
		}()

		logger.Info().Msgf("SMTP server started on %s:%d", m.config.Server.Host, m.config.Server.Port)
	}

	return nil
}

// Stop stops the mail manager
func (m *Manager) Stop(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&m.running, 1, 0) {
		return apperror.NewError("mail manager is not running")
	}

	m.cancel()
	if m.server != nil && m.server.IsRunning() {
		err := m.server.Stop(ctx)
		if err != nil {
			logger.Error().Err(err).Msg("failed to stop SMTP server")
		}
	}

	if m.config.Queue.Enabled && m.queueManager != nil {
		err := m.queueManager.Stop()
		if err != nil {
			logger.Error().Err(err).Msg("failed to stop queue manager")
		}
	}

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		logger.Warn().Msg("mail manager stop timed out")
		return ctx.Err()
	}
}

// Send sends an email message synchronously
func (m *Manager) Send(ctx context.Context, message *Message) error {
	if !m.IsRunning() {
		return apperror.NewError("mail manager is not running")
	}

	// Generate ID if not set
	if message.ID == "" {
		message.ID = uuid.New().String()
	}

	logger.Debug().
		Field("id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("sending email")

	// Send the message
	err := m.sender.Send(ctx, message)
	if err != nil {
		m.incrementFailedCount()
		return apperror.Wrap(err)
	}

	m.incrementSentCount()
	m.updateLastSent()

	logger.Info().
		Field("id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("email sent successfully")

	return nil
}

// SendAsync sends an email message asynchronously using the queue
func (m *Manager) SendAsync(ctx context.Context, message *Message) error {
	if !m.IsRunning() {
		return apperror.NewError("mail manager is not running")
	}

	if !m.config.Queue.Enabled || m.queueManager == nil {
		return apperror.NewError("queue is not enabled")
	}

	// Generate ID if not set
	if message.ID == "" {
		message.ID = uuid.New().String()
	}

	logger.Debug().
		Field("id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("queuing email for async sending")

	// Create queue job
	jobData := map[string]interface{}{
		"id":            message.ID,
		"from":          message.From,
		"to":            message.To,
		"cc":            message.CC,
		"bcc":           message.BCC,
		"reply_to":      message.ReplyTo,
		"subject":       message.Subject,
		"text_body":     message.TextBody,
		"html_body":     message.HTMLBody,
		"template":      message.Template,
		"template_data": message.TemplateData,
		"attachments":   message.Attachments,
		"headers":       message.Headers,
		"priority":      int(message.Priority),
		"created_at":    message.CreatedAt,
		"schedule_at":   message.ScheduleAt,
		"metadata":      message.Metadata,
	}

	job := queue.NewJob("mail").
		WithID(message.ID).
		WithPayload(jobData).
		WithPriority(queue.Priority(message.Priority)).
		WithMaxAttempts(m.config.Queue.MaxAttempts)

	// Schedule the job if specified
	if message.ScheduleAt != nil {
		job = job.WithScheduleAt(*message.ScheduleAt)
	}

	// Enqueue the job
	err := m.queueManager.Enqueue(ctx, job.Build())
	if err != nil {
		return apperror.Wrap(err)
	}

	m.incrementQueuedCount()

	logger.Debug().
		Field("id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("email queued for async sending")

	return nil
}

// AddNotificationHandler adds a notification handler to the SMTP server
func (m *Manager) AddNotificationHandler(handler NotificationHandler) error {
	if m.server == nil {
		return apperror.NewError("SMTP server is not configured")
	}

	m.server.AddHandler(handler)
	return nil
}

// GetStats returns the current mail statistics
func (m *Manager) GetStats() *Stats {
	m.statsMutex.RLock()
	defer m.statsMutex.RUnlock()

	// Create a copy to avoid race conditions
	stats := &Stats{
		SentCount:     m.stats.SentCount,
		FailedCount:   m.stats.FailedCount,
		QueuedCount:   m.stats.QueuedCount,
		ReceivedCount: m.stats.ReceivedCount,
	}

	if m.stats.LastSent != nil {
		lastSent := *m.stats.LastSent
		stats.LastSent = &lastSent
	}

	if m.stats.LastReceived != nil {
		lastReceived := *m.stats.LastReceived
		stats.LastReceived = &lastReceived
	}

	return stats
}

// IsRunning returns true if the mail manager is running
func (m *Manager) IsRunning() bool {
	return atomic.LoadInt32(&m.running) == 1
}

// SendTestEmail sends a test email
func (m *Manager) SendTestEmail(ctx context.Context, to string) error {
	testMessage := &Message{
		From:     m.config.Client.From,
		To:       []string{to},
		Subject:  "Test Email",
		TextBody: "This is a test email from the mail manager.",
		HTMLBody: "<p>This is a test email from the mail manager.</p>",
	}

	return m.Send(ctx, testMessage)
}

// handleMailJob handles queued mail jobs
func (m *Manager) handleMailJob(ctx context.Context, job *queue.Job) error {
	logger.Debug().Field("job_id", job.ID).Msg("processing mail job")

	// Decode the message from job payload
	var jobData map[string]interface{}
	err := json.Unmarshal(job.Payload, &jobData)
	if err != nil {
		return apperror.Wrap(err)
	}

	// Convert job data back to message
	message, err := m.jobDataToMessage(jobData)
	if err != nil {
		return apperror.Wrap(err)
	}

	// Send the message
	err = m.sender.Send(ctx, message)
	if err != nil {
		m.incrementFailedCount()
		return apperror.Wrap(err)
	}

	m.incrementSentCount()
	m.updateLastSent()
	m.decrementQueuedCount()

	logger.Info().
		Field("job_id", job.ID).
		Field("message_id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("queued email sent successfully")

	return nil
}

// NotifyMessageReceived notifies that a message was received by the SMTP server
func (m *Manager) NotifyMessageReceived() {
	m.incrementReceivedCount()
	m.updateLastReceived()
}

// incrementSentCount increments the sent count
func (m *Manager) incrementSentCount() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	m.stats.SentCount++
}

// incrementFailedCount increments the failed count
func (m *Manager) incrementFailedCount() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	m.stats.FailedCount++
}

// incrementQueuedCount increments the queued count
func (m *Manager) incrementQueuedCount() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	m.stats.QueuedCount++
}

// decrementQueuedCount decrements the queued count
func (m *Manager) decrementQueuedCount() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	if m.stats.QueuedCount > 0 {
		m.stats.QueuedCount--
	}
}

// incrementReceivedCount increments the received count
func (m *Manager) incrementReceivedCount() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	m.stats.ReceivedCount++
}

// updateLastSent updates the last sent timestamp
func (m *Manager) updateLastSent() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	now := time.Now()
	m.stats.LastSent = &now
}

// updateLastReceived updates the last received timestamp
func (m *Manager) updateLastReceived() {
	m.statsMutex.Lock()
	defer m.statsMutex.Unlock()
	now := time.Now()
	m.stats.LastReceived = &now
}

// jobDataToMessage converts job data back to a Message
func (m *Manager) jobDataToMessage(jobData map[string]interface{}) (*Message, error) {
	message := &Message{}

	id, ok := jobData["id"].(string)
	if ok {
		message.ID = id
	}
	from, ok := jobData["from"].(string)
	if ok {
		message.From = from
	}
	to, ok := jobData["to"]
	if ok {
		message.To = convertToStringSlice(to)
	}
	cc, ok := jobData["cc"]
	if ok {
		message.CC = convertToStringSlice(cc)
	}
	bcc, ok := jobData["bcc"]
	if ok {
		message.BCC = convertToStringSlice(bcc)
	}
	replyTo, ok := jobData["reply_to"].(string)
	if ok {
		message.ReplyTo = replyTo
	}
	subject, ok := jobData["subject"].(string)
	if ok {
		message.Subject = subject
	}
	textBody, ok := jobData["text_body"].(string)
	if ok {
		message.TextBody = textBody
	}
	htmlBody, ok := jobData["html_body"].(string)
	if ok {
		message.HTMLBody = htmlBody
	}
	template, ok := jobData["template"].(string)
	if ok {
		message.Template = template
	}
	templateData, ok := jobData["template_data"]
	if ok {
		message.TemplateData = templateData
	}
	priority, ok := jobData["priority"].(float64)
	if ok {
		message.Priority = Priority(int(priority))
	}
	if headers, ok := jobData["headers"].(map[string]interface{}); ok {
		message.Headers = make(map[string]string)
		for k, v := range headers {
			s, ok := v.(string)
			if ok {
				message.Headers[k] = s
			}
		}
	}
	if metadata, ok := jobData["metadata"].(map[string]interface{}); ok {
		message.Metadata = make(map[string]string)
		for k, v := range metadata {
			s, ok := v.(string)
			if ok {
				message.Metadata[k] = s
			}
		}
	}
	if createdAt, ok := jobData["created_at"].(string); ok {
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, apperror.NewError("invalid created_at format").AddError(err)
		}
		message.CreatedAt = t
	}
	if scheduleAt, ok := jobData["schedule_at"].(string); ok && scheduleAt != "" {
		t, err := time.Parse(time.RFC3339, scheduleAt)
		if err != nil {
			return nil, apperror.NewError("invalid schedule_at format").AddError(err)
		}
		message.ScheduleAt = &t
	}
	if attachments, ok := jobData["attachments"].([]interface{}); ok {
		message.Attachments = make([]Attachment, 0, len(attachments))
		for _, att := range attachments {
			if attMap, ok := att.(map[string]interface{}); ok {
				attachment := Attachment{}
				filename, ok := attMap["filename"].(string)
				if ok {
					attachment.Filename = filename
				}
				contentType, ok := attMap["content_type"].(string)
				if ok {
					attachment.ContentType = contentType
				}
				content, ok := attMap["content"].(string)
				if ok {
					decoded, err := base64.StdEncoding.DecodeString(content)
					if err != nil {
						return nil, apperror.NewError("failed to decode attachment content").AddError(err)
					}
					attachment.Content = decoded
				}
				size, ok := attMap["size"].(float64)
				if ok {
					attachment.Size = int64(size)
				}
				inline, ok := attMap["inline"].(bool)
				if ok {
					attachment.Inline = inline
				}
				contentID, ok := attMap["content_id"].(string)
				if ok {
					attachment.ContentID = contentID
				}
				message.Attachments = append(message.Attachments, attachment)
			}
		}
	}

	return message, nil
}

// convertToStringSlice safely converts various types to []string
// This handles different JSON unmarshaling scenarios for string slice fields
func convertToStringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		// Direct string slice (ideal case)
		return v
	case []interface{}:
		// JSON unmarshaled as interface slice
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	case string:
		// Single string value
		return []string{v}
	case nil:
		// Nil value
		return nil
	default:
		// Fallback: try to convert to string and return as single-item slice
		if str := convertToString(value); str != "" {
			return []string{str}
		}
		return nil
	}
}

// convertToString safely converts various types to string
func convertToString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
