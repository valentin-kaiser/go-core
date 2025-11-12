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

// smtpSender implements the Sender interface using internal email package
type smtpSender struct {
	config          ClientConfig
	templateManager *TemplateManager
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

// Send sends an email message via SMTP
func (s *smtpSender) Send(ctx context.Context, message *Message) error {
	if !s.config.Enabled {
		return apperror.NewError("SMTP sender is disabled")
	}

	// Validate message
	err := s.validateMessage(message)
	if err != nil {
		return apperror.Wrap(err)
	}

	// Process template if specified
	if s.templateManager != nil && s.templateManager.config.Enabled {
		err = s.processTemplate(message)
		if err != nil {
			return apperror.Wrap(err)
		}
	}

	// Create email using internal email package
	emailMsg, err := s.createEmail(message)
	if err != nil {
		return apperror.Wrap(err)
	}

	// Send with retries
	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			logger.Warn().Field("attempt", attempt).Field("message_id", message.ID).Msg("retrying email send")

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.config.RetryDelay):
				// Continue with retry
			}
		}

		err = s.sendEmail(ctx, emailMsg)
		if err == nil {
			return nil
		}

		logger.Error().Err(err).Field("attempt", attempt).Field("message_id", message.ID).Msg("failed to send email via SMTP")

		if attempt == s.config.MaxRetries {
			break
		}
	}

	return apperror.NewError("failed to send email after retries").AddError(err)
}

// SendAsync is not implemented for SMTP sender (handled by manager)
func (s *smtpSender) SendAsync(_ context.Context, _ *Message) error {
	return apperror.NewError("async sending not supported by SMTP sender directly")
}

// validateMessage validates the email message
func (s *smtpSender) validateMessage(message *Message) error {
	if message.From == "" && s.config.From == "" {
		return apperror.NewError("from address is required")
	}

	if len(message.To) == 0 {
		return apperror.NewError("at least one recipient is required")
	}

	return nil
}

// processTemplate processes the email template if specified
func (s *smtpSender) processTemplate(message *Message) error {
	// Skip if neither template name nor template content is provided
	if message.Template == "" && message.TemplateContent == "" {
		return nil
	}

	var err error
	if message.TemplateContent != "" {
		// Use direct template content
		message.HTMLBody, err = s.templateManager.RenderTemplateContent(message.TemplateContent, message.TemplateData, message.TemplateFuncs)
	} else {
		// Use template file
		message.HTMLBody, err = s.templateManager.RenderTemplate(message.Template, message.TemplateData, message.TemplateFuncs)
	}

	if err != nil {
		return apperror.Wrap(err)
	}

	return nil
}

// createEmail creates an email.Email from our Message
func (s *smtpSender) createEmail(message *Message) (*email.Email, error) {
	emailMsg := email.New()

	// Set From address
	from := message.From
	if from == "" {
		from = s.config.From
	}
	emailMsg.From = from

	// Set recipients
	emailMsg.To = message.To
	emailMsg.Cc = message.CC
	emailMsg.Bcc = message.BCC

	// Set Reply-To
	if message.ReplyTo != "" {
		emailMsg.ReplyTo = []string{message.ReplyTo}
	}

	// Set subject
	emailMsg.Subject = message.Subject

	// Set body content
	if message.TextBody != "" {
		emailMsg.Text = []byte(message.TextBody)
	}
	if message.HTMLBody != "" {
		emailMsg.HTML = []byte(message.HTMLBody)
	}

	// Set custom headers
	if emailMsg.Headers == nil {
		emailMsg.Headers = make(map[string][]string)
	}

	// Add Message-ID if available
	if message.ID != "" {
		emailMsg.Headers["Message-ID"] = []string{fmt.Sprintf("<%s@%s>", message.ID, s.config.Host)}
	}

	// Add priority header
	if message.Priority != PriorityNormal {
		priority := s.getPriorityHeader(message.Priority)
		emailMsg.Headers["X-Priority"] = []string{priority}
	}

	// Add custom headers
	for key, value := range message.Headers {
		emailMsg.Headers[key] = []string{value}
	}

	// Add attachments
	for _, attachment := range message.Attachments {
		err := s.addAttachment(emailMsg, attachment)
		if err != nil {
			return nil, apperror.Wrap(err)
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
			return err
		}
		// Set Content-ID header for inline attachments
		a.Header.Set("Content-ID", fmt.Sprintf("<%s>", attachment.ContentID))
		a.Header.Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", attachment.Filename))
		return nil
	}

	_, err := emailMsg.Attach(reader, attachment.Filename, attachment.ContentType)
	return err
}

// sendEmail sends the email using the appropriate method
func (s *smtpSender) sendEmail(_ context.Context, emailMsg *email.Email) error {
	// Prepare authentication
	var auth smtp.Auth
	if s.config.Auth {
		auth = s.createAuth()
	}

	// Get server address
	addr := net.JoinHostPort(s.config.Host, strconv.Itoa(s.config.Port))

	// Send based on encryption method
	switch strings.ToUpper(s.config.Encryption) {
	case "TLS":
		return s.sendWithTLS(emailMsg, addr, auth)
	case "STARTTLS":
		return s.sendWithStartTLS(emailMsg, addr, auth)
	default:
		return s.sendPlain(emailMsg, addr, auth)
	}
}

// createAuth creates SMTP authentication
func (s *smtpSender) createAuth() smtp.Auth {
	switch strings.ToUpper(s.config.AuthMethod) {
	case "CRAMMD5":
		// Use the proper CRAM-MD5 implementation from net/smtp
		return smtp.CRAMMD5Auth(s.config.Username, s.config.Password)
	case "LOGIN":
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
		return "1 (Highest)"
	case PriorityHigh:
		return "2 (High)"
	case PriorityNormal:
		return "3 (Normal)"
	case PriorityLow:
		return "4 (Low)"
	default:
		return "3 (Normal)"
	}
}

// LoginAuth implements LOGIN authentication for SMTP
type LoginAuth struct {
	username, password string
}

// NewLoginAuth creates a new LOGIN authenticator
func NewLoginAuth(username, password string) smtp.Auth {
	return &LoginAuth{username, password}
}

// Start begins the LOGIN authentication
func (a *LoginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

// Next handles the LOGIN authentication process
func (a *LoginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:", "VXNlcm5hbWU6": // "Username:" in base64
			return []byte(a.username), nil
		case "Password:", "UGFzc3dvcmQ6": // "Password:" in base64
			return []byte(a.password), nil
		default:
			return nil, apperror.NewError("unexpected server challenge: " + string(fromServer))
		}
	}
	return nil, nil
}
