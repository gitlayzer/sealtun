package tunnel

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/labring/sealtun/pkg/accesspolicy"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/publicauth"
)

type ServerOptions struct {
	BasicAuth    *publicauth.BasicAuth
	AccessPolicy *accesspolicy.Policy
	TargetURL    string
}

type Server struct {
	secret                     string
	port                       int
	protocol                   string
	localPort                  string
	targetURL                  string
	targetHost                 string
	basicAuth                  *publicauth.BasicAuth
	accessPolicy               *accesspolicy.Policy
	basicAuthAuthorizedHeaders sync.Map
	basicAuthCacheCount        atomic.Int64
	rateLimiter                *accesspolicy.RateLimiter
	auditMu                    sync.Mutex
	auditEvents                []accesspolicy.AuditEvent
	auditStart                 int
	streamOpenSlots            chan struct{}
	rawTCPSlots                chan struct{}

	mu            sync.RWMutex
	activeSession *yamux.Session
	upgrader      websocket.Upgrader
	reverseProxy  *httputil.ReverseProxy
	connectedAt   atomic.Int64

	totalRequests      atomic.Int64
	activeRequests     atomic.Int64
	totalResponseBytes atomic.Int64
	total5xx           atomic.Int64
	lastStatus         atomic.Int64
	lastRequestAt      atomic.Int64
	totalDurationMs    atomic.Int64

	totalTCPConnections  atomic.Int64
	activeTCPConnections atomic.Int64
	totalTCPBytes        atomic.Int64
	totalTCPErrors       atomic.Int64
	lastTCPConnectedAt   atomic.Int64
}

func NewServer(secret string, port int, protocol string, localPort string) *Server {
	return NewServerWithOptions(secret, port, protocol, localPort, ServerOptions{})
}

func NewServerWithOptions(secret string, port int, protocol string, localPort string, opts ServerOptions) *Server {
	rateLimiter := (*accesspolicy.RateLimiter)(nil)
	if opts.AccessPolicy != nil && strings.TrimSpace(opts.AccessPolicy.RateLimit) != "" {
		if spec, err := accesspolicy.ParseRateLimit(opts.AccessPolicy.RateLimit); err == nil {
			rateLimiter = accesspolicy.NewRateLimiter(spec)
		}
	}
	targetURL := strings.TrimSpace(opts.TargetURL)
	if targetURL == "" {
		if target, err := LocalhostTarget(localPort); err == nil {
			targetURL = target.URL
		}
	}
	targetHost := ""
	if parsed, err := url.Parse(targetURL); err == nil {
		targetHost = parsed.Host
	}
	s := &Server{
		secret:          secret,
		port:            port,
		protocol:        protocol,
		localPort:       localPort,
		targetURL:       targetURL,
		targetHost:      targetHost,
		basicAuth:       opts.BasicAuth,
		accessPolicy:    opts.AccessPolicy,
		rateLimiter:     rateLimiter,
		streamOpenSlots: make(chan struct{}, maxConcurrentStreamOpens),
		rawTCPSlots:     make(chan struct{}, maxConcurrentRawTCPConns),
		upgrader: websocket.Upgrader{
			// The tunnel control/TCP WebSocket endpoints are only ever dialed by
			// the non-browser Sealtun CLI client, which never sets an Origin
			// header. Reject any request that carries one so a browser page
			// cannot be coerced into opening a tunnel WebSocket even if the
			// bearer secret were somehow exposed to client-side JavaScript.
			CheckOrigin: func(r *http.Request) bool {
				return r.Header.Get("Origin") == ""
			},
		},
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = "tunnel-target"
		if s.targetHost != "" {
			req.Host = s.targetHost
		}
	}

	s.reverseProxy = &httputil.ReverseProxy{
		Director:  director,
		Transport: s.reverseProxyTransport(),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			WriteUnavailablePage(w, s.targetLabel(), fmt.Sprintf("The remote ingress is reachable, but the local Sealtun client is not connected to this tunnel yet: %v", err))
		},
	}

	return s
}

func (s *Server) targetLabel() string {
	if strings.TrimSpace(s.targetURL) != "" {
		return s.targetURL
	}
	return s.localPort
}

func (s *Server) reverseProxyTransport() http.RoundTripper {
	return &http.Transport{
		DialContext:           s.proxyDialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func (s *Server) proxyDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	session := s.activeSession
	s.mu.RUnlock()

	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("local client is not connected")
	}

	select {
	case s.streamOpenSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	type openResult struct {
		conn net.Conn
		err  error
	}
	result := make(chan openResult)
	go func() {
		defer func() { <-s.streamOpenSlots }()
		stream, err := session.OpenStream()
		select {
		case result <- openResult{conn: stream, err: err}:
		case <-ctx.Done():
			if stream != nil {
				_ = stream.Close()
			}
		}
	}()

	select {
	case opened := <-result:
		return opened.conn, opened.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/_sealtun/healthz" {
		s.handleHealthz(w, r)
		return
	}
	if r.URL.Path == "/_sealtun/metrics" {
		s.handleMetrics(w, r)
		return
	}
	if r.URL.Path == "/_sealtun/audit" {
		s.handleAudit(w, r)
		return
	}

	// 1. Check if it's the internal tunnel negotiation endpoint
	if r.URL.Path == "/_sealtun/ws" {
		s.handleTunnelConnection(w, r)
		return
	}
	if r.URL.Path == "/_sealtun/tcp" {
		s.handleTCPConnection(w, r)
		return
	}

	// 2. All other requests are public traffic -> Forward to Local Client via Reverse Proxy
	s.handlePublicTraffic(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !requireReadOnlyMethod(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.mu.RLock()
	connected := s.activeSession != nil && !s.activeSession.IsClosed()
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if !connected {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"ok":false,"clientConnected":false,"protocol":%q}`, s.protocol)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"ok":true,"clientConnected":true,"protocol":%q,"connectedAt":%q}`, s.protocol, time.Unix(s.connectedAt.Load(), 0).Format(time.RFC3339))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !requireReadOnlyMethod(w, r) {
		return
	}
	if !s.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.mu.RLock()
	connected := s.activeSession != nil && !s.activeSession.IsClosed()
	s.mu.RUnlock()

	connectedAt := ""
	if value := s.connectedAt.Load(); value > 0 {
		connectedAt = time.Unix(value, 0).Format(time.RFC3339)
	}
	lastRequestAt := ""
	if value := s.lastRequestAt.Load(); value > 0 {
		lastRequestAt = time.Unix(value, 0).Format(time.RFC3339)
	}

	total := s.totalRequests.Load()
	avgDurationMs := int64(0)
	if total > 0 {
		avgDurationMs = s.totalDurationMs.Load() / total
	}
	payload := map[string]interface{}{
		"clientConnected":     connected,
		"connectedAt":         connectedAt,
		"protocol":            s.protocol,
		"localPort":           s.localPort,
		"targetUrl":           s.targetURL,
		"totalRequests":       total,
		"activeRequests":      s.activeRequests.Load(),
		"totalResponseBytes":  s.totalResponseBytes.Load(),
		"total5xx":            s.total5xx.Load(),
		"lastStatus":          s.lastStatus.Load(),
		"lastRequestAt":       lastRequestAt,
		"averageDurationMs":   avgDurationMs,
		"totalDurationMillis": s.totalDurationMs.Load(),
	}
	if tunnelprotocol.UsesRawTCP(s.protocol) {
		lastTCPConnectedAt := ""
		if value := s.lastTCPConnectedAt.Load(); value > 0 {
			lastTCPConnectedAt = time.Unix(value, 0).Format(time.RFC3339)
		}
		payload["totalTCPConnections"] = s.totalTCPConnections.Load()
		payload["activeTCPConnections"] = s.activeTCPConnections.Load()
		payload["totalTCPBytes"] = s.totalTCPBytes.Load()
		payload["totalTCPErrors"] = s.totalTCPErrors.Load()
		payload["lastTCPConnectedAt"] = lastTCPConnectedAt
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if !requireReadOnlyMethod(w, r) {
		return
	}
	if !s.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	since, err := parseAuditSince(r.URL.Query().Get("since"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit, err := parseAuditLimit(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payload := s.auditPayload(since, limit)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func requireReadOnlyMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	return false
}

func (s *Server) handlePublicTraffic(w http.ResponseWriter, r *http.Request) {
	clientIP := accesspolicy.ClientIP(r)
	if ok, reason := accesspolicy.NetworkAllowed(s.accessPolicy, r); !ok {
		s.recordRequestAudit(r, "deny", auditReasonForIPDeny(reason), http.StatusForbidden, clientIP)
		http.Error(w, reason, http.StatusForbidden)
		return
	}
	if s.rateLimiter != nil && !s.rateLimiter.Allow(rateLimitKey(clientIP), time.Now()) {
		s.recordRequestAudit(r, "deny", "rate-limit", http.StatusTooManyRequests, clientIP)
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	authKind, ok := s.authorizedPublicTraffic(r)
	if !ok {
		if s.basicAuth != nil {
			w.Header().Add("WWW-Authenticate", `Basic realm="Sealtun Tunnel", charset="UTF-8"`)
		}
		if accesspolicy.HasTokenAuth(s.accessPolicy) {
			w.Header().Add("WWW-Authenticate", `Bearer realm="Sealtun Tunnel"`)
		}
		s.recordRequestAudit(r, "deny", auditDeniedAuthReason(r), http.StatusUnauthorized, clientIP)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	accesspolicy.StripTemporaryTokenQuery(r.URL)
	if authKind == publicAuthBasic || authKind == publicAuthBearer {
		r.Header.Del("Authorization")
	}

	start := time.Now()
	s.totalRequests.Add(1)
	s.activeRequests.Add(1)
	defer s.activeRequests.Add(-1)

	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	s.reverseProxy.ServeHTTP(recorder, r)

	status := recorder.status
	s.lastStatus.Store(int64(status))
	s.lastRequestAt.Store(time.Now().Unix())
	s.totalResponseBytes.Add(int64(recorder.bytes))
	s.totalDurationMs.Add(time.Since(start).Milliseconds())
	if status >= 500 {
		s.total5xx.Add(1)
	}
	s.recordRequestAudit(r, "allow", authKind.auditReason(), status, clientIP)
	fmt.Printf("request method=%s path=%q status=%d bytes=%d duration=%s\n", r.Method, redactedRequestPath(r), status, recorder.bytes, time.Since(start).Round(time.Millisecond))
}

type publicAuthKind int

const (
	publicAuthNone publicAuthKind = iota
	publicAuthBasic
	publicAuthBearer
	publicAuthTemporary
)

func (k publicAuthKind) auditReason() string {
	switch k {
	case publicAuthBasic:
		return "basic-auth"
	case publicAuthBearer:
		return "bearer"
	case publicAuthTemporary:
		return "temporary-token"
	default:
		return "none"
	}
}

const (
	maxAuditEvents           = 1000
	maxAuditPathBytes        = 2048
	maxConcurrentStreamOpens = 128
	// Raw TCP (SSH/TCP) has no HTTP auth layer, so keep the unauthenticated
	// connection surface bounded: a concurrency cap plus an idle timeout.
	maxConcurrentRawTCPConns = 256
	rawTCPIdleTimeout        = 10 * time.Minute
)

func (s *Server) recordRequestAudit(r *http.Request, decision, reason string, status int, clientIP net.IP) {
	if !accesspolicy.AuditEnabled(s.accessPolicy) {
		return
	}
	event := accesspolicy.AuditEvent{
		Time:     time.Now().UTC().Format(time.RFC3339),
		Decision: decision,
		Reason:   reason,
		Method:   "",
		Path:     "",
		Status:   status,
	}
	if r != nil {
		event.Method = r.Method
		if r.URL != nil {
			event.Path = r.URL.EscapedPath()
			if event.Path == "" {
				event.Path = "/"
			}
			if len(event.Path) > maxAuditPathBytes {
				event.Path = event.Path[:maxAuditPathBytes]
			}
		}
	}
	if clientIP != nil {
		event.ClientIP = clientIP.String()
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if len(s.auditEvents) < maxAuditEvents {
		s.auditEvents = append(s.auditEvents, event)
		return
	}
	s.auditEvents[s.auditStart] = event
	s.auditStart = (s.auditStart + 1) % maxAuditEvents
}

func (s *Server) auditPayload(since time.Duration, limit int) accesspolicy.AuditPayload {
	cutoff := time.Time{}
	if since > 0 {
		cutoff = time.Now().UTC().Add(-since)
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	filtered := make([]accesspolicy.AuditEvent, 0, len(s.auditEvents))
	for i := 0; i < len(s.auditEvents); i++ {
		event := s.auditEvents[(s.auditStart+i)%len(s.auditEvents)]
		if !cutoff.IsZero() {
			eventTime, err := time.Parse(time.RFC3339, event.Time)
			if err != nil || eventTime.Before(cutoff) {
				continue
			}
		}
		filtered = append(filtered, event)
	}
	total := len(filtered)
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return accesspolicy.AuditPayload{Events: filtered, Total: total}
}

func parseAuditSince(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 10 * time.Minute, nil
	}
	since, err := time.ParseDuration(value)
	if err != nil || since < 0 {
		return 0, fmt.Errorf("since must be a non-negative duration")
	}
	return since, nil
}

func parseAuditLimit(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 200, nil
	}
	limit, err := parsePositiveAuditInt(value)
	if err != nil || limit > maxAuditEvents {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxAuditEvents)
	}
	return limit, nil
}

func parsePositiveAuditInt(value string) (int, error) {
	var out int
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("not a number")
		}
		out = out*10 + int(ch-'0')
	}
	if out <= 0 {
		return 0, fmt.Errorf("not positive")
	}
	return out, nil
}

func rateLimitKey(ip net.IP) string {
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}

func auditReasonForIPDeny(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if strings.Contains(reason, "deny") || strings.Contains(reason, "denied") {
		return "ip-denylist"
	}
	if strings.Contains(reason, "allow") || strings.Contains(reason, "not allowed") {
		return "ip-allowlist"
	}
	return "ip-rule"
}

func auditDeniedAuthReason(r *http.Request) string {
	if r == nil {
		return "no-auth"
	}
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authHeader), "basic ") {
			return "basic-auth"
		}
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			return "bearer"
		}
		return "authorization"
	}
	if r.URL != nil && strings.TrimSpace(r.URL.Query().Get(accesspolicy.TemporaryTokenQueryParam)) != "" {
		return "temporary-token"
	}
	return "no-auth"
}

func (s *Server) authorizedPublicTraffic(r *http.Request) (publicAuthKind, bool) {
	requiresBasic := s.basicAuth != nil
	requiresToken := accesspolicy.HasTokenAuth(s.accessPolicy)
	if !requiresBasic && !requiresToken {
		return publicAuthNone, true
	}
	if requiresBasic && s.authorizedBasicAuth(r) {
		return publicAuthBasic, true
	}
	if requiresToken && accesspolicy.BearerTokenAuthorized(s.accessPolicy, r) {
		return publicAuthBearer, true
	}
	if requiresToken && accesspolicy.TokenAuthorized(s.accessPolicy, r, time.Now()) {
		return publicAuthTemporary, true
	}
	return publicAuthNone, false
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(data)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func redactedRequestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if r.URL.RawQuery != "" {
		return path + "?<redacted>"
	}
	return path
}

func (s *Server) handleTunnelConnection(w http.ResponseWriter, r *http.Request) {
	// Authenticate
	if !s.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Upgrade
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("upgrade error: %v\n", err)
		return
	}
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	})
	stopPing := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			case <-stopPing:
				return
			}
		}
	}()
	defer close(stopPing)

	netConn := NewWSConn(conn)

	// Since we OPEN streams to the client, we act as the Yamux Client!
	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.EnableKeepAlive = true
	yamuxConfig.KeepAliveInterval = 10 * time.Second

	session, err := yamux.Client(netConn, yamuxConfig)
	if err != nil {
		fmt.Printf("yamux client setup error: %v\n", err)
		_ = netConn.Close()
		return
	}

	// Replace active session. Close the previous session after a short grace
	// period instead of immediately, so in-flight requests on the old session
	// can drain rather than being abruptly cut to a 502. This also makes
	// reconnection storms less disruptive to active traffic.
	s.mu.Lock()
	old := s.activeSession
	s.activeSession = session
	s.connectedAt.Store(time.Now().Unix())
	s.mu.Unlock()
	if old != nil && !old.IsClosed() {
		go func() {
			time.Sleep(sessionDrainGrace)
			_ = old.Close()
		}()
	}

	fmt.Println("Local client connected successfully to the server pod.")

	// Wait for the session to close before exiting the handler
	<-session.CloseChan()
	fmt.Println("Local client disconnected.")
}

func (s *Server) handleTCPConnection(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.mu.RLock()
	session := s.activeSession
	s.mu.RUnlock()
	if session == nil || session.IsClosed() {
		http.Error(w, "local client is not connected", http.StatusServiceUnavailable)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("tcp upgrade error: %v\n", err)
		return
	}
	defer conn.Close()

	// Dead TCP-over-WebSocket connections (client powered off, network drop
	// without RST) must not leak a goroutine and a yamux stream forever;
	// apply the same keepalive discipline as the tunnel control channel.
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	})
	stopPing := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			case <-stopPing:
				return
			}
		}
	}()
	defer close(stopPing)

	stream, err := session.OpenStream()
	if err != nil {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "local client is not connected"))
		return
	}
	defer stream.Close()

	relayWebSocketAndStream(conn, stream)
}

func relayWebSocketAndStream(conn *websocket.Conn, stream net.Conn) {
	wsConn := NewWSConn(conn)
	defer wsConn.Close()

	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(stream, wsConn)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(wsConn, stream)
		errc <- err
	}()
	<-errc
}

func (s *Server) authorized(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	expectedAuth := fmt.Sprintf("Bearer %s", s.secret)
	return subtle.ConstantTimeCompare([]byte(authHeader), []byte(expectedAuth)) == 1
}

func (s *Server) authorizedBasicAuth(r *http.Request) bool {
	if s.basicAuth == nil {
		return true
	}
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if _, ok := s.basicAuthAuthorizedHeaders.Load(basicAuthHeaderDigest(authHeader)); ok {
			return true
		}
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}
	authorized := publicauth.Check(*s.basicAuth, username, password)
	if authorized && authHeader != "" {
		s.cacheBasicAuthHeader(authHeader)
	}
	return authorized
}

// maxBasicAuthCacheEntries bounds the positive-auth header cache so a client
// cannot exhaust memory by sending many distinct-but-valid Authorization
// headers (Base64 has multiple encodings/padding for the same credentials).
const maxBasicAuthCacheEntries = 1024

// sessionDrainGrace is how long a superseded tunnel session is kept open after
// a new client connects, so in-flight requests can finish instead of being cut.
const sessionDrainGrace = 5 * time.Second

func (s *Server) cacheBasicAuthHeader(authHeader string) {
	if s.basicAuthCacheCount.Load() >= maxBasicAuthCacheEntries {
		// Simple bounded-cache eviction: drop everything and start over once
		// the cap is reached. The cost is at most one extra bcrypt verification
		// per still-active client after a flush, which is acceptable.
		s.basicAuthAuthorizedHeaders.Range(func(key, _ any) bool {
			s.basicAuthAuthorizedHeaders.Delete(key)
			return true
		})
		s.basicAuthCacheCount.Store(0)
	}
	if _, loaded := s.basicAuthAuthorizedHeaders.LoadOrStore(basicAuthHeaderDigest(authHeader), struct{}{}); !loaded {
		s.basicAuthCacheCount.Add(1)
	}
}

func basicAuthHeaderDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("Server listening on %s (H2C enabled)\n", addr)

	httpListener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	listeners := []net.Listener{httpListener}
	var rawListener net.Listener
	if tunnelprotocol.UsesRawTCP(s.protocol) {
		rawListener, err = net.Listen("tcp", fmt.Sprintf(":%d", tunnelprotocol.ServerRawTCPPort))
		if err != nil {
			_ = httpListener.Close()
			return fmt.Errorf("listen raw tcp on :%d: %w", tunnelprotocol.ServerRawTCPPort, err)
		}
		listeners = append(listeners, rawListener)
		fmt.Printf("Raw TCP listener enabled on :%d\n", tunnelprotocol.ServerRawTCPPort)
	}
	defer closeListeners(listeners)

	errc := make(chan error, 2)
	if rawListener != nil {
		go func() {
			errc <- s.serveRawTCP(rawListener)
		}()
	}

	server := newTunnelHTTPServer(s)
	go func() {
		errc <- server.Serve(httpListener)
	}()
	serveCount := 1
	if rawListener != nil {
		serveCount++
	}
	err = <-errc
	closeListeners(listeners)
	drainServeErrors(errc, serveCount-1)
	return err
}

func newTunnelHTTPServer(handler http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Handler:           handler,
		Protocols:         protocols,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func drainServeErrors(errc <-chan error, count int) {
	for i := 0; i < count; i++ {
		<-errc
	}
}

func (s *Server) serveRawTCP(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Temporary failures (fd exhaustion, EMFILE bursts) must not tear
			// down the whole server; back off briefly and keep accepting.
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handleRawTCPConnection(conn)
	}
}

func (s *Server) handleRawTCPConnection(conn net.Conn) {
	defer conn.Close()
	s.totalTCPConnections.Add(1)
	s.lastTCPConnectedAt.Store(time.Now().Unix())

	// Raw TCP has no HTTP auth layer, so unauthenticated internet traffic can
	// hold connections open indefinitely (each pinning a goroutine, an fd, and
	// a yamux stream). Cap concurrency and expire idle connections.
	select {
	case s.rawTCPSlots <- struct{}{}:
		defer func() { <-s.rawTCPSlots }()
	default:
		s.totalTCPErrors.Add(1)
		return
	}
	s.activeTCPConnections.Add(1)
	defer s.activeTCPConnections.Add(-1)
	defer conn.Close()
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	_ = conn.SetDeadline(time.Now().Add(rawTCPIdleTimeout))

	// Raw TCP (SSH/TCP) traffic has no HTTP layer, so token/Basic Auth cannot be
	// enforced here. We can still apply the IP allow/denylist using the real
	// connection peer address, which (unlike forwarded HTTP headers) cannot be
	// spoofed by the client.
	if ok, _ := accesspolicy.NetworkAllowedForIP(s.accessPolicy, rawConnPeerIP(conn)); !ok {
		s.totalTCPErrors.Add(1)
		return
	}

	s.mu.RLock()
	session := s.activeSession
	s.mu.RUnlock()
	if session == nil || session.IsClosed() {
		s.totalTCPErrors.Add(1)
		return
	}

	stream, err := session.OpenStream()
	if err != nil {
		s.totalTCPErrors.Add(1)
		return
	}
	defer stream.Close()

	if err := s.relayRawTCPConns(conn, stream); err != nil && !expectedRelayClose(err) {
		s.totalTCPErrors.Add(1)
	}
}

// rawConnPeerIP extracts the remote peer IP from a raw TCP connection. Returns
// nil when the address cannot be parsed, in which case an IP allowlist (if any)
// will reject the connection (fail closed) and a denylist-only policy will allow
// it (no rule matched).
func rawConnPeerIP(conn net.Conn) net.IP {
	if conn == nil {
		return nil
	}
	addr := conn.RemoteAddr()
	if addr == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func (s *Server) relayRawTCPConns(a, b net.Conn) error {
	return relayBidirectional(a, b, func(n int64) {
		s.totalTCPBytes.Add(n)
	})
}

func expectedRelayClose(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(err.Error(), "use of closed network connection") ||
		strings.Contains(err.Error(), "websocket: close")
}
