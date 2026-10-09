package web

import (
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
	"golang.org/x/time/rate"
)

// Router is a custom HTTP router that supports middlewares and status callbacks
// It implements the http.Handler interface and allows for flexible request handling
type Router struct {
	mux                  *http.ServeMux
	canonicalDomain      string
	sorted               [][]Middleware
	middlewares          map[MiddlewareOrder][]Middleware
	onStatus             map[string]map[int]func(http.ResponseWriter, *http.Request)
	onStatusPatterns     map[string]struct{}
	limits               map[string]*limitStore
	limitedPatterns      map[string]struct{}
	cacheControl         map[string]string
	cacheControlPatterns map[string]struct{}
	headers              map[string]map[string]string
	headerPatterns       map[string]struct{}
	cors                 map[string]func(http.ResponseWriter, *http.Request)
	corsPatterns         map[string]struct{}
	whitelist            map[string]*net.IPNet
	blacklist            map[string]*net.IPNet
	honeypotCallback     func(map[string]*net.IPNet)
	routes               map[string]http.Handler // Track registered routes for unregistration
	mutex                sync.RWMutex            // Protect concurrent access to routes
	// state is an immutable snapshot of what a request needs to be dispatched. It is
	// published by publish after every change, so requests do not touch the mutex.
	state atomic.Pointer[routeState]
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
		mux:                  http.NewServeMux(),
		middlewares:          make(map[MiddlewareOrder][]Middleware),
		onStatus:             make(map[string]map[int]func(http.ResponseWriter, *http.Request)),
		onStatusPatterns:     make(map[string]struct{}),
		limits:               make(map[string]*limitStore),
		limitedPatterns:      make(map[string]struct{}),
		whitelist:            make(map[string]*net.IPNet),
		blacklist:            make(map[string]*net.IPNet),
		routes:               make(map[string]http.Handler),
		cacheControl:         make(map[string]string),
		cacheControlPatterns: make(map[string]struct{}),
		headers:              make(map[string]map[string]string),
		headerPatterns:       make(map[string]struct{}),
		cors:                 make(map[string]func(http.ResponseWriter, *http.Request)),
		corsPatterns:         make(map[string]struct{}),
	}

	r.publish()
	return r
}

// routeState is the part of the router a request reads before dispatching
type routeState struct {
	mux              *http.ServeMux
	sorted           [][]Middleware
	onStatusPatterns map[string]struct{}
	limitedPatterns  map[string]struct{}
	// limits is a copy published together with limitedPatterns, so a request that matched a
	// pattern in this snapshot always finds the store that belongs to it
	limits    map[string]*limitStore
	whitelist map[string]*net.IPNet
	blacklist map[string]*net.IPNet
}

// publish makes the current mux, middlewares and pattern sets visible to requests.
// Must be called with the mutex held after any of them changed.
func (router *Router) publish() {
	limits := make(map[string]*limitStore, len(router.limits))
	for pattern, store := range router.limits {
		limits[pattern] = store
	}
	router.state.Store(&routeState{
		limits:           limits,
		mux:              router.mux,
		sorted:           router.sorted,
		onStatusPatterns: router.onStatusPatterns,
		limitedPatterns:  router.limitedPatterns,
		whitelist:        router.whitelist,
		blacklist:        router.blacklist,
	})
}

// ServeHTTP implements the http.Handler interface for the Router
// It wraps the request with middlewares and handles the response
func (router *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state := router.state.Load()
	mux, onStatusPatterns, sorted := state.mux, state.onStatusPatterns, state.sorted

	rw := newResponseWriter(w, r)
	blocked := router.block(rw, r, state.whitelist, state.blacklist)
	if !blocked {
		if redirected := router.canonicalRedirect(rw, r); !redirected {
			router.rateLimit(rw, r, state.limitedPatterns, state.limits)
			router.wrapWith(sorted, mux).ServeHTTP(rw, r)
		}
	}
	router.handleStatusHooks(rw, r, onStatusPatterns)
	rw.flush()
}

// Use adds a middleware to the router
// It allows you to specify the order of execution using MiddlewareOrder
// All middlewares of the same order will be executed in the order they were added
func (router *Router) Use(order MiddlewareOrder, middleware func(http.Handler) http.Handler) {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	if _, ok := router.middlewares[order]; !ok {
		router.middlewares[order] = make([]Middleware, 0)
	}
	router.middlewares[order] = append(router.middlewares[order], middleware)
	router.sort()
	router.publish()
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
	router.onStatusPatterns = withPattern(router.onStatusPatterns, pattern)
	router.publish()
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
		router.limitedPatterns = withoutPattern(router.limitedPatterns, pattern)
		delete(router.cacheControl, pattern)
		delete(router.cacheControlPatterns, pattern)
		delete(router.headers, pattern)
		delete(router.headerPatterns, pattern)
		delete(router.cors, pattern)
		delete(router.corsPatterns, pattern)
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
	router.cacheControl = make(map[string]string)
	router.cacheControlPatterns = make(map[string]struct{})
	router.headers = make(map[string]map[string]string)
	router.headerPatterns = make(map[string]struct{})
	router.cors = make(map[string]func(http.ResponseWriter, *http.Request))
	router.corsPatterns = make(map[string]struct{})
	router.mux = http.NewServeMux()
	router.publish()
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
	router.publish()
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
	router.limitedPatterns = withPattern(router.limitedPatterns, pattern)
	router.publish()
	return nil
}

// registerCacheControl applies a custom Cache-Control header value to the given pattern
// It overrides both the default security header cache control and any server-wide value
// for requests matching the pattern
func (router *Router) registerCacheControl(pattern, value string) error {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	if _, exists := router.cacheControl[pattern]; exists {
		return apperror.NewErrorf("pattern %s already has a cache control value registered", pattern)
	}

	router.cacheControl[pattern] = value
	router.cacheControlPatterns[pattern] = struct{}{}
	return nil
}

// cacheControlFor returns the custom Cache-Control header value registered for the
// pattern matching the given path, if any
func (router *Router) cacheControlFor(path string) (string, bool) {
	router.mutex.RLock()
	defer router.mutex.RUnlock()

	matched := router.matchPattern(path, router.cacheControlPatterns)
	if matched == "" {
		return "", false
	}
	return router.cacheControl[matched], true
}

// registerHeader adds or overrides a single custom header value for the given pattern
// Multiple calls for the same pattern accumulate into the same header set, with later
// calls overriding earlier ones for the same key
func (router *Router) registerHeader(pattern, key, value string) {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	if _, ok := router.headers[pattern]; !ok {
		router.headers[pattern] = make(map[string]string)
	}
	router.headers[pattern][key] = value
	router.headerPatterns[pattern] = struct{}{}
}

// headersFor returns the custom headers registered for the pattern matching the given path, if any
func (router *Router) headersFor(path string) (map[string]string, bool) {
	router.mutex.RLock()
	defer router.mutex.RUnlock()

	matched := router.matchPattern(path, router.headerPatterns)
	if matched == "" {
		return nil, false
	}
	return router.headers[matched], true
}

// registerCORS applies a custom CORS configuration to the given pattern
// It overrides the server-wide CORS configuration for requests matching the pattern
func (router *Router) registerCORS(pattern string, config *CORSConfig) error {
	router.mutex.Lock()
	defer router.mutex.Unlock()

	if _, exists := router.cors[pattern]; exists {
		return apperror.NewErrorf("pattern %s already has a CORS configuration registered", pattern)
	}

	router.cors[pattern] = buildCORSApplier(config)
	router.corsPatterns[pattern] = struct{}{}
	return nil
}

// corsFor returns the CORS header applier registered for the pattern matching the given path, if any
func (router *Router) corsFor(path string) (func(http.ResponseWriter, *http.Request), bool) {
	router.mutex.RLock()
	defer router.mutex.RUnlock()

	matched := router.matchPattern(path, router.corsPatterns)
	if matched == "" {
		return nil, false
	}
	return router.cors[matched], true
}

// wrap applies all registered middlewares to the given handler
// It sorts the middlewares by their order and applies them LIFO (last in, first out)
func (router *Router) wrap(handler http.Handler) http.Handler {
	router.mutex.RLock()
	sorted := router.sorted
	router.mutex.RUnlock()
	return router.wrapWith(sorted, handler)
}

// wrapWith applies the given middlewares, which sort has already ordered, to the handler
func (router *Router) wrapWith(sorted [][]Middleware, handler http.Handler) http.Handler {
	for _, middlewares := range sorted {
		for i := len(middlewares) - 1; i >= 0; i-- {
			handler = middlewares[i](handler)
		}
	}

	return handler
}

// withPattern returns a copy of the set with the pattern added.
// The sets are read by requests without a lock, so they are replaced instead of modified.
func withPattern(set map[string]struct{}, pattern string) map[string]struct{} {
	copied := make(map[string]struct{}, len(set)+1)
	for k := range set {
		copied[k] = struct{}{}
	}
	copied[pattern] = struct{}{}
	return copied
}

// withoutPattern returns a copy of the set without the pattern
func withoutPattern(set map[string]struct{}, pattern string) map[string]struct{} {
	copied := make(map[string]struct{}, len(set))
	for k := range set {
		if k != pattern {
			copied[k] = struct{}{}
		}
	}
	return copied
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

func (router *Router) rateLimit(w http.ResponseWriter, r *http.Request, patterns map[string]struct{}, limits map[string]*limitStore) {
	matched := router.matchPattern(r.URL.Path, patterns)
	if matched != "" {
		store := limits[matched]
		if store == nil {
			return
		}

		ip := router.clientIP(r)
		if ip == "" {
			logger.Warn().Msg("rate limiting failed, no client IP found")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if !store.limiter(ip).Allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
	}
}

func (router *Router) canonicalRedirect(w http.ResponseWriter, r *http.Request) bool {
	if router.canonicalDomain == "" {
		return false
	}

	host := r.Host
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		host = strings.SplitN(xfh, ",", 2)[0]
		host = strings.TrimSpace(host)
	}

	addr := strings.Split(host, ":")

	port := ""
	domain := addr[0]
	if len(addr) > 1 {
		port = ":" + addr[1]
	}

	if domain != router.canonicalDomain {
		protocol := "http"
		if r.TLS != nil {
			protocol = "https"
		} else if xfp := r.Header.Get("X-Forwarded-Proto"); xfp != "" {
			candidate := strings.ToLower(strings.TrimSpace(strings.SplitN(xfp, ",", 2)[0]))
			if candidate == "https" || candidate == "http" {
				protocol = candidate
			}
		}

		logger.Trace().Fields(logging.F("host", host), logging.F("domain", router.canonicalDomain), logging.F("port", port), logging.F("protocol", protocol)).Msg("redirecting to canonical domain")
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
	if !router.ipInList(ip, router.state.Load().whitelist) {
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
		// Requests read the blacklist without a lock, so add to a copy and publish it
		router.mutex.Lock()
		blacklist := make(map[string]*net.IPNet, len(router.blacklist)+1)
		for k, v := range router.blacklist {
			blacklist[k] = v
		}
		blacklist[network.String()] = network
		router.blacklist = blacklist
		router.publish()
		callback := router.honeypotCallback
		router.mutex.Unlock()

		if callback != nil {
			callback(blacklist)
		}
	}
}

// block blocks all requests to the router coming from a IP address defined in the blacklist
func (router *Router) block(w http.ResponseWriter, r *http.Request, whitelist, blacklist map[string]*net.IPNet) bool {
	ipStr := router.clientIP(r)

	// Without lists there is nothing to look up, so the address only has to be valid
	if len(blacklist) == 0 {
		if !validClientIP(ipStr) {
			logger.Warn().Fields(logging.F("ip", ipStr)).Msg("blocked request with invalid IP address")
			http.Error(w, "Invalid IP address", http.StatusBadRequest)
			return true
		}
		return false
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		logger.Warn().Fields(logging.F("ip", ipStr)).Msg("blocked request with invalid IP address")
		http.Error(w, "Invalid IP address", http.StatusBadRequest)
		return true
	}

	if !router.ipInList(ip, whitelist) && router.ipInList(ip, blacklist) {
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
	whitelist := make(map[string]*net.IPNet, len(networks))
	for _, network := range networks {
		whitelist[network.String()] = network
	}

	router.mutex.Lock()
	router.whitelist = whitelist
	router.publish()
	router.mutex.Unlock()
	return nil
}

func (router *Router) setBlacklist(entries []string) error {
	networks, err := router.parseIPList(entries)
	if err != nil {
		return apperror.Wrap(err)
	}
	blacklist := make(map[string]*net.IPNet, len(networks))
	for _, network := range networks {
		blacklist[network.String()] = network
	}

	router.mutex.Lock()
	router.blacklist = blacklist
	router.publish()
	router.mutex.Unlock()
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
	// The keys are already in canonical form, so index the map instead of canonicalizing them
	// again for every request, which is what Header.Get does
	if values := r.Header["X-Forwarded-For"]; len(values) > 0 && values[0] != "" {
		xff := values[0]
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		return strings.TrimSpace(xff)
	}

	if values := r.Header["X-Real-Ip"]; len(values) > 0 && values[0] != "" {
		return strings.TrimSpace(values[0])
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

// validClientIP reports whether net.ParseIP would accept the address, without allocating
func validClientIP(s string) bool {
	addr, err := netip.ParseAddr(s)
	return err == nil && addr.Zone() == ""
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
