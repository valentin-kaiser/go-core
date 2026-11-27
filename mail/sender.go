package mail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/mail/internal/email"
)

// Sender header and challenge constants.
const (
	headerMessageID          = "Message-ID"
	headerXPriority          = "X-Priority"
	headerContentID          = "Content-ID"
	headerContentDisposition = "Content-Disposition"

	priorityHighest = "1 (Highest)"
	priorityHigh    = "2 (High)"
	priorityNormal  = "3 (Normal)"
	priorityLow     = "4 (Low)"

	loginFirstPrompt        = "Username:"
	loginFirstPromptBase64  = "VXNlcm5hbWU6"
	loginSecondPrompt       = "Password:"
	loginSecondPromptBase64 = "UGFzc3dvcmQ6"
)

// LoginAuth implements LOGIN authentication for SMTP
type LoginAuth struct {
	username, password string
}

// Start begins the LOGIN authentication
func (a *LoginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return string(AuthMethodLogin), []byte(a.username), nil
}

// Next handles the LOGIN authentication process
func (a *LoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case loginFirstPrompt, loginFirstPromptBase64:
			return []byte(a.username), nil
		case loginSecondPrompt, loginSecondPromptBase64:
			return []byte(a.password), nil
		default:
			return nil, apperror.NewError("unexpected server challenge: " + string(fromServer))
		}
	}
	return nil, nil
}

// Sender is the interface for sending emails
type Sender interface {
	// Send sends an email message
	Send(ctx context.Context, message *Message) error
	// SendAsync sends an email message asynchronously using the queue
	SendAsync(ctx context.Context, message *Message) error
}

// NewSMTPSender creates a new SMTP sender with configurable security
func NewSMTPSender(config ClientConfig, templateManager *TemplateManager) Sender {
	if config.Enabled {
		logger.Info().Field("host", config.Host).Field("port", config.Port).Field("encryption", config.Encryption).Msg("SMTP sender enabled")
	}

	return &smtpSender{
		config:          config,
		templateManager: templateManager,
	}
}

// smtpSender implements the Sender interface using internal email package
type smtpSender struct {
	config          ClientConfig
	templateManager *TemplateManager
}

// Send sends an email message via SMTP
func (s *smtpSender) Send(ctx context.Context, message *Message) error {
	if !s.config.Enabled {
		return apperror.NewError("smtp sender is disabled")
	}

	err := s.validateMessage(message)
	if err != nil {
		return apperror.NewErrorf("validating message %s failed", message.ID).AddError(err)
	}

	if s.templateManager != nil && s.templateManager.config.Enabled {
		err = s.processTemplate(message)
		if err != nil {
			return apperror.NewErrorf("processing template of message %s failed", message.ID).AddError(err)
		}
	}

	emailMsg, err := s.createEmail(message)
	if err != nil {
		return apperror.NewErrorf("creating email for message %s failed", message.ID).AddError(err)
	}

	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			logger.Warn().Field("attempt", attempt).Field("message_id", message.ID).Msg("retrying email send")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.config.RetryDelay):
			}
		}
		err = s.sendEmail(ctx, emailMsg)
		if err == nil {
			return nil
		}
		if attempt == s.config.MaxRetries {
			break
		}
		logger.Warn().Err(err).Field("attempt", attempt).Field("message_id", message.ID).Msg("sending email via SMTP failed")
	}

	return apperror.NewError("sending email after retries failed").AddError(err)
}

// SendAsync is not implemented for SMTP sender (handled by manager)
func (s *smtpSender) SendAsync(_ context.Context, _ *Message) error {
	return apperror.NewError("async sending not supported by SMTP sender directly")
}

// validateMessage validates the email message
func (s *smtpSender) validateMessage(message *Message) error {
	if len(message.To) == 0 {
		return apperror.NewError("at least one recipient is required")
	}

	return nil
}

// processTemplate processes the email template if specified
func (s *smtpSender) processTemplate(message *Message) error {
	if message.Template == "" && message.TemplateContent == "" {
		return nil
	}

	var err error
	if message.TemplateContent != "" {
		message.HTMLBody, err = s.templateManager.RenderTemplateContent(message.TemplateContent, message.TemplateData, message.TemplateFuncs)
	} else {
		message.HTMLBody, err = s.templateManager.RenderTemplate(message.Template, message.TemplateData, message.TemplateFuncs)
	}

	if err != nil {
		return apperror.NewError("rendering message template failed").AddError(err)
	}

	return nil
}

// createEmail creates an email.Email from our Message
func (s *smtpSender) createEmail(message *Message) (*email.Email, error) {
	emailMsg := email.New()

	from := message.From
	if from == "" {
		from = s.config.From
	}
	emailMsg.From = from

	emailMsg.To = message.To
	emailMsg.Cc = message.CC
	emailMsg.Bcc = message.BCC

	if message.ReplyTo != "" {
		emailMsg.ReplyTo = []string{message.ReplyTo}
	}

	emailMsg.Subject = message.Subject

	if message.TextBody != "" {
		emailMsg.Text = []byte(message.TextBody)
	}
	if message.HTMLBody != "" {
		emailMsg.HTML = []byte(message.HTMLBody)
	}

	if emailMsg.Headers == nil {
		emailMsg.Headers = make(map[string][]string)
	}

	if message.ID != "" {
		emailMsg.Headers[headerMessageID] = []string{fmt.Sprintf("<%s@%s>", message.ID, s.config.Host)}
	}

	if message.Priority != PriorityNormal {
		priority := s.getPriorityHeader(message.Priority)
		emailMsg.Headers[headerXPriority] = []string{priority}
	}

	for key, value := range message.Headers {
		emailMsg.Headers[key] = []string{value}
	}

	for _, attachment := range message.Attachments {
		err := s.addAttachment(emailMsg, attachment)
		if err != nil {
			return nil, apperror.NewErrorf("adding attachment %s failed", attachment.Filename).AddError(err)
		}
	}

	return emailMsg, nil
}

// addAttachment adds an attachment to the email
func (s *smtpSender) addAttachment(emailMsg *email.Email, attachment Attachment) error {
	var reader io.Reader

	switch {
	case attachment.Content != nil:
		reader = bytes.NewReader(attachment.Content)
	case attachment.Reader != nil:
		reader = attachment.Reader
	default:
		return apperror.NewError("attachment has no content or reader")
	}

	if attachment.Inline && attachment.ContentID != "" {
		// Inline attachment with Content-ID for HTML embedding
		a, err := emailMsg.Attach(reader, attachment.Filename, attachment.ContentType)
		if err != nil {
			return apperror.NewErrorf("attaching inline file %s failed", attachment.Filename).AddError(err)
		}
		a.Header.Set(headerContentID, fmt.Sprintf("<%s>", attachment.ContentID))
		a.Header.Set(headerContentDisposition, fmt.Sprintf("inline; filename=\"%s\"", attachment.Filename))
		return nil
	}

	_, err := emailMsg.Attach(reader, attachment.Filename, attachment.ContentType)
	if err != nil {
		return apperror.NewErrorf("attaching file %s failed", attachment.Filename).AddError(err)
	}

	return nil
}

// sendEmail sends the email using the appropriate method
func (s *smtpSender) sendEmail(_ context.Context, emailMsg *email.Email) error {
	var auth smtp.Auth
	if s.config.Auth {
		auth = s.createAuth()
	}

	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))

	switch strings.ToUpper(s.config.Encryption) {
	case string(EncryptionTLS):
		return s.sendWithTLS(emailMsg, addr, auth)
	case string(EncryptionSTARTTLS):
		return s.sendWithStartTLS(emailMsg, addr, auth)
	default:
		return s.sendPlain(emailMsg, addr, auth)
	}
}

// createAuth creates SMTP authentication
func (s *smtpSender) createAuth() smtp.Auth {
	switch strings.ToUpper(s.config.AuthMethod) {
	case string(AuthMethodCRAMMD5):
		return smtp.CRAMMD5Auth(s.config.Username, s.config.Password)
	case string(AuthMethodLogin):
		return NewLoginAuth(s.config.Username, s.config.Password)
	default:
		return smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
	}
}

// sendWithTLS sends email with TLS encryption
func (s *smtpSender) sendWithTLS(emailMsg *email.Email, addr string, auth smtp.Auth) error {
	tlsConfig := s.config.TLSConfig()
	return emailMsg.SendWithTLS(addr, auth, tlsConfig, s.config.FQDN)
}

// sendWithStartTLS sends email with STARTTLS encryption
func (s *smtpSender) sendWithStartTLS(emailMsg *email.Email, addr string, auth smtp.Auth) error {
	tlsConfig := s.config.TLSConfig()
	return emailMsg.SendWithStartTLS(addr, auth, tlsConfig, s.config.FQDN)
}

// sendPlain sends email without encryption
func (s *smtpSender) sendPlain(emailMsg *email.Email, addr string, auth smtp.Auth) error {
	return emailMsg.Send(addr, auth, s.config.FQDN)
}

// getPriorityHeader converts priority to email header value
func (s *smtpSender) getPriorityHeader(priority Priority) string {
	switch priority {
	case PriorityCritical:
		return priorityHighest
	case PriorityHigh:
		return priorityHigh
	case PriorityNormal:
		return priorityNormal
	case PriorityLow:
		return priorityLow
	default:
		return priorityNormal
	}
}

// NewLoginAuth creates a new LOGIN authenticator
func NewLoginAuth(username, password string) smtp.Auth {
	return &LoginAuth{username: username, password: password}
}
