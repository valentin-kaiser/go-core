package mail

import (
	"context"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// FQDNPattern validates a fully qualified domain name (FQDN) with the following rules:
// - One or more labels separated by dots.
// - Each label is 1-63 characters: alphanumeric and hyphens, but cannot start or end with a hyphen.
// - The final label (TLD) must be alphabetic and at least one character.
var FQDNPattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{1,}$`)

// SecurityManager handles security validations and tracking for both SMTP client and server
type SecurityManager struct {
	config        SecurityConfig
	ipConnections map[string]int
	ipRateLimit   map[string]struct {
		*rate.Limiter
		last time.Time
	}
	authFailures    map[string]*authTracker
	allowedNetworks []*net.IPNet
	blockedNetworks []*net.IPNet
	mutex           sync.RWMutex
}

// authTracker tracks authentication failures per IP
type authTracker struct {
	failures    int
	windowStart time.Time
	blocked     bool
	blockedTime time.Time
}

// NewSecurityManager creates a new security manager
func NewSecurityManager(config SecurityConfig) *SecurityManager {
	sm := &SecurityManager{
		config:        config,
		ipConnections: make(map[string]int),
		ipRateLimit: make(map[string]struct {
			*rate.Limiter
			last time.Time
		}),
		authFailures: make(map[string]*authTracker),
		mutex:        sync.RWMutex{},
	}

	// Parse IP allowlist
	for _, ipStr := range config.IPAllowlist {
		_, parsedNetwork, parseErr := net.ParseCIDR(ipStr)
		if parseErr == nil {
			sm.allowedNetworks = append(sm.allowedNetworks, parsedNetwork)
			continue
		}

		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}

		// Single IP address - convert to /32 or /128 network
		var network *net.IPNet
		_, network, _ = net.ParseCIDR(ipStr + "/128") // Default to IPv6
		if ip.To4() != nil {
			_, network, _ = net.ParseCIDR(ipStr + "/32") // Override for IPv4
		}
		sm.allowedNetworks = append(sm.allowedNetworks, network)
	}

	// Parse IP blocklist
	for _, ipStr := range config.IPBlocklist {
		_, blockNetwork, blockErr := net.ParseCIDR(ipStr)
		if blockErr == nil {
			sm.blockedNetworks = append(sm.blockedNetworks, blockNetwork)
			continue
		}

		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}

		// Single IP address - convert to /32 or /128 network
		var network *net.IPNet
		_, network, _ = net.ParseCIDR(ipStr + "/128") // Default to IPv6
		if ip.To4() != nil {
			_, network, _ = net.ParseCIDR(ipStr + "/32") // Override for IPv4
		}
		sm.blockedNetworks = append(sm.blockedNetworks, network)
	}

	// Start cleanup goroutine
	go sm.cleanup()

	return sm
}

// ValidateConnection checks if a connection from the given IP is allowed
func (sm *SecurityManager) ValidateConnection(remoteAddr string) error {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("remote_addr", remoteAddr).Msg("invalid IP address in connection")
		}
		return &SecurityError{Type: "invalid_ip", Message: "invalid IP address"}
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	// Check if IP is blocked
	if sm.isIPBlocked(ip) {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", ip.String()).Msg("connection blocked - IP in blocklist")
		}
		return &SecurityError{Type: "ip_blocked", Message: "IP address is blocked"}
	}

	// Check if IP is in allowlist (if allowlist is configured)
	if len(sm.allowedNetworks) > 0 && !sm.isIPAllowed(ip) {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", ip.String()).Msg("connection denied - IP not in allowlist")
		}
		return &SecurityError{Type: "ip_not_allowed", Message: "IP address is not allowed"}
	}

	// Check connection limits per IP
	if sm.config.MaxConnectionsPerIP > 0 {
		currentConnections := sm.ipConnections[ip.String()]
		if currentConnections >= sm.config.MaxConnectionsPerIP {
			if sm.config.LogSecurityEvents {
				logger.Warn().
					Field("ip", ip.String()).
					Field("connections", currentConnections).
					Msg("connection limit exceeded")
			}
			return &SecurityError{Type: "connection_limit", Message: "too many connections from this IP"}
		}
	}

	// Check if IP is blocked due to auth failures
	if tracker, exists := sm.authFailures[ip.String()]; exists && tracker.blocked {
		if time.Since(tracker.blockedTime) < sm.config.AuthFailureWindow {
			if sm.config.LogSecurityEvents {
				logger.Warn().Field("ip", ip.String()).Msg("connection blocked due to authentication failures")
			}
			return &SecurityError{Type: "auth_blocked", Message: "IP temporarily blocked due to authentication failures"}
		}

		// Reset auth failure tracking after window expires
		tracker.blocked = false
		tracker.failures = 0
	}

	// Increment connection count
	sm.ipConnections[ip.String()]++

	if sm.config.LogSecurityEvents {
		logger.Debug().
			Field("ip", ip.String()).
			Field("connections", sm.ipConnections[ip.String()]).
			Msg("connection accepted")
	}

	return nil
}

// ValidateHelo validates HELO/EHLO hostname
func (sm *SecurityManager) ValidateHelo(hostname, remoteAddr string) error {
	if !sm.config.HeloValidation {
		return nil
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	// Basic hostname validation
	if hostname == "" {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", host).Msg("empty HELO hostname")
		}
		return &SecurityError{Type: "empty_helo", Message: "HELO hostname cannot be empty"}
	}

	// Check for FQDN requirement
	if sm.config.HeloRequireFQDN {
		if !sm.isValidFQDN(hostname) {
			if sm.config.LogSecurityEvents {
				logger.Warn().Field("ip", host).Field("hostname", hostname).Msg("invalid FQDN in HELO")
			}
			return &SecurityError{Type: "invalid_fqdn", Message: "HELO hostname must be a valid FQDN"}
		}
	}

	// DNS validation
	if sm.config.HeloDNSCheck {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Try to resolve the hostname
		_, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, hostname)
		if lookupErr != nil {
			if sm.config.LogSecurityEvents {
				logger.Warn().Field("ip", host).Field("hostname", hostname).Err(lookupErr).Msg("DNS resolution failed for HELO hostname")
			}
			return &SecurityError{Type: "dns_resolution_failed", Message: "HELO hostname cannot be resolved"}
		}
	}

	if sm.config.LogSecurityEvents {
		logger.Debug().Field("ip", host).Field("hostname", hostname).Msg("HELO validation passed")
	}

	return nil
}

// CheckRateLimit checks if the IP has exceeded rate limits
func (sm *SecurityManager) CheckRateLimit(remoteAddr string) error {
	if sm.config.RateLimitPerIP <= 0 {
		return nil
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	limiter, exists := sm.ipRateLimit[host]
	if !exists {
		sm.ipRateLimit[host] = struct {
			*rate.Limiter
			last time.Time
		}{
			Limiter: rate.NewLimiter(rate.Limit(sm.config.RateLimitPerIP), 1),
			last:    time.Now(),
		}
		return nil
	}

	limiter.last = time.Now()
	if !limiter.Allow() {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", host).Msg("rate limit exceeded")
		}
		return &SecurityError{Type: "rate_limit", Message: "Rate limit exceeded"}
	}

	return nil
}

// RecordAuthFailure records an authentication failure
func (sm *SecurityManager) RecordAuthFailure(remoteAddr string) {
	if sm.config.MaxAuthFailures <= 0 {
		return
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	now := time.Now()
	tracker, exists := sm.authFailures[host]

	if !exists {
		sm.authFailures[host] = &authTracker{
			failures:    1,
			windowStart: now,
		}
		return
	}

	// Reset window if it's been longer than the auth failure window
	if now.Sub(tracker.windowStart) > sm.config.AuthFailureWindow {
		tracker.failures = 1
		tracker.windowStart = now
		tracker.blocked = false
		return
	}

	tracker.failures++
	if tracker.failures >= sm.config.MaxAuthFailures {
		tracker.blocked = true
		tracker.blockedTime = now
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", host).Field("failures", tracker.failures).Msg("IP blocked due to authentication failures")
		}
	}
}

// RecordAuthSuccess records a successful authentication
func (sm *SecurityManager) RecordAuthSuccess(remoteAddr string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	// Reset auth failure tracking on successful auth
	if tracker, exists := sm.authFailures[host]; exists {
		tracker.failures = 0
		tracker.blocked = false
	}
}

// CloseConnection decrements the connection count for an IP
func (sm *SecurityManager) CloseConnection(remoteAddr string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sm.ipConnections[host] > 0 {
		sm.ipConnections[host]--
		if sm.ipConnections[host] == 0 {
			delete(sm.ipConnections, host)
		}
	}

	if sm.config.LogSecurityEvents {
		logger.Debug().Field("ip", host).Field("remaining_connections", sm.ipConnections[host]).Msg("connection closed")
	}
}

// GetAuthFailureDelay returns the delay to apply after auth failure
func (sm *SecurityManager) GetAuthFailureDelay() time.Duration {
	return sm.config.AuthFailureDelay
}

// isIPAllowed checks if IP is in allowlist
func (sm *SecurityManager) isIPAllowed(ip net.IP) bool {
	for _, network := range sm.allowedNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// isIPBlocked checks if IP is in blocklist
func (sm *SecurityManager) isIPBlocked(ip net.IP) bool {
	for _, network := range sm.blockedNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// isValidFQDN validates if hostname is a proper FQDN
func (sm *SecurityManager) isValidFQDN(hostname string) bool {
	// Basic FQDN validation
	if len(hostname) == 0 || len(hostname) > 255 {
		return false
	}

	// Must contain at least one dot
	if !strings.Contains(hostname, ".") {
		return false
	}

	// Must not start or end with dot
	if strings.HasPrefix(hostname, ".") || strings.HasSuffix(hostname, ".") {
		return false
	}

	// Validate format using regex
	return FQDNPattern.MatchString(hostname)
}

// cleanup periodically cleans up old entries
func (sm *SecurityManager) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.mutex.Lock()
		now := time.Now()

		// Clean up rate limiters
		for ip, limiter := range sm.ipRateLimit {
			if time.Since(limiter.last) > 2*time.Minute {
				delete(sm.ipRateLimit, ip)
			}
		}

		// Clean up auth trackers
		for ip, tracker := range sm.authFailures {
			if now.Sub(tracker.windowStart) > 2*sm.config.AuthFailureWindow {
				delete(sm.authFailures, ip)
			}
		}

		sm.mutex.Unlock()
	}
}

// SecurityError represents a security-related error
type SecurityError struct {
	Type    string
	Message string
}

func (e *SecurityError) Error() string {
	return e.Message
}
