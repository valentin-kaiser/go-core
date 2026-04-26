package web

import (
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
	"golang.org/x/time/rate"
)

// Router is a custom HTTP router that supports middlewares and status callbacks
// It implements the http.Handler interface and allows for flexible request handling
type Router struct {
	mux              *http.ServeMux
	canonicalDomain  string
	sorted           [][]Middleware
	middlewares      map[MiddlewareOrder][]Middleware
	onStatus         map[string]map[int]func(http.ResponseWriter, *http.Request)
	onStatusPatterns map[string]struct{}
	limits           map[string]*limitStore
	limitedPatterns  map[string]struct{}
	whitelist        map[string]*net.IPNet
	blacklist        map[string]*net.IPNet
	honeypotCallback func(map[string]*net.IPNet)
	routes           map[string]http.Handler // Track registered routes for unregistration
	mutex            sync.RWMutex            // Protect concurrent access to routes
}

type limitStore struct {
	mutex sync.Mutex
	limit rate.Limit
	burst int
	state map[string]*rate.Limiter
}

func (ls *limitStore) limiter(ip string) *rate.Limiter {
	ls.mutex.Lock()
	defer ls.mutex.Unlock()
	if limiter, exists := ls.state[ip]; exists {
		return limiter
	}
	limiter := rate.NewLimiter(ls.limit, ls.burst)
	ls.state[ip] = limiter
	return limiter
}

// NewRouter creates a new Router instance
// It initializes the ServeMux and the middlewares map
func NewRouter() *Router {
	r := &Router{
		mux:              http.NewServeMux(),
		middlewares:      make(map[MiddlewareOrder][]Middleware),
		onStatus:         make(map[string]map[int]func(http.ResponseWriter, *http.Request)),
		onStatusPatterns: make(map[string]struct{}),
		limits:           make(map[string]*limitStore),
		limitedPatterns:  make(map[string]struct{}),
		whitelist:        make(map[string]*net.IPNet),
		blacklist:        make(map[string]*net.IPNet),
		routes:           make(map[string]http.Handler),
	}

	return r
}

// ServeHTTP implements the http.Handler interface for the Router
// It wraps the request with middlewares and handles the response
func (router *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	router.mutex.RLock()
	mux := router.mux
	onStatusPatterns := make(map[string]struct{})
	for k, v := range router.onStatusPatterns {
		onStatusPatterns[k] = v
	}
	limitedPatterns := make(map[string]struct{})
	for k, v := range router.limitedPatterns {
		limitedPatterns[k] = v
	}
	router.mutex.RUnlock()

	rw := newResponseWriter(w, r)
	blocked := router.block(rw, r)
	if !blocked {
		if redirected := router.canonicalRedirect(rw, r); !redirected {
			router.rateLimit(rw, r, limitedPatterns)
			router.wrap(mux).ServeHTTP(rw, r)
		}
	}
	router.handleStatusHooks(rw, r, onStatusPatterns)
	rw.flush()
}

// Use adds a middleware to the router
// It allows you to specify the order of execution using MiddlewareOrder
// All middlewares of the same order will be executed in the order they were added
func (router *Router) Use(order MiddlewareOrder, middleware func(http.Handler) http.Handler) {
	if _, ok := router.middlewares[order]; !ok {
		router.middlewares[order] = make([]Middleware, 0)
	}
	router.middlewares[order] = append(router.middlewares[order], middleware)
	router.sort()
}

// Handle registers a handler for the given pattern
func (router *Router) Handle(pattern string, handler http.Handler) {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	router.mux.Handle(pattern, handler)
	router.routes[pattern] = handler
}

// HandleFunc registers a handler function for the given pattern
func (router *Router) HandleFunc(pattern string, handlerFunc http.HandlerFunc) {
	router.Handle(pattern, handlerFunc)
}

// OnStatus registers a callback function for a specific HTTP status code
// This function will be called after the response is written if the status and pattern match
// It allows you to handle specific status codes, such as logging or a custom response
func (router *Router) OnStatus(pattern string, status int, fn func(http.ResponseWriter, *http.Request)) {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	if _, ok := router.onStatus[pattern]; !ok {
		router.onStatus[pattern] = make(map[int]func(http.ResponseWriter, *http.Request))
	}
	router.onStatus[pattern][status] = fn
	router.onStatusPatterns[pattern] = struct{}{}
}

// UnregisterHandler removes routes from the router
// It removes the route pattern from all relevant internal maps and recreates the ServeMux
func (router *Router) UnregisterHandler(patterns []string) {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	var notFound []string
	for _, pattern := range patterns {
		if _, exists := router.routes[pattern]; !exists {
			notFound = append(notFound, pattern)
		}
	}

	if len(notFound) > 0 {
		return
	}

	for _, pattern := range patterns {
		delete(router.routes, pattern)
		delete(router.limits, pattern)
		delete(router.limitedPatterns, pattern)
	}

	router.rebuildMux()
}

// UnregisterAllHandler removes all routes from the router
// It clears all route-related maps and creates a new empty ServeMux
func (router *Router) UnregisterAllHandler() {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	router.routes = make(map[string]http.Handler)
	router.limits = make(map[string]*limitStore)
	router.limitedPatterns = make(map[string]struct{})
	router.mux = http.NewServeMux()
}

// GetRegisteredRoutes returns a slice of all currently registered route patterns
func (router *Router) GetRegisteredRoutes() []string {
	router.mutex.RLock()
	defer router.mutex.RUnlock()

	patterns := make([]string, 0, len(router.routes))
	for pattern := range router.routes {
		patterns = append(patterns, pattern)
	}
	return patterns
}

// rebuildMux recreates the ServeMux and re-registers all remaining routes
// This method should be called with the mutex already locked
func (router *Router) rebuildMux() {
	router.mux = http.NewServeMux()

	for pattern, handler := range router.routes {
		router.mux.Handle(pattern, handler)
	}
}

// registerRateLimit applies a rate limit to the given pattern
// It uses the golang.org/x/time/rate package to limit the number of requests
func (router *Router) registerRateLimit(pattern string, limit rate.Limit, burst int) error {
	if limit <= 0 || burst <= 0 {
		return apperror.NewErrorf("invalid rate limit or burst value: limit=%v, burst=%d", limit, burst)
	}

	router.mutex.Lock()
	defer router.mutex.Unlock()

	if _, exists := router.limits[pattern]; exists {
		return apperror.NewErrorf("pattern %s is already rate limited", pattern)
	}

	router.limits[pattern] = &limitStore{
		limit: limit,
		burst: burst,
		state: make(map[string]*rate.Limiter),
	}
	router.limitedPatterns[pattern] = struct{}{}
	return nil
}

// wrap applies all registered middlewares to the given handler
// It sorts the middlewares by their order and applies them LIFO (last in, first out)
func (router *Router) wrap(handler http.Handler) http.Handler {
	for _, middlewares := range router.sorted {
		for i := len(middlewares) - 1; i >= 0; i-- {
			handler = middlewares[i](handler)
		}
	}

	return handler
}

func (router *Router) sort() {
	router.sorted = make([][]Middleware, 0, len(router.middlewares))
	orders := make([]MiddlewareOrder, 0, len(router.middlewares))
	for order := range router.middlewares {
		orders = append(orders, order)
	}
	sort.Slice(orders, func(i, j int) bool {
		return orders[i] > orders[j]
	})

	for _, order := range orders {
		router.sorted = append(router.sorted, router.middlewares[order])
	}
}

func (router *Router) handleStatusHooks(rw *ResponseWriter, r *http.Request, patterns map[string]struct{}) {
	if matched := router.matchPattern(r.URL.Path, patterns); matched != "" {
		router.mutex.RLock()
		fn, ok := router.onStatus[matched][rw.status]
		router.mutex.RUnlock()

		if ok {
			rw.clear()
			fn(rw, r)
		}
	}
}

func (router *Router) rateLimit(w http.ResponseWriter, r *http.Request, patterns map[string]struct{}) {
	matched := router.matchPattern(r.URL.Path, patterns)
	if matched != "" {
		ip := router.clientIP(r)
		if ip == "" {
			logger.Warn().Msg("rate limiting failed, no client IP found")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		router.mutex.RLock()
		limiter := router.limits[matched].limiter(ip)
		router.mutex.RUnlock()

		if !limiter.Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
	}
}

func (router *Router) canonicalRedirect(w http.ResponseWriter, r *http.Request) bool {
	if router.canonicalDomain == "" {
		return false
	}

	addr := strings.Split(r.Host, ":")

	port := ""
	domain := addr[0]
	if len(addr) > 1 {
		port = ":" + addr[1]
	}

	if domain != router.canonicalDomain {
		protocol := "http"
		if r.TLS != nil {
			protocol = "https"
		}

		logger.Trace().Fields(logging.F("host", r.Host), logging.F("domain", router.canonicalDomain), logging.F("port", port), logging.F("protocol", protocol)).Msg("redirecting to canonical domain")
		http.Redirect(w, r, protocol+"://"+router.canonicalDomain+port+r.RequestURI, http.StatusMovedPermanently)
		return true
	}
	return false
}

func (router *Router) honeypot(w http.ResponseWriter, r *http.Request) {
	ipStr := router.clientIP(r)

	ip := net.ParseIP(ipStr)
	if ip == nil {
		logger.Warn().Fields(logging.F("ip", ipStr)).Msg("honeypot accessed with invalid IP address")
		http.Error(w, "Invalid IP address", http.StatusBadRequest)
		return
	}

	logger.Trace().Fields(logging.F("ip", ip.String())).Msg("honeypot triggered, checking IP address")
	if !router.ipInList(ip, router.whitelist) {
		cidr := ip.String() + "/32"
		if ip.To4() == nil {
			cidr = ip.String() + "/128" // Use /128 for IPv6 addresses
		}

		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			logger.Error().Fields(logging.F("error", err), logging.F("ip", cidr)).Msg("failed to parse IP address for honeypot")
			return
		}

		logger.Debug().Fields(logging.F("ip", network.String())).Msg("honeypot triggered, blocking IP address")
		router.blacklist[network.String()] = network
		if router.honeypotCallback != nil {
			router.honeypotCallback(router.blacklist)
		}
	}
}

// block blocks all requests to the router coming from a IP address defined in the blacklist
func (router *Router) block(w http.ResponseWriter, r *http.Request) bool {
	ipStr := router.clientIP(r)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		logger.Warn().Fields(logging.F("ip", ipStr)).Msg("blocked request with invalid IP address")
		http.Error(w, "Invalid IP address", http.StatusBadRequest)
		return true
	}

	if !router.ipInList(ip, router.whitelist) && router.ipInList(ip, router.blacklist) {
		logger.Trace().Fields(logging.F("ip", ip.String())).Msg("blocked request from IP address")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return true
	}

	return false
}

func (router *Router) setWhitelist(entries []string) error {
	networks, err := router.parseIPList(entries)
	if err != nil {
		return apperror.Wrap(err)
	}
	router.whitelist = make(map[string]*net.IPNet, len(networks))
	for _, network := range networks {
		router.whitelist[network.String()] = network
	}
	return nil
}

func (router *Router) setBlacklist(entries []string) error {
	networks, err := router.parseIPList(entries)
	if err != nil {
		return apperror.Wrap(err)
	}
	router.blacklist = make(map[string]*net.IPNet, len(networks))
	for _, network := range networks {
		router.blacklist[network.String()] = network
	}
	return nil
}

func (router *Router) matchPattern(path string, patterns map[string]struct{}) (matched string) {
	for pattern := range patterns {
		if pattern == path ||
			(strings.HasSuffix(pattern, "/") && strings.HasPrefix(path, pattern)) ||
			pattern == "/" {
			if len(pattern) > len(matched) {
				matched = pattern
			}
		}
	}
	return
}

func (router *Router) clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	if xRealIP := r.Header.Get("X-Real-IP"); xRealIP != "" {
		return strings.TrimSpace(xRealIP)
	}

	if r.RemoteAddr != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}

	return ""
}

func (router *Router) ipInList(ip net.IP, list map[string]*net.IPNet) bool {
	for _, network := range list {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (router *Router) parseIPList(entries []string) ([]*net.IPNet, error) {
	var list []*net.IPNet
	for _, entry := range entries {
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil {
				if ip.To4() != nil {
					entry += "/32" // Use /32 for IPv4 addresses
				}
				if ip.To4() == nil {
					entry += "/128" // Use /128 for IPv6 addresses
				}
			}
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, apperror.NewErrorf("failed to parse CIDR entry '%s'", entry).AddError(err)
		}
		list = append(list, network)
	}
	return list, nil
}
