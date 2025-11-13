package mail

import (
	"context"
	"html/template"
	"io"
	"mime/multipart"
	"strings"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Message represents an email message
type Message struct {
	// ID is a unique identifier for the message
	ID string `json:"id"`
	// From is the sender's email address
	From string `json:"from"`
	// To is a list of recipient email addresses
	To []string `json:"to"`
	// CC is a list of carbon copy recipient email addresses
	CC []string `json:"cc,omitempty"`
	// BCC is a list of blind carbon copy recipient email addresses
	BCC []string `json:"bcc,omitempty"`
	// ReplyTo is the reply-to email address
	ReplyTo string `json:"reply_to,omitempty"`
	// Subject is the email subject
	Subject string `json:"subject"`
	// TextBody is the plain text body of the email
	TextBody string `json:"text_body,omitempty"`
	// HTMLBody is the HTML body of the email
	HTMLBody string `json:"html_body,omitempty"`
	// Template is the name of the template to use
	Template string `json:"template,omitempty"`
	// TemplateContent is the direct template content to use (alternative to Template)
	TemplateContent string `json:"template_content,omitempty"`
	// TemplateData is the data to pass to the template
	TemplateData interface{} `json:"template_data,omitempty"`
	// TemplateFuncs contains template functions specific to this message (optional)
	TemplateFuncs template.FuncMap `json:"-"`
	// Attachments is a list of file attachments
	Attachments []Attachment `json:"attachments,omitempty"`
	// Headers contains additional email headers
	Headers map[string]string `json:"headers,omitempty"`
	// Priority is the message priority
	Priority Priority `json:"priority"`
	// CreatedAt is when the message was created
	CreatedAt time.Time `json:"created_at"`
	// ScheduleAt is when the message should be sent (optional)
	ScheduleAt *time.Time `json:"schedule_at,omitempty"`
	// Metadata contains additional metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Attachment represents a file attachment
type Attachment struct {
	// Filename is the name of the file
	Filename string `json:"filename"`
	// ContentType is the MIME content type
	ContentType string `json:"content_type"`
	// Content is the file content
	Content []byte `json:"content,omitempty"`
	// Reader is an alternative to Content for streaming large files
	Reader io.Reader `json:"-"`
	// Size is the size of the attachment in bytes
	Size int64 `json:"size"`
	// Inline indicates if the attachment should be displayed inline
	Inline bool `json:"inline"`
	// ContentID is used for inline attachments
	ContentID string `json:"content_id,omitempty"`
}

// Priority represents the message priority
type Priority int

const (
	// PriorityLow represents low priority
	PriorityLow Priority = iota
	// PriorityNormal represents normal priority
	PriorityNormal
	// PriorityHigh represents high priority
	PriorityHigh
	// PriorityCritical represents critical priority
	PriorityCritical
)

// String returns the string representation of the priority
func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Recipient represents an email recipient
type Recipient struct {
	// Email is the recipient's email address
	Email string `json:"email"`

	// Name is the recipient's display name
	Name string `json:"name,omitempty"`

	// Type is the recipient type (to, cc, bcc)
	Type string `json:"type"`
}

// TemplateData represents data passed to email templates
type TemplateData struct {
	// Subject is the email subject
	Subject string `json:"subject"`

	// Data is the custom data for the template
	Data interface{} `json:"data"`

	// Metadata contains additional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// MessageBuilder provides a fluent interface for building messages
type MessageBuilder struct {
	message *Message
	Error   error // Error is used to capture any errors during building
}

// NewMessage creates a new message builder
func NewMessage() *MessageBuilder {
	return &MessageBuilder{
		message: &Message{
			Priority:  PriorityNormal,
			CreatedAt: time.Now(),
			Headers:   make(map[string]string),
			Metadata:  make(map[string]string),
		},
	}
}

// From sets the sender's email address
func (b *MessageBuilder) From(from string) *MessageBuilder {
	if b.Error != nil {
		return b
	}

	if from == "" {
		b.Error = apperror.NewError("from address cannot be empty")
		return b
	}

	b.message.From = from
	return b
}

// To sets the recipient email addresses
func (b *MessageBuilder) To(to ...string) *MessageBuilder {
	if b.Error != nil {
		return b
	}
	if len(to) == 0 {
		b.Error = apperror.NewError("at least one recipient is required")
		return b
	}
	b.message.To = to
	return b
}

// CC sets the carbon copy recipient email addresses
func (b *MessageBuilder) CC(cc ...string) *MessageBuilder {
	b.message.CC = cc
	return b
}

// BCC sets the blind carbon copy recipient email addresses
func (b *MessageBuilder) BCC(bcc ...string) *MessageBuilder {
	b.message.BCC = bcc
	return b
}

// ReplyTo sets the reply-to email address
func (b *MessageBuilder) ReplyTo(replyTo string) *MessageBuilder {
	b.message.ReplyTo = replyTo
	return b
}

// Subject sets the email subject
func (b *MessageBuilder) Subject(subject string) *MessageBuilder {
	b.message.Subject = subject
	return b
}

// TextBody sets the plain text body
func (b *MessageBuilder) TextBody(text string) *MessageBuilder {
	b.message.TextBody = text
	return b
}

// HTMLBody sets the HTML body
func (b *MessageBuilder) HTMLBody(html string) *MessageBuilder {
	b.message.HTMLBody = html
	return b
}

// Template sets the template name and data
func (b *MessageBuilder) Template(name string, data interface{}) *MessageBuilder {
	b.message.Template = name
	b.message.TemplateData = data
	return b
}

// TemplateContent sets the template content directly and data
func (b *MessageBuilder) TemplateContent(content string, data interface{}) *MessageBuilder {
	b.message.TemplateContent = content
	b.message.TemplateData = data
	return b
}

// WithTemplateFunc adds a template function for this message
func (b *MessageBuilder) WithTemplateFunc(key string, fn interface{}) *MessageBuilder {
	if b.Error != nil {
		return b
	}
	if b.message.TemplateFuncs == nil {
		b.message.TemplateFuncs = make(template.FuncMap)
	}
	b.message.TemplateFuncs[key] = fn
	return b
}

// WithTemplateFuncs sets multiple template functions for this message
func (b *MessageBuilder) WithTemplateFuncs(funcs template.FuncMap) *MessageBuilder {
	if b.Error != nil {
		return b
	}
	if b.message.TemplateFuncs == nil {
		b.message.TemplateFuncs = make(template.FuncMap)
	}
	for key, fn := range funcs {
		b.message.TemplateFuncs[key] = fn
	}
	return b
}

// Attach adds a file attachment
func (b *MessageBuilder) Attach(attachment Attachment) *MessageBuilder {
	if b.Error != nil {
		return b
	}
	// Ensure the attachment has a valid filename and content type
	if attachment.Filename == "" || attachment.ContentType == "" {
		b.Error = apperror.NewError("attachment must have a valid filename and content type").AddDetail("filename", attachment.Filename).AddDetail("content_type", attachment.ContentType)
		return b
	}
	b.message.Attachments = append(b.message.Attachments, attachment)
	return b
}

// AttachInline adds an inline file attachment with Content-ID for embedding in HTML
func (b *MessageBuilder) AttachInline(filename, contentType, contentID string, content []byte) *MessageBuilder {
	if b.Error != nil {
		return b
	}
	// Ensure the attachment has a valid filename and content type
	if strings.TrimSpace(filename) == "" || strings.TrimSpace(contentType) == "" {
		b.Error = apperror.NewError("inline attachment must have a valid filename and content type").AddDetail("filename", filename).AddDetail("content_type", contentType)
		return b
	}
	if len(content) == 0 {
		b.Error = apperror.NewError("inline attachment content cannot be empty").AddDetail("filename", filename)
		return b
	}
	if strings.TrimSpace(contentID) == "" {
		b.Error = apperror.NewError("inline attachment must have a Content-ID").AddDetail("filename", filename).AddDetail("content_type", contentType)
		return b
	}
	attachment := Attachment{
		Filename:    filename,
		ContentType: contentType,
		Content:     content,
		Size:        int64(len(content)),
		Inline:      true,
		ContentID:   contentID,
	}
	b.message.Attachments = append(b.message.Attachments, attachment)
	return b
}

// AttachInlineFromReader adds an inline file attachment from a reader with Content-ID for embedding in HTML
func (b *MessageBuilder) AttachInlineFromReader(filename, contentType, contentID string, reader io.Reader, size int64) *MessageBuilder {
	if b.Error != nil {
		return b
	}
	if strings.TrimSpace(filename) == "" || strings.TrimSpace(contentType) == "" {
		b.Error = apperror.NewError("inline attachment must have a valid filename and content type").AddDetail("filename", filename).AddDetail("content_type", contentType)
		return b
	}
	if size <= 0 {
		b.Error = apperror.NewError("inline attachment size must be greater than zero").AddDetail("filename", filename)
		return b
	}
	if strings.TrimSpace(contentID) == "" {
		b.Error = apperror.NewError("inline attachment must have a Content-ID").AddDetail("filename", filename).AddDetail("content_type", contentType)
		return b
	}
	if reader == nil {
		b.Error = apperror.NewError("inline attachment reader cannot be nil").AddDetail("filename", filename).AddDetail("content_type", contentType)
		return b
	}
	attachment := Attachment{
		Filename:    filename,
		ContentType: contentType,
		Reader:      reader,
		Size:        size,
		Inline:      true,
		ContentID:   contentID,
	}
	b.message.Attachments = append(b.message.Attachments, attachment)
	return b
}

// AttachFile adds a file attachment from a multipart file header
// Returns the builder for chaining. If an error occurs, it logs the error
// and continues without adding the attachment.
func (b *MessageBuilder) AttachFile(fileHeader *multipart.FileHeader) *MessageBuilder {
	if b.Error != nil {
		return b
	}

	file, err := fileHeader.Open()
	if err != nil {
		b.Error = apperror.NewError("failed to open attachment file").AddError(err)
		return b
	}

	// Read the file content into memory and close it to prevent resource leaks
	content, err := io.ReadAll(file)
	apperror.Catch(file.Close, "failed to close attachment file")
	if err != nil {
		b.Error = apperror.NewError("failed to read attachment file").AddError(err)
		return b
	}

	attachment := Attachment{
		Filename:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Content:     content, // Use content instead of reader to avoid resource leaks
		Size:        fileHeader.Size,
	}

	return b.Attach(attachment)
}

// Header sets a custom header
func (b *MessageBuilder) Header(key, value string) *MessageBuilder {
	b.message.Headers[key] = value
	return b
}

// Priority sets the message priority
func (b *MessageBuilder) Priority(priority Priority) *MessageBuilder {
	b.message.Priority = priority
	return b
}

// ScheduleAt sets when the message should be sent
func (b *MessageBuilder) ScheduleAt(scheduleAt time.Time) *MessageBuilder {
	b.message.ScheduleAt = &scheduleAt
	return b
}

// Metadata sets metadata for the message
func (b *MessageBuilder) Metadata(key, value string) *MessageBuilder {
	b.message.Metadata[key] = value
	return b
}

// Build returns the built message
func (b *MessageBuilder) Build() (*Message, error) {
	return b.message, b.Error
}

// NotificationHandler is a function that handles incoming SMTP messages
type NotificationHandler func(ctx context.Context, from string, to []string, data io.Reader) error

// Sender is the interface for sending emails
type Sender interface {
	// Send sends an email message
	Send(ctx context.Context, message *Message) error
	// SendAsync sends an email message asynchronously using the queue
	SendAsync(ctx context.Context, message *Message) error
}

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

// AuthMethod represents SMTP authentication methods
type AuthMethod string

const (
	// AuthMethodPlain represents PLAIN authentication
	AuthMethodPlain AuthMethod = "PLAIN"
	// AuthMethodCRAMMD5 represents CRAM-MD5 authentication
	AuthMethodCRAMMD5 AuthMethod = "CRAMMD5"
	// AuthMethodLogin represents LOGIN authentication
	AuthMethodLogin AuthMethod = "LOGIN"
)

// EncryptionMethod represents SMTP encryption methods
type EncryptionMethod string

const (
	// EncryptionNone represents no encryption
	EncryptionNone EncryptionMethod = "NONE"
	// EncryptionSTARTTLS represents STARTTLS encryption
	EncryptionSTARTTLS EncryptionMethod = "STARTTLS"
	// EncryptionTLS represents TLS encryption
	EncryptionTLS EncryptionMethod = "TLS"
)

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
