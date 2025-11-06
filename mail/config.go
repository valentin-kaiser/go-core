package mail

import (
	"crypto/tls"
	"html/template"
	"io/fs"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/config"
)

// Config holds the configuration for the mail package
type Config struct {
	// SMTP Client Configuration
	Client ClientConfig `yaml:"client" json:"client"`
	// SMTP Server Configuration
	Server ServerConfig `yaml:"server" json:"server"`
	// Queue Configuration
	Queue QueueConfig `yaml:"queue" json:"queue"`
	// Templates Configuration
	Templates TemplateConfig `yaml:"templates" json:"templates"`
}

// ClientConfig holds the SMTP client configuration for sending emails
type ClientConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Host is the SMTP server hostname
	Host string `yaml:"host" json:"host"`
	// Port is the SMTP server port
	Port int `yaml:"port" json:"port"`
	// Username for SMTP authentication
	Username string `yaml:"username" json:"username"`
	// Password for SMTP authentication
	Password string `yaml:"password" json:"password"`
	// From address for outgoing emails
	From string `yaml:"from" json:"from"`
	// FQDN for HELO command
	FQDN string `yaml:"fqdn" json:"fqdn"`
	// Authentication enabled
	Auth bool `yaml:"auth" json:"auth"`
	// AuthMethod defines the authentication method (PLAIN, CRAMMD5, LOGIN)
	AuthMethod string `yaml:"auth_method" json:"auth_method"`
	// Encryption method (NONE, STARTTLS, TLS)
	Encryption string `yaml:"encryption" json:"encryption"`
	// SkipCertificateVerification skips TLS certificate verification
	SkipCertificateVerification bool `yaml:"skip_cert_verification" json:"skip_cert_verification"`
	// Timeout for SMTP operations
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
	// MaxRetries for failed email sending
	MaxRetries int `yaml:"max_retries" json:"max_retries"`
	// RetryDelay between retries
	RetryDelay time.Duration `yaml:"retry_delay" json:"retry_delay"`
}

// ServerConfig holds the SMTP server configuration
type ServerConfig struct {
	// Enabled indicates if the SMTP server should be started
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Host to bind the server to
	Host string `yaml:"host" json:"host"`
	// Port to bind the server to
	Port int `yaml:"port" json:"port"`
	// Domain name for the server
	Domain string `yaml:"domain" json:"domain"`
	// Authentication required for incoming messages
	Auth bool `yaml:"auth" json:"auth"`
	// Username for server authentication
	Username string `yaml:"username" json:"username"`
	// Password for server authentication
	Password string `yaml:"password" json:"password"`
	// TLS encryption enabled
	TLS bool `yaml:"tls" json:"tls"`
	// Certificate file path for TLS
	CertFile string `yaml:"cert_file" json:"cert_file"`
	// Key file path for TLS
	KeyFile string `yaml:"key_file" json:"key_file"`
	// ReadTimeout for server connections
	ReadTimeout time.Duration `yaml:"read_timeout" json:"read_timeout"`
	// WriteTimeout for server connections
	WriteTimeout time.Duration `yaml:"write_timeout" json:"write_timeout"`
	// MaxMessageBytes is the maximum size of a message
	MaxMessageBytes int64 `yaml:"max_message_bytes" json:"max_message_bytes"`
	// MaxRecipients is the maximum number of recipients per message
	MaxRecipients int `yaml:"max_recipients" json:"max_recipients"`
	// AllowInsecureAuth allows authentication over non-TLS connections
	AllowInsecureAuth bool `yaml:"allow_insecure_auth" json:"allow_insecure_auth"`
	// MaxConcurrentHandlers limits the number of concurrent notification handlers
	MaxConcurrentHandlers int `yaml:"max_concurrent_handlers" json:"max_concurrent_handlers"`
	// Security holds the security configuration for the SMTP server
	Security SecurityConfig `yaml:"security" json:"security"`
}

// SecurityConfig holds security-related configuration for the smtp server
type SecurityConfig struct {
	// HeloValidation enables HELO/EHLO hostname validation
	HeloValidation bool `yaml:"helo_validation" json:"helo_validation"`
	// HeloRequireFQDN requires HELO hostname to be a fully qualified domain name
	HeloRequireFQDN bool `yaml:"helo_require_fqdn" json:"helo_require_fqdn"`
	// HeloDNSCheck enables DNS resolution check for HELO hostname
	HeloDNSCheck bool `yaml:"helo_dns_check" json:"helo_dns_check"`
	// IPAllowlist contains allowed IP addresses/CIDR blocks
	IPAllowlist []string `yaml:"ip_allowlist" json:"ip_allowlist"`
	// IPBlocklist contains blocked IP addresses/CIDR blocks
	IPBlocklist []string `yaml:"ip_blocklist" json:"ip_blocklist"`
	// MaxConnectionsPerIP limits connections per IP address
	MaxConnectionsPerIP int `yaml:"max_connections_per_ip" json:"max_connections_per_ip"`
	// RateLimitPerIP limits commands per IP per minute
	RateLimitPerIP int `yaml:"rate_limit_per_ip" json:"rate_limit_per_ip"`
	// AuthFailureDelay adds delay after authentication failures
	AuthFailureDelay time.Duration `yaml:"auth_failure_delay" json:"auth_failure_delay"`
	// MaxAuthFailures limits auth attempts before blocking IP
	MaxAuthFailures int `yaml:"max_auth_failures" json:"max_auth_failures"`
	// AuthFailureWindow is the time window to track auth failures
	AuthFailureWindow time.Duration `yaml:"auth_failure_window" json:"auth_failure_window"`
	// LogSecurityEvents enables detailed security logging
	LogSecurityEvents bool `yaml:"log_security_events" json:"log_security_events"`
}

// QueueConfig holds the queue configuration for mail processing
type QueueConfig struct {
	// Enabled indicates if queue processing should be used
	Enabled bool `yaml:"enabled" json:"enabled"`
	// WorkerCount is the number of workers processing mail jobs
	WorkerCount int `yaml:"worker_count" json:"worker_count"`
	// QueueName is the name of the queue for mail jobs
	QueueName string `yaml:"queue_name" json:"queue_name"`
	// Priority for mail jobs
	Priority int `yaml:"priority" json:"priority"`
	// MaxAttempts for failed mail jobs
	MaxAttempts int `yaml:"max_attempts" json:"max_attempts"`
	// JobTimeout for mail job processing
	JobTimeout time.Duration `yaml:"job_timeout" json:"job_timeout"`
}

// TemplateConfig holds the template configuration
type TemplateConfig struct {
	// Enabled indicates if template processing is enabled
	Enabled bool `yaml:"enabled" json:"enabled"`
	// DefaultTemplate is the name of the default template
	DefaultTemplate string `yaml:"default_template" json:"default_template"`
	// AutoReload indicates if templates should be reloaded on change
	AutoReload bool `yaml:"auto_reload" json:"auto_reload"`
	// FileSystem for loading templates (internal use - not serializable)
	FileSystem fs.FS `yaml:"-" json:"-"`
	// TemplatesPath is the path to custom email templates (used with WithFileServer)
	TemplatesPath string `yaml:"templates_path" json:"templates_path"`
	// WithDefaultFuncs indicates if default template functions should be included
	WithDefaultFuncs bool `yaml:"-" json:"-"`
	// GlobalFuncs defines additional global template functions
	GlobalFuncs template.FuncMap `yaml:"-" json:"-"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Client: ClientConfig{
			Host:                        "localhost",
			Port:                        587,
			From:                        "noreply@example.com",
			FQDN:                        "localhost",
			Auth:                        false,
			AuthMethod:                  "PLAIN",
			Encryption:                  "STARTTLS",
			SkipCertificateVerification: false,
			Timeout:                     30 * time.Second,
			MaxRetries:                  3,
			RetryDelay:                  5 * time.Second,
		},
		Server: ServerConfig{
			Enabled:               false,
			Host:                  "localhost",
			Port:                  2525,
			Domain:                "localhost",
			Auth:                  false,
			TLS:                   false,
			ReadTimeout:           10 * time.Second,
			WriteTimeout:          10 * time.Second,
			MaxMessageBytes:       10 * 1024 * 1024, // 10MB
			MaxRecipients:         100,
			AllowInsecureAuth:     false,
			MaxConcurrentHandlers: 50, // Limit concurrent notification handlers
			Security: SecurityConfig{
				HeloValidation:      false,
				HeloRequireFQDN:     false,
				HeloDNSCheck:        false,
				IPAllowlist:         []string{},
				IPBlocklist:         []string{},
				MaxConnectionsPerIP: 10,
				RateLimitPerIP:      60, // 60 commands per minute
				AuthFailureDelay:    time.Second,
				MaxAuthFailures:     5,
				AuthFailureWindow:   15 * time.Minute,
				LogSecurityEvents:   true,
			},
		},
		Queue: QueueConfig{
			Enabled:     true,
			WorkerCount: 5,
			QueueName:   "mail",
			Priority:    1,
			MaxAttempts: 3,
			JobTimeout:  60 * time.Second,
		},
		Templates: TemplateConfig{
			DefaultTemplate: "default.html",
			AutoReload:      true,
		},
	}
}

// TLSConfig returns a TLS configuration for the SMTP client
func (c *ClientConfig) TLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         c.Host,
		InsecureSkipVerify: c.SkipCertificateVerification,
		MinVersion:         tls.VersionTLS12,
	}
}

// Validate checks the client configuration for errors
func (c *ClientConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Host == "" {
		return apperror.NewError("SMTP host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return apperror.NewError("SMTP port must be between 1 and 65535")
	}
	if c.Auth {
		if c.Username == "" {
			return apperror.NewError("SMTP username is required")
		}
		if c.Password == "" {
			return apperror.NewError("SMTP password is required")
		}
	}
	if c.From == "" {
		return apperror.NewError("SMTP from address is required")
	}
	if c.FQDN == "" {
		return apperror.NewError("SMTP FQDN is required")
	}
	return nil
}

// TLSConfig returns a TLS configuration for the SMTP server
func (c *ServerConfig) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}

// Validate checks the server configuration for errors
func (c *ServerConfig) Validate() error {
	if c.Host == "" {
		return apperror.NewError("SMTP server host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return apperror.NewError("SMTP server port must be between 1 and 65535")
	}
	if c.Domain == "" {
		return apperror.NewError("SMTP server domain is required")
	}
	if c.TLS && (c.CertFile == "" || c.KeyFile == "") {
		return apperror.NewError("TLS is enabled but certificate or key file is missing")
	}
	return nil
}

// Validate checks the queue configuration for errors
func (c *QueueConfig) Validate() error {
	if c.Enabled {
		if c.QueueName == "" {
			return apperror.NewError("Queue name is required")
		}
		if c.MaxAttempts <= 0 {
			return apperror.NewError("Max attempts must be greater than 0")
		}
		if c.JobTimeout <= 0 {
			return apperror.NewError("Job timeout must be greater than 0")
		}
	}
	return nil
}

// Validate checks the template configuration for errors
func (c *TemplateConfig) Validate() error {
	if c.Enabled {
		if c.DefaultTemplate == "" {
			return apperror.NewError("Default template is required")
		}
	}
	return nil
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	if err := c.Client.Validate(); err != nil {
		return apperror.Wrap(err)
	}
	if err := c.Server.Validate(); err != nil {
		return apperror.Wrap(err)
	}
	if err := c.Queue.Validate(); err != nil {
		return apperror.Wrap(err)
	}
	if err := c.Templates.Validate(); err != nil {
		return apperror.Wrap(err)
	}
	return nil
}

// Changed checks if the mail configuration has changed compared to another configuration.
func (c *Config) Changed(n *Config) bool {
	return config.Changed(c, n)
}

// Changed checks if the client configuration has changed compared to another configuration.
func (c *ClientConfig) Changed(n *ClientConfig) bool {
	return config.Changed(c, n)
}

// Changed checks if the server configuration has changed compared to another configuration.
func (c *ServerConfig) Changed(n *ServerConfig) bool {
	return config.Changed(c, n)
}

// Changed checks if the queue configuration has changed compared to another configuration.
func (c *QueueConfig) Changed(n *QueueConfig) bool {
	return config.Changed(c, n)
}

// Changed checks if the template configuration has changed compared to another configuration.
func (c *TemplateConfig) Changed(n *TemplateConfig) bool {
	return config.Changed(c, n)
}
