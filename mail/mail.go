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
//		"github.com/valentin-kaiser/go-core/v2/mail"
//		"github.com/valentin-kaiser/go-core/v2/queue"
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
	"github.com/valentin-kaiser/go-core/v2/apperror"
	"github.com/valentin-kaiser/go-core/v2/logging"
	"github.com/valentin-kaiser/go-core/v2/queue"
)

// Queue job constants.
const (
	jobTypeMail = "mail"

	jobKeyID           = "id"
	jobKeyFrom         = "from"
	jobKeyTo           = "to"
	jobKeyCC           = "cc"
	jobKeyBCC          = "bcc"
	jobKeyReplyTo      = "reply_to"
	jobKeySubject      = "subject"
	jobKeyTextBody     = "text_body"
	jobKeyHTMLBody     = "html_body"
	jobKeyTemplate     = "template"
	jobKeyTemplateData = "template_data"
	jobKeyAttachments  = "attachments"
	jobKeyHeaders      = "headers"
	jobKeyPriority     = "priority"
	jobKeyCreatedAt    = "created_at"
	jobKeyScheduleAt   = "schedule_at"
	jobKeyMetadata     = "metadata"

	attachmentKeyFilename    = "filename"
	attachmentKeyContentType = "content_type"
	attachmentKeyContent     = "content"
	attachmentKeySize        = "size"
	attachmentKeyInline      = "inline"
	attachmentKeyContentID   = "content_id"
)

var logger = logging.GetPackageLogger("mail")

// Stats represents mail statistics
type Stats struct {
	// SentCount is the number of emails sent
	SentCount int64 `json:"sent_count"`

	// FailedCount is the number of emails that failed to send
	FailedCount int64 `json:"failed_count"`

	// QueuedCount is the number of emails currently queued
	QueuedCount int64 `json:"queued_count"`

	// ReceivedCount is the number of emails received by the server
	ReceivedCount int64 `json:"received_count"`

	// LastSent is the timestamp of the last sent email
	LastSent *time.Time `json:"last_sent,omitempty"`

	// LastReceived is the timestamp of the last received email
	LastReceived *time.Time `json:"last_received,omitempty"`
}

// Manager manages email sending and SMTP server functionality
type Manager struct {
	config          *Config
	sender          Sender
	server          Server
	TemplateManager *TemplateManager
	queueManager    *queue.Manager
	stats           *Stats
	statsMutex      sync.RWMutex
	running         atomic.Int32
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
	if !m.running.CompareAndSwap(0, 1) {
		return apperror.NewError("mail manager is already running")
	}

	if m.config.Queue.Enabled && m.queueManager != nil {
		err := m.queueManager.Start(m.ctx)
		if err != nil {
			m.running.Store(0)
			return apperror.NewError("starting queue manager failed").AddError(err)
		}
		m.queueManager.RegisterHandler(jobTypeMail, m.handleMailJob)
	}

	if m.config.Server.Enabled && m.server != nil {
		m.wg.Go(func() {
			err := m.server.Start(m.ctx)
			if err != nil {
				logger.Error().Err(err).Msg("starting SMTP server failed")
			}
		})
		logger.Info().Fields(logging.F("host", m.config.Server.Host), logging.F("port", m.config.Server.Port)).Msg("SMTP server started")
	}

	return nil
}

// Stop stops the mail manager
func (m *Manager) Stop(ctx context.Context) error {
	if !m.running.CompareAndSwap(1, 0) {
		return apperror.NewError("mail manager is not running")
	}

	m.cancel()
	if m.server != nil && m.server.IsRunning() {
		err := m.server.Stop(ctx)
		if err != nil {
			logger.Error().Err(err).Msg("stopping SMTP server failed")
		}
	}

	if m.config.Queue.Enabled && m.queueManager != nil {
		err := m.queueManager.Stop()
		if err != nil {
			logger.Error().Err(err).Msg("stopping queue manager failed")
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

	if message.ID == "" {
		message.ID = uuid.New().String()
	}

	logger.Debug().
		Field("id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("sending email")

	err := m.sender.Send(ctx, message)
	if err != nil {
		m.incrementFailedCount()
		return apperror.NewErrorf("sending message %s failed", message.ID).AddError(err)
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

	if message.ID == "" {
		message.ID = uuid.New().String()
	}

	logger.Debug().
		Field("id", message.ID).
		Field("subject", message.Subject).
		Field("to", message.To).
		Msg("queuing email for async sending")

	jobData := map[string]any{
		jobKeyID:           message.ID,
		jobKeyFrom:         message.From,
		jobKeyTo:           message.To,
		jobKeyCC:           message.CC,
		jobKeyBCC:          message.BCC,
		jobKeyReplyTo:      message.ReplyTo,
		jobKeySubject:      message.Subject,
		jobKeyTextBody:     message.TextBody,
		jobKeyHTMLBody:     message.HTMLBody,
		jobKeyTemplate:     message.Template,
		jobKeyTemplateData: message.TemplateData,
		jobKeyAttachments:  message.Attachments,
		jobKeyHeaders:      message.Headers,
		jobKeyPriority:     int(message.Priority),
		jobKeyCreatedAt:    message.CreatedAt,
		jobKeyScheduleAt:   message.ScheduleAt,
		jobKeyMetadata:     message.Metadata,
	}

	job := queue.NewJob(jobTypeMail).
		WithID(message.ID).
		WithPayload(jobData).
		WithPriority(queue.Priority(message.Priority)).
		WithMaxAttempts(m.config.Queue.MaxAttempts)

	if message.ScheduleAt != nil {
		job = job.WithScheduleAt(*message.ScheduleAt)
	}

	err := m.queueManager.Enqueue(ctx, job.Build())
	if err != nil {
		return apperror.NewErrorf("enqueuing message %s failed", message.ID).AddError(err)
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
		return apperror.NewError("smtp server is not configured")
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
	return m.running.Load() == 1
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

// NotifyMessageReceived notifies that a message was received by the SMTP server
func (m *Manager) NotifyMessageReceived() {
	m.incrementReceivedCount()
	m.updateLastReceived()
}

// handleMailJob handles queued mail jobs
func (m *Manager) handleMailJob(ctx context.Context, job *queue.Job) error {
	logger.Debug().Field("job_id", job.ID).Msg("processing mail job")

	var jobData map[string]any
	err := json.Unmarshal(job.Payload, &jobData)
	if err != nil {
		return apperror.NewError("unmarshaling job payload failed").AddError(err)
	}

	message, err := m.jobDataToMessage(jobData)
	if err != nil {
		return apperror.NewError("converting job data to message failed").AddError(err)
	}

	err = m.sender.Send(ctx, message)
	if err != nil {
		m.incrementFailedCount()
		return apperror.NewErrorf("sending message %s failed", message.ID).AddError(err)
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
func (m *Manager) jobDataToMessage(jobData map[string]any) (*Message, error) {
	message := &Message{}

	id, ok := jobData[jobKeyID].(string)
	if ok {
		message.ID = id
	}
	from, ok := jobData[jobKeyFrom].(string)
	if ok {
		message.From = from
	}
	to, ok := jobData[jobKeyTo]
	if ok {
		message.To = convertToStringSlice(to)
	}
	cc, ok := jobData[jobKeyCC]
	if ok {
		message.CC = convertToStringSlice(cc)
	}
	bcc, ok := jobData[jobKeyBCC]
	if ok {
		message.BCC = convertToStringSlice(bcc)
	}
	replyTo, ok := jobData[jobKeyReplyTo].(string)
	if ok {
		message.ReplyTo = replyTo
	}
	subject, ok := jobData[jobKeySubject].(string)
	if ok {
		message.Subject = subject
	}
	textBody, ok := jobData[jobKeyTextBody].(string)
	if ok {
		message.TextBody = textBody
	}
	htmlBody, ok := jobData[jobKeyHTMLBody].(string)
	if ok {
		message.HTMLBody = htmlBody
	}
	template, ok := jobData[jobKeyTemplate].(string)
	if ok {
		message.Template = template
	}
	templateData, ok := jobData[jobKeyTemplateData]
	if ok {
		message.TemplateData = templateData
	}
	priority, ok := jobData[jobKeyPriority].(float64)
	if ok {
		message.Priority = Priority(int(priority))
	}
	if headers, ok := jobData[jobKeyHeaders].(map[string]any); ok {
		message.Headers = stringValues(headers)
	}
	if metadata, ok := jobData[jobKeyMetadata].(map[string]any); ok {
		message.Metadata = stringValues(metadata)
	}
	if createdAt, ok := jobData[jobKeyCreatedAt].(string); ok {
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, apperror.NewError("invalid created_at format").AddError(err)
		}
		message.CreatedAt = t
	}
	if scheduleAt, ok := jobData[jobKeyScheduleAt].(string); ok && scheduleAt != "" {
		t, err := time.Parse(time.RFC3339, scheduleAt)
		if err != nil {
			return nil, apperror.NewError("invalid schedule_at format").AddError(err)
		}
		message.ScheduleAt = &t
	}
	if attachments, ok := jobData[jobKeyAttachments].([]any); ok {
		var err error
		message.Attachments, err = jobDataToAttachments(attachments)
		if err != nil {
			return nil, err
		}
	}

	return message, nil
}

// stringValues copies the string entries of a decoded JSON object and drops all other entries.
func stringValues(values map[string]any) map[string]string {
	result := make(map[string]string)
	for k, v := range values {
		s, ok := v.(string)
		if ok {
			result[k] = s
		}
	}
	return result
}

// jobDataToAttachments converts the decoded attachments of job data and skips entries that are not objects.
func jobDataToAttachments(attachments []any) ([]Attachment, error) {
	result := make([]Attachment, 0, len(attachments))
	for _, att := range attachments {
		attMap, ok := att.(map[string]any)
		if !ok {
			continue
		}
		attachment, err := jobDataToAttachment(attMap)
		if err != nil {
			return nil, err
		}
		result = append(result, attachment)
	}
	return result, nil
}

// jobDataToAttachment converts a single decoded attachment of job data.
func jobDataToAttachment(attMap map[string]any) (Attachment, error) {
	attachment := Attachment{}
	filename, ok := attMap[attachmentKeyFilename].(string)
	if ok {
		attachment.Filename = filename
	}
	contentType, ok := attMap[attachmentKeyContentType].(string)
	if ok {
		attachment.ContentType = contentType
	}
	content, ok := attMap[attachmentKeyContent].(string)
	if ok {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return Attachment{}, apperror.NewError("decoding attachment content failed").AddError(err)
		}
		attachment.Content = decoded
	}
	size, ok := attMap[attachmentKeySize].(float64)
	if ok {
		attachment.Size = int64(size)
	}
	inline, ok := attMap[attachmentKeyInline].(bool)
	if ok {
		attachment.Inline = inline
	}
	contentID, ok := attMap[attachmentKeyContentID].(string)
	if ok {
		attachment.ContentID = contentID
	}
	return attachment, nil
}

// convertToStringSlice safely converts various types to []string
// This handles different JSON unmarshaling scenarios for string slice fields
func convertToStringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		// JSON unmarshaled as interface slice
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	case string:
		return []string{v}
	case nil:
		return nil
	default:
		if str := convertToString(value); str != "" {
			return []string{str}
		}
		return nil
	}
}

// convertToString safely converts various types to string
func convertToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
