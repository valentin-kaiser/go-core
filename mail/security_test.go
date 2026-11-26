package mail_test

import (
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/mail"
)

// TestNewPolicy tests the Policy functionality
func TestSecurityManager(t *testing.T) {
	config := mail.SecurityConfig{
		HeloValidation:      true,
		HeloRequireFQDN:     true,
		HeloDNSCheck:        false, // Disable DNS check for testing
		IPAllowlist:         []string{"192.168.1.0/24", "10.0.0.1"},
		IPBlocklist:         []string{"192.168.2.0/24"},
		MaxConnectionsPerIP: 5,
		RateLimitPerIP:      10,
		MaxAuthFailures:     3,
		AuthFailureWindow:   time.Minute,
		AuthFailureDelay:    time.Second,
	}

	sm := mail.NewPolicy(config)

	if sm == nil {
		t.Fatal("Expected Policy to be created")
	}
}

func TestSecurityManager_ValidateConnection(t *testing.T) {
	tests := []struct {
		name        string
		config      mail.SecurityConfig
		remoteAddr  string
		wantError   bool
		description string
	}{
		{
			name: "allowed IP in allowlist",
			config: mail.SecurityConfig{
				IPAllowlist:         []string{"192.168.1.0/24"},
				MaxConnectionsPerIP: 5,
			},
			remoteAddr:  "192.168.1.100:12345",
			wantError:   false,
			description: "should allow IP in allowlist",
		},
		{
			name: "blocked IP in blocklist",
			config: mail.SecurityConfig{
				IPBlocklist:         []string{"192.168.2.0/24"},
				MaxConnectionsPerIP: 5,
			},
			remoteAddr:  "192.168.2.100:12345",
			wantError:   true,
			description: "should block IP in blocklist",
		},
		{
			name: "IP not in allowlist when allowlist exists",
			config: mail.SecurityConfig{
				IPAllowlist:         []string{"192.168.1.0/24"},
				MaxConnectionsPerIP: 5,
			},
			remoteAddr:  "10.0.0.1:12345",
			wantError:   true,
			description: "should block IP not in allowlist",
		},
		{
			name: "invalid remote address",
			config: mail.SecurityConfig{
				MaxConnectionsPerIP: 5,
			},
			remoteAddr:  "invalid-address",
			wantError:   true,
			description: "should handle invalid remote address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := mail.NewPolicy(tt.config)
			err := sm.ValidateConnection(tt.remoteAddr)

			if (err != nil) != tt.wantError {
				t.Errorf("%s: ValidateConnection() error = %v, wantError %v", tt.description, err, tt.wantError)
			}
		})
	}
}

func TestSecurityManager_ValidateHelo(t *testing.T) {
	tests := []struct {
		name        string
		config      mail.SecurityConfig
		hostname    string
		remoteAddr  string
		wantError   bool
		description string
	}{
		{
			name: "valid FQDN with validation enabled",
			config: mail.SecurityConfig{
				HeloValidation:  true,
				HeloRequireFQDN: true,
				HeloDNSCheck:    false,
			},
			hostname:    "mail.example.com",
			remoteAddr:  "192.168.1.1:12345",
			wantError:   false,
			description: "should accept valid FQDN",
		},
		{
			name: "invalid hostname with validation enabled",
			config: mail.SecurityConfig{
				HeloValidation:  true,
				HeloRequireFQDN: true,
				HeloDNSCheck:    false,
			},
			hostname:    "localhost",
			remoteAddr:  "192.168.1.1:12345",
			wantError:   true,
			description: "should reject non-FQDN when FQDN required",
		},
		{
			name: "validation disabled",
			config: mail.SecurityConfig{
				HeloValidation: false,
			},
			hostname:    "anything",
			remoteAddr:  "192.168.1.1:12345",
			wantError:   false,
			description: "should accept any hostname when validation disabled",
		},
		{
			name: "empty hostname with validation enabled",
			config: mail.SecurityConfig{
				HeloValidation: true,
			},
			hostname:    "",
			remoteAddr:  "192.168.1.1:12345",
			wantError:   true,
			description: "should reject empty hostname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := mail.NewPolicy(tt.config)
			err := sm.ValidateHelo(tt.hostname, tt.remoteAddr)

			if (err != nil) != tt.wantError {
				t.Errorf("%s: ValidateHelo() error = %v, wantError %v", tt.description, err, tt.wantError)
			}
		})
	}
}

func TestSecurityManager_AuthFailureTracking(t *testing.T) {
	config := mail.SecurityConfig{
		MaxAuthFailures:   2,
		AuthFailureWindow: time.Minute,
		AuthFailureDelay:  time.Second,
	}

	sm := mail.NewPolicy(config)
	remoteAddr := "192.168.1.1:12345"

	// Record first failure
	sm.RecordAuthFailure(remoteAddr)

	// Record second failure
	sm.RecordAuthFailure(remoteAddr)

	// Connection should now fail validation due to auth failures
	err := sm.ValidateConnection(remoteAddr)
	if err == nil {
		t.Error("Connection should be blocked after max auth failures")
	}

	// Auth success should reset failures
	sm.RecordAuthSuccess(remoteAddr)

	// Connection should now succeed
	err = sm.ValidateConnection(remoteAddr)
	if err != nil {
		t.Errorf("Connection should succeed after auth success, got error: %v", err)
	}
}

func TestSecurityManager_ConnectionTracking(t *testing.T) {
	config := mail.SecurityConfig{
		MaxConnectionsPerIP: 2,
	}

	sm := mail.NewPolicy(config)
	remoteAddr := "192.168.1.1:12345"

	// First connection should succeed
	err := sm.ValidateConnection(remoteAddr)
	if err != nil {
		t.Errorf("First connection should succeed, got error: %v", err)
	}

	// Second connection should succeed
	err = sm.ValidateConnection(remoteAddr)
	if err != nil {
		t.Errorf("Second connection should succeed, got error: %v", err)
	}

	// Third connection should fail
	err = sm.ValidateConnection(remoteAddr)
	if err == nil {
		t.Error("Third connection should fail due to max connections limit")
	}

	// Close a connection
	sm.CloseConnection(remoteAddr)

	// New connection should now succeed
	err = sm.ValidateConnection(remoteAddr)
	if err != nil {
		t.Errorf("Connection should succeed after closing one, got error: %v", err)
	}
}

func TestSecurityManager_GetAuthFailureDelay(t *testing.T) {
	config := mail.SecurityConfig{
		MaxAuthFailures:   3,
		AuthFailureWindow: time.Minute,
		AuthFailureDelay:  time.Second * 2,
	}

	sm := mail.NewPolicy(config)
	delay := sm.AuthFailureDelay()

	if delay <= 0 {
		t.Error("Auth failure delay should be positive")
	}

	// Delay should be reasonable (between 1-5 seconds typically)
	if delay > 10*time.Second {
		t.Errorf("Auth failure delay seems too long: %v", delay)
	}
}

func TestSecurityManager_IPValidation(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []string
		blocklist []string
		testIP    string
		wantAllow bool
	}{
		{
			name:      "IP in allowlist",
			allowlist: []string{"192.168.1.0/24"},
			blocklist: []string{},
			testIP:    "192.168.1.100",
			wantAllow: true,
		},
		{
			name:      "IP in blocklist",
			allowlist: []string{},
			blocklist: []string{"192.168.2.0/24"},
			testIP:    "192.168.2.100",
			wantAllow: false,
		},
		{
			name:      "IP not in any list",
			allowlist: []string{},
			blocklist: []string{},
			testIP:    "10.0.0.1",
			wantAllow: true,
		},
		{
			name:      "specific IP in allowlist",
			allowlist: []string{"10.0.0.1"},
			blocklist: []string{},
			testIP:    "10.0.0.1",
			wantAllow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := mail.SecurityConfig{
				IPAllowlist:         tt.allowlist,
				IPBlocklist:         tt.blocklist,
				MaxConnectionsPerIP: 5,
			}

			sm := mail.NewPolicy(config)
			err := sm.ValidateConnection(tt.testIP + ":12345")

			isAllowed := err == nil
			if isAllowed != tt.wantAllow {
				t.Errorf("IP validation result = %v, want %v", isAllowed, tt.wantAllow)
			}
		})
	}
}

func TestSecurityError(t *testing.T) {
	secErr := &mail.SecurityError{
		Type:    "TEST_ERROR",
		Message: "Test security error",
	}

	errorMsg := secErr.Error()
	if errorMsg == "" {
		t.Error("SecurityError should return non-empty error message")
	}

	if errorMsg != "Test security error" {
		t.Errorf("Error message should match message field, got: %s", errorMsg)
	}
}
