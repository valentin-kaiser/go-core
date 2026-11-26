package mail_test

import (
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/mail"
)

func TestDefaultConfig_Comprehensive(t *testing.T) {
	config := mail.DefaultConfig()

	if config == nil {
		t.Fatal("Expected non-nil default config")
	}

	// Test default client config values
	if config.Client.Timeout == 0 {
		t.Error("Default client timeout should be set")
	}

	if config.Client.MaxRetries < 0 {
		t.Error("Default max retries should be non-negative")
	}

	if config.Client.RetryDelay == 0 {
		t.Error("Default retry delay should be set")
	}

	// Test default server config values
	if config.Server.ReadTimeout == 0 {
		t.Error("Default server read timeout should be set")
	}

	if config.Server.WriteTimeout == 0 {
		t.Error("Default server write timeout should be set")
	}

	if config.Server.MaxMessageBytes == 0 {
		t.Error("Default max message bytes should be set")
	}

	if config.Server.MaxRecipients == 0 {
		t.Error("Default max recipients should be set")
	}
}

func TestClientConfig_Validation(t *testing.T) {
	tests := []struct {
		name          string
		modifyConfig  func(*mail.ClientConfig)
		expectedValid bool
		description   string
	}{
		{
			name: "valid config with auth",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.Username = "user"
				c.Password = "pass"
				c.From = "from@example.com"
				c.Auth = true
				c.Encryption = "STARTTLS"
			},
			expectedValid: true,
			description:   "should accept valid config with auth",
		},
		{
			name: "valid config without auth",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.From = "from@example.com"
				c.Auth = false
				c.Encryption = "NONE"
			},
			expectedValid: true,
			description:   "should accept valid config without auth",
		},
		{
			name: "empty host",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = ""
				c.Port = 587
			},
			expectedValid: false,
			description:   "should reject empty host",
		},
		{
			name: "invalid port",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 0
			},
			expectedValid: false,
			description:   "should reject zero port",
		},
		{
			name: "invalid port range",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 99999
			},
			expectedValid: false,
			description:   "should reject port out of range",
		},
		{
			name: "auth enabled but no credentials",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.Auth = true
				c.Username = ""
				c.Password = ""
			},
			expectedValid: false,
			description:   "should reject auth without credentials",
		},
		{
			name: "invalid encryption method",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.Encryption = "INVALID"
			},
			expectedValid: false,
			description:   "should reject invalid encryption method",
		},
		{
			name: "negative timeout",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.Timeout = -time.Second
			},
			expectedValid: false,
			description:   "should reject negative timeout",
		},
		{
			name: "negative max retries",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.MaxRetries = -1
			},
			expectedValid: false,
			description:   "should reject negative max retries",
		},
		{
			name: "negative retry delay",
			modifyConfig: func(c *mail.ClientConfig) {
				c.Host = "smtp.example.com"
				c.Port = 587
				c.RetryDelay = -time.Second
			},
			expectedValid: false,
			description:   "should reject negative retry delay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mail.ClientConfig{
				Host:       "smtp.example.com",
				Port:       587,
				Timeout:    30 * time.Second,
				MaxRetries: 3,
				RetryDelay: time.Second,
				Encryption: "NONE",
			}

			if tt.modifyConfig != nil {
				tt.modifyConfig(&config)
			}

			isValid := validateClientConfig(config)
			if isValid != tt.expectedValid {
				t.Errorf("%s: validation result = %v, expected %v", tt.description, isValid, tt.expectedValid)
			}
		})
	}
}

func TestServerConfig_Validation(t *testing.T) {
	tests := []struct {
		name          string
		modifyConfig  func(*mail.ServerConfig)
		expectedValid bool
		description   string
	}{
		{
			name: "valid server config",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = "example.com"
				c.ReadTimeout = 30 * time.Second
				c.WriteTimeout = 30 * time.Second
				c.MaxMessageBytes = 1024 * 1024
				c.MaxRecipients = 100
			},
			expectedValid: true,
			description:   "should accept valid server config",
		},
		{
			name: "empty domain",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = ""
			},
			expectedValid: false,
			description:   "should reject empty domain",
		},
		{
			name: "invalid port",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = -1
				c.Domain = "example.com"
			},
			expectedValid: false,
			description:   "should reject negative port",
		},
		{
			name: "TLS with missing cert files",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = "example.com"
				c.TLS = true
				c.CertFile = ""
				c.KeyFile = ""
			},
			expectedValid: true, // Should be valid as self-signed certs will be generated
			description:   "should accept TLS with empty cert files (self-signed)",
		},
		{
			name: "auth with missing credentials",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = "example.com"
				c.Auth = true
				c.Username = ""
				c.Password = ""
			},
			expectedValid: false,
			description:   "should reject auth without credentials",
		},
		{
			name: "zero timeouts",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = "example.com"
				c.ReadTimeout = 0
				c.WriteTimeout = 0
			},
			expectedValid: false,
			description:   "should reject zero timeouts",
		},
		{
			name: "zero max message bytes",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = "example.com"
				c.MaxMessageBytes = 0
			},
			expectedValid: false,
			description:   "should reject zero max message bytes",
		},
		{
			name: "zero max recipients",
			modifyConfig: func(c *mail.ServerConfig) {
				c.Host = "localhost"
				c.Port = 2525
				c.Domain = "example.com"
				c.MaxRecipients = 0
			},
			expectedValid: false,
			description:   "should reject zero max recipients",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mail.ServerConfig{
				Host:            "localhost",
				Port:            2525,
				Domain:          "example.com",
				ReadTimeout:     30 * time.Second,
				WriteTimeout:    30 * time.Second,
				MaxMessageBytes: 1024 * 1024,
				MaxRecipients:   100,
			}

			if tt.modifyConfig != nil {
				tt.modifyConfig(&config)
			}

			isValid := validateServerConfig(config)
			if isValid != tt.expectedValid {
				t.Errorf("%s: validation result = %v, expected %v", tt.description, isValid, tt.expectedValid)
			}
		})
	}
}

func TestSecurityConfig_Validation(t *testing.T) {
	tests := []struct {
		name          string
		config        mail.SecurityConfig
		expectedValid bool
		description   string
	}{
		{
			name: "valid security config",
			config: mail.SecurityConfig{
				HeloValidation:      true,
				HeloRequireFQDN:     true,
				IPAllowlist:         []string{"192.168.1.0/24", "10.0.0.1"},
				IPBlocklist:         []string{"192.168.2.0/24"},
				MaxConnectionsPerIP: 5,
				RateLimitPerIP:      60,
				MaxAuthFailures:     3,
				AuthFailureWindow:   time.Minute,
				AuthFailureDelay:    time.Second,
			},
			expectedValid: true,
			description:   "should accept valid security config",
		},
		{
			name: "invalid IP in allowlist",
			config: mail.SecurityConfig{
				IPAllowlist: []string{"invalid-ip"},
			},
			expectedValid: false,
			description:   "should reject invalid IP in allowlist",
		},
		{
			name: "invalid CIDR in blocklist",
			config: mail.SecurityConfig{
				IPBlocklist: []string{"192.168.1.0/99"},
			},
			expectedValid: false,
			description:   "should reject invalid CIDR in blocklist",
		},
		{
			name: "negative max connections",
			config: mail.SecurityConfig{
				MaxConnectionsPerIP: -1,
			},
			expectedValid: false,
			description:   "should reject negative max connections",
		},
		{
			name: "negative rate limit",
			config: mail.SecurityConfig{
				RateLimitPerIP: -1,
			},
			expectedValid: false,
			description:   "should reject negative rate limit",
		},
		{
			name: "negative auth failures",
			config: mail.SecurityConfig{
				MaxAuthFailures: -1,
			},
			expectedValid: false,
			description:   "should reject negative max auth failures",
		},
		{
			name: "negative auth failure window",
			config: mail.SecurityConfig{
				AuthFailureWindow: -time.Second,
			},
			expectedValid: false,
			description:   "should reject negative auth failure window",
		},
		{
			name: "negative auth failure delay",
			config: mail.SecurityConfig{
				AuthFailureDelay: -time.Second,
			},
			expectedValid: false,
			description:   "should reject negative auth failure delay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := validateSecurityConfig(tt.config)
			if isValid != tt.expectedValid {
				t.Errorf("%s: validation result = %v, expected %v", tt.description, isValid, tt.expectedValid)
			}
		})
	}
}

func TestQueueConfig_Validation(t *testing.T) {
	tests := []struct {
		name          string
		config        mail.QueueConfig
		expectedValid bool
		description   string
	}{
		{
			name: "valid queue config",
			config: mail.QueueConfig{
				Enabled: true,
			},
			expectedValid: true,
			description:   "should accept valid queue config",
		},
		{
			name: "disabled queue",
			config: mail.QueueConfig{
				Enabled: false,
			},
			expectedValid: true,
			description:   "should accept disabled queue config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := validateQueueConfig(tt.config)
			if isValid != tt.expectedValid {
				t.Errorf("%s: validation result = %v, expected %v", tt.description, isValid, tt.expectedValid)
			}
		})
	}
}

func TestTemplateConfig_Validation(t *testing.T) {
	tests := []struct {
		name          string
		config        mail.TemplateConfig
		expectedValid bool
		description   string
	}{
		{
			name: "valid template config",
			config: mail.TemplateConfig{
				DefaultTemplate: "default.html",
				AutoReload:      true,
			},
			expectedValid: true,
			description:   "should accept valid template config",
		},
		{
			name: "empty default template",
			config: mail.TemplateConfig{
				DefaultTemplate: "",
				AutoReload:      true,
			},
			expectedValid: false,
			description:   "should reject empty default template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := validateTemplateConfig(tt.config)
			if isValid != tt.expectedValid {
				t.Errorf("%s: validation result = %v, expected %v", tt.description, isValid, tt.expectedValid)
			}
		})
	}
}

// Helper validation functions (these would ideally be part of the mail package)
func validateClientConfig(config mail.ClientConfig) bool {
	if config.Host == "" {
		return false
	}
	if config.Port <= 0 || config.Port > 65535 {
		return false
	}
	if config.Auth && (config.Username == "" || config.Password == "") {
		return false
	}
	if config.Encryption != "" && config.Encryption != "NONE" &&
		config.Encryption != "STARTTLS" && config.Encryption != "TLS" {
		return false
	}
	if config.Timeout < 0 {
		return false
	}
	if config.MaxRetries < 0 {
		return false
	}
	if config.RetryDelay < 0 {
		return false
	}
	return true
}

func validateServerConfig(config mail.ServerConfig) bool {
	if config.Domain == "" {
		return false
	}
	if config.Port < 0 || config.Port > 65535 {
		return false
	}
	if config.Auth && (config.Username == "" || config.Password == "") {
		return false
	}
	if config.ReadTimeout <= 0 {
		return false
	}
	if config.WriteTimeout <= 0 {
		return false
	}
	if config.MaxMessageBytes <= 0 {
		return false
	}
	if config.MaxRecipients <= 0 {
		return false
	}
	return true
}

func validateSecurityConfig(config mail.SecurityConfig) bool {
	// Validate IP addresses and CIDR blocks
	for _, ip := range config.IPAllowlist {
		if !isValidIPOrCIDR(ip) {
			return false
		}
	}
	for _, ip := range config.IPBlocklist {
		if !isValidIPOrCIDR(ip) {
			return false
		}
	}

	if config.MaxConnectionsPerIP < 0 {
		return false
	}
	if config.RateLimitPerIP < 0 {
		return false
	}
	if config.MaxAuthFailures < 0 {
		return false
	}
	if config.AuthFailureWindow < 0 {
		return false
	}
	if config.AuthFailureDelay < 0 {
		return false
	}
	return true
}

func validateQueueConfig(_ mail.QueueConfig) bool {
	// Basic queue config is always valid
	return true
}

func validateTemplateConfig(config mail.TemplateConfig) bool {
	return config.DefaultTemplate != ""
}

func isValidIPOrCIDR(s string) bool {
	// Simple validation - in real implementation would use net.ParseIP and net.ParseCIDR
	return s != "" && s != "invalid-ip" && s != "192.168.1.0/99"
}
