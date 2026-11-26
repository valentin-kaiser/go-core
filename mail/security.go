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

// Security manager constants.
const (
	ipv4Bits = 32
	ipv6Bits = 128

	// heloDNSTimeout bounds the DNS lookup of a HELO hostname.
	heloDNSTimeout = 5 * time.Second
	// maxFQDNLength is the maximum accepted length of a HELO hostname.
	maxFQDNLength = 255

	// cleanupInterval is the interval between cleanups of stale tracking entries.
	cleanupInterval = 5 * time.Minute
	// rateLimiterIdleTimeout is the idle time after which a rate limiter is dropped.
	rateLimiterIdleTimeout = 2 * time.Minute
	// authTrackerWindowFactor scales the auth failure window after which a tracker is dropped.
	authTrackerWindowFactor = 2
)

// FQDNPattern validates a fully qualified domain name (FQDN) with the following rules:
// - One or more labels separated by dots.
// - Each label is 1-63 characters: alphanumeric and hyphens, but cannot start or end with a hyphen.
// - The final label (TLD) must be alphabetic and at least one character.
var FQDNPattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{1,}$`)

// SecurityError represents a security-related error
type SecurityError struct {
	Type    string
	Message string
}

func (e *SecurityError) Error() string {
	return e.Message
}

// authTracker tracks authentication failures per IP
type authTracker struct {
	failures    int
	windowStart time.Time
	blocked     bool
	blockedTime time.Time
}

// Policy handles security validations and tracking for both SMTP client and server
type Policy struct {
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

// NewPolicy creates a new security manager
func NewPolicy(config SecurityConfig) *Policy {
	sm := &Policy{
		config:        config,
		ipConnections: make(map[string]int),
		ipRateLimit: make(map[string]struct {
			*rate.Limiter

			last time.Time
		}),
		authFailures: make(map[string]*authTracker),
		mutex:        sync.RWMutex{},
	}

	sm.allowedNetworks = parseNetworks(config.IPAllowlist)
	sm.blockedNetworks = parseNetworks(config.IPBlocklist)

	go sm.cleanup()

	return sm
}

// ValidateConnection checks if a connection from the given IP is allowed
func (sm *Policy) ValidateConnection(remoteAddr string) error {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("remote_addr", remoteAddr).Msg("invalid IP address in connection")
		}
		return &SecurityError{Type: "invalid_ip", Message: "invalid ip address"}
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sm.isIPBlocked(ip) {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", ip.String()).Msg("connection blocked - IP in blocklist")
		}
		return &SecurityError{Type: "ip_blocked", Message: "ip address is blocked"}
	}

	if len(sm.allowedNetworks) > 0 && !sm.isIPAllowed(ip) {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", ip.String()).Msg("connection denied - IP not in allowlist")
		}
		return &SecurityError{Type: "ip_not_allowed", Message: "ip address is not allowed"}
	}

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

	if tracker, exists := sm.authFailures[ip.String()]; exists && tracker.blocked {
		if time.Since(tracker.blockedTime) < sm.config.AuthFailureWindow {
			if sm.config.LogSecurityEvents {
				logger.Warn().Field("ip", ip.String()).Msg("connection blocked due to authentication failures")
			}
			return &SecurityError{Type: "auth_blocked", Message: "ip temporarily blocked due to authentication failures"}
		}
		tracker.blocked = false
		tracker.failures = 0
	}

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
func (sm *Policy) ValidateHelo(hostname, remoteAddr string) error {
	if !sm.config.HeloValidation {
		return nil
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	if hostname == "" {
		if sm.config.LogSecurityEvents {
			logger.Warn().Field("ip", host).Msg("empty HELO hostname")
		}
		return &SecurityError{Type: "empty_helo", Message: "helo hostname cannot be empty"}
	}

	if sm.config.HeloRequireFQDN {
		if !sm.isValidFQDN(hostname) {
			if sm.config.LogSecurityEvents {
				logger.Warn().Field("ip", host).Field("hostname", hostname).Msg("invalid FQDN in HELO")
			}
			return &SecurityError{Type: "invalid_fqdn", Message: "helo hostname must be a valid FQDN"}
		}
	}

	if sm.config.HeloDNSCheck {
		ctx, cancel := context.WithTimeout(context.Background(), heloDNSTimeout)
		defer cancel()
		_, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, hostname)
		if lookupErr != nil {
			if sm.config.LogSecurityEvents {
				logger.Warn().Field("ip", host).Field("hostname", hostname).Err(lookupErr).Msg("DNS resolution failed for HELO hostname")
			}
			return &SecurityError{Type: "dns_resolution_failed", Message: "helo hostname cannot be resolved"}
		}
	}

	if sm.config.LogSecurityEvents {
		logger.Debug().Field("ip", host).Field("hostname", hostname).Msg("HELO validation passed")
	}

	return nil
}

// CheckRateLimit checks if the IP has exceeded rate limits
func (sm *Policy) CheckRateLimit(remoteAddr string) error {
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
		return &SecurityError{Type: "rate_limit", Message: "rate limit exceeded"}
	}

	return nil
}

// RecordAuthFailure records an authentication failure
func (sm *Policy) RecordAuthFailure(remoteAddr string) {
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
func (sm *Policy) RecordAuthSuccess(remoteAddr string) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}

	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if tracker, exists := sm.authFailures[host]; exists {
		tracker.failures = 0
		tracker.blocked = false
	}
}

// CloseConnection decrements the connection count for an IP
func (sm *Policy) CloseConnection(remoteAddr string) {
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

// AuthFailureDelay returns the delay to apply after auth failure
func (sm *Policy) AuthFailureDelay() time.Duration {
	return sm.config.AuthFailureDelay
}

// isIPAllowed checks if IP is in allowlist
func (sm *Policy) isIPAllowed(ip net.IP) bool {
	for _, network := range sm.allowedNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// isIPBlocked checks if IP is in blocklist
func (sm *Policy) isIPBlocked(ip net.IP) bool {
	for _, network := range sm.blockedNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// isValidFQDN validates if hostname is a proper FQDN
func (sm *Policy) isValidFQDN(hostname string) bool {
	if len(hostname) == 0 || len(hostname) > maxFQDNLength {
		return false
	}

	if !strings.Contains(hostname, ".") {
		return false
	}

	if strings.HasPrefix(hostname, ".") || strings.HasSuffix(hostname, ".") {
		return false
	}

	return FQDNPattern.MatchString(hostname)
}

// cleanup periodically cleans up old entries
func (sm *Policy) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		sm.mutex.Lock()
		now := time.Now()
		for ip, limiter := range sm.ipRateLimit {
			if time.Since(limiter.last) > rateLimiterIdleTimeout {
				delete(sm.ipRateLimit, ip)
			}
		}
		for ip, tracker := range sm.authFailures {
			if now.Sub(tracker.windowStart) > authTrackerWindowFactor*sm.config.AuthFailureWindow {
				delete(sm.authFailures, ip)
			}
		}
		sm.mutex.Unlock()
	}
}

// parseNetworks converts CIDR notations and single IP addresses to networks and skips invalid entries.
func parseNetworks(entries []string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		_, network, err := net.ParseCIDR(entry)
		if err == nil {
			networks = append(networks, network)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			logger.Warn().Err(err).Field("entry", entry).Msg("ignoring invalid ip entry")
			continue
		}
		// Single IP address - convert to a /32 or /128 network
		if ip4 := ip.To4(); ip4 != nil {
			networks = append(networks, &net.IPNet{IP: ip4, Mask: net.CIDRMask(ipv4Bits, ipv4Bits)})
			continue
		}
		networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(ipv6Bits, ipv6Bits)})
	}
	return networks
}
