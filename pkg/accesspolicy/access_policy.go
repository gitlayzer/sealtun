package accesspolicy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const TemporaryTokenQueryParam = "_sealtun_token" // #nosec G101 -- query parameter name, not a credential value.
const minTokenLength = 8

const (
	rateLimiterPruneThreshold = 1024
	rateLimiterPruneInterval  = time.Second
	rateLimiterMaxBuckets     = 65536
	rateLimiterOverflowKey    = "\x00overflow"
)

var ipMatcherCache sync.Map

type Policy struct {
	BearerTokenHashes []string         `json:"bearerTokenHashes,omitempty"`
	IPAllowlist       []string         `json:"ipAllowlist,omitempty"`
	IPDenylist        []string         `json:"ipDenylist,omitempty"`
	TemporaryTokens   []TemporaryToken `json:"temporaryTokens,omitempty"`
	RateLimit         string           `json:"rateLimit,omitempty"`
	Audit             *AuditConfig     `json:"audit,omitempty"`
}

type TemporaryToken struct {
	Name      string `json:"name,omitempty"`
	TokenHash string `json:"tokenHash"`
	TTL       string `json:"ttl,omitempty"`
	ExpiresAt string `json:"expiresAt"`
}

type AuditConfig struct {
	Enabled bool `json:"enabled"`
}

type RateLimitSpec struct {
	Limit  int
	Window time.Duration
	Raw    string
}

type RateLimiter struct {
	mu        sync.Mutex
	spec      RateLimitSpec
	buckets   map[string]rateLimitBucket
	lastPrune time.Time
}

type rateLimitBucket struct {
	WindowStart time.Time
	Count       int
}

type AuditEvent struct {
	Time     string `json:"time"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Status   int    `json:"status,omitempty"`
	ClientIP string `json:"clientIp,omitempty"`
}

type AuditPayload struct {
	Events []AuditEvent `json:"events"`
	Total  int          `json:"total"`
}

func Empty(policy *Policy) bool {
	if policy == nil {
		return true
	}
	return len(policy.BearerTokenHashes) == 0 &&
		len(policy.IPAllowlist) == 0 &&
		len(policy.IPDenylist) == 0 &&
		len(policy.TemporaryTokens) == 0 &&
		strings.TrimSpace(policy.RateLimit) == "" &&
		!AuditEnabled(policy)
}

func HasTokenAuth(policy *Policy) bool {
	return policy != nil && (len(policy.BearerTokenHashes) > 0 || len(policy.TemporaryTokens) > 0)
}

func AuditEnabled(policy *Policy) bool {
	return policy != nil && policy.Audit != nil && policy.Audit.Enabled
}

func HashToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("token is required")
	}
	if len(token) < minTokenLength {
		return "", fmt.Errorf("token must be at least %d characters", minTokenLength)
	}
	return hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	var encoded [sha256.Size * 2]byte
	hex.Encode(encoded[:], sum[:])
	var out strings.Builder
	out.Grow(len("sha256:") + len(encoded))
	out.WriteString("sha256:")
	_, _ = out.Write(encoded[:])
	return out.String()
}

func Validate(policy *Policy) error {
	if policy == nil {
		return nil
	}
	for _, hash := range policy.BearerTokenHashes {
		if err := validateTokenHash(hash); err != nil {
			return fmt.Errorf("bearer token hash: %w", err)
		}
	}
	for _, entry := range policy.IPAllowlist {
		if _, err := parseIPMatcher(entry); err != nil {
			return fmt.Errorf("ip allowlist entry %q: %w", entry, err)
		}
	}
	for _, entry := range policy.IPDenylist {
		if _, err := parseIPMatcher(entry); err != nil {
			return fmt.Errorf("ip denylist entry %q: %w", entry, err)
		}
	}
	for _, token := range policy.TemporaryTokens {
		if err := validateTokenHash(token.TokenHash); err != nil {
			return fmt.Errorf("temporary token: %w", err)
		}
		expiresAt, err := time.Parse(time.RFC3339, token.ExpiresAt)
		if err != nil {
			return fmt.Errorf("temporary token expiresAt: %w", err)
		}
		if expiresAt.IsZero() {
			return fmt.Errorf("temporary token expiresAt is required")
		}
	}
	if strings.TrimSpace(policy.RateLimit) != "" {
		if _, err := ParseRateLimit(policy.RateLimit); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
	}
	return nil
}

func ParseRateLimit(value string) (RateLimitSpec, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return RateLimitSpec{}, fmt.Errorf("value is required")
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return RateLimitSpec{}, fmt.Errorf("must use <count>/<s|m|h>, for example 60/m")
	}
	limit, err := parsePositiveInt(strings.TrimSpace(parts[0]))
	if err != nil {
		return RateLimitSpec{}, fmt.Errorf("count %q must be a positive integer", strings.TrimSpace(parts[0]))
	}
	windowName := strings.ToLower(strings.TrimSpace(parts[1]))
	var window time.Duration
	switch windowName {
	case "s":
		window = time.Second
	case "m":
		window = time.Minute
	case "h":
		window = time.Hour
	default:
		return RateLimitSpec{}, fmt.Errorf("window must be s, m, or h")
	}
	return RateLimitSpec{Limit: limit, Window: window, Raw: fmt.Sprintf("%d/%s", limit, windowName)}, nil
}

func parsePositiveInt(value string) (int, error) {
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return limit, nil
}

func NewRateLimiter(spec RateLimitSpec) *RateLimiter {
	if spec.Limit <= 0 || spec.Window <= 0 {
		return nil
	}
	return &RateLimiter{
		spec:    spec,
		buckets: make(map[string]rateLimitBucket),
	}
}

func (l *RateLimiter) Allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	if now.IsZero() {
		now = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(now)
	bucket, exists := l.buckets[key]
	if !exists && len(l.buckets) >= rateLimiterMaxBuckets-1 {
		// Do not evict active clients: eviction would let an attacker reset its
		// own limit by cycling high-cardinality keys. New clients share one
		// bounded overflow bucket, which fails closed once its limit is reached.
		key = rateLimiterOverflowKey
		bucket = l.buckets[key]
	}
	if bucket.WindowStart.IsZero() || now.Sub(bucket.WindowStart) >= l.spec.Window || now.Before(bucket.WindowStart) {
		bucket = rateLimitBucket{WindowStart: now, Count: 0}
	}
	bucket.Count++
	l.buckets[key] = bucket
	return bucket.Count <= l.spec.Limit
}

func (l *RateLimiter) pruneLocked(now time.Time) {
	if len(l.buckets) <= rateLimiterPruneThreshold {
		return
	}
	if !l.lastPrune.IsZero() && now.Sub(l.lastPrune) < rateLimiterPruneInterval {
		return
	}
	l.lastPrune = now
	for key, bucket := range l.buckets {
		if now.Sub(bucket.WindowStart) >= 2*l.spec.Window {
			delete(l.buckets, key)
		}
	}
}

func NetworkAllowed(policy *Policy, r *http.Request) (bool, string) {
	if policy == nil || (len(policy.IPAllowlist) == 0 && len(policy.IPDenylist) == 0) {
		return true, ""
	}
	return NetworkAllowedForIP(policy, ClientIP(r))
}

// NetworkAllowedForIP applies a policy's IP allowlist/denylist against a
// caller-resolved client IP. It exists so non-HTTP transports (e.g. the raw TCP
// listener) can enforce the same IP rules using the real peer address rather
// than relying on forwardable HTTP headers.
func NetworkAllowedForIP(policy *Policy, ip net.IP) (bool, string) {
	if policy == nil || (len(policy.IPAllowlist) == 0 && len(policy.IPDenylist) == 0) {
		return true, ""
	}
	if ip == nil {
		return false, "client IP could not be determined"
	}
	for _, entry := range policy.IPDenylist {
		matcher, err := cachedIPMatcher(entry)
		if err != nil {
			// Fail closed: a denylist entry we cannot parse must never be
			// treated as "does not match". Reject the request instead of
			// silently letting a potentially-denied client through.
			return false, "client IP denylist entry is invalid"
		}
		if matcher.Contains(ip) {
			return false, "client IP is denied"
		}
	}
	if len(policy.IPAllowlist) == 0 {
		return true, ""
	}
	for _, entry := range policy.IPAllowlist {
		matcher, err := cachedIPMatcher(entry)
		if err != nil {
			// Fail closed: a malformed allowlist entry must not be able to
			// match. Reject so a misconfigured policy cannot accidentally
			// grant access.
			return false, "client IP allowlist entry is invalid"
		}
		if matcher.Contains(ip) {
			return true, ""
		}
	}
	return false, "client IP is not allowed"
}

func TokenAuthorized(policy *Policy, r *http.Request, now time.Time) bool {
	if policy == nil {
		return false
	}
	if BearerTokenAuthorized(policy, r) {
		return true
	}
	queryToken := ""
	if r != nil && r.URL != nil {
		queryToken = r.URL.Query().Get(TemporaryTokenQueryParam)
	}
	if queryToken == "" {
		return false
	}
	queryToken = strings.TrimSpace(queryToken)
	if len(queryToken) < minTokenLength {
		return false
	}
	queryTokenHash := hashToken(queryToken)
	for _, token := range policy.TemporaryTokens {
		expiresAt, err := time.Parse(time.RFC3339, token.ExpiresAt)
		if err != nil || !now.Before(expiresAt) {
			continue
		}
		if tokenHashMatches(queryTokenHash, token.TokenHash) {
			return true
		}
	}
	return false
}

func BearerTokenAuthorized(policy *Policy, r *http.Request) bool {
	if policy == nil {
		return false
	}
	token := bearerTokenFromRequest(r)
	return token != "" && tokenMatchesAny(token, policy.BearerTokenHashes)
}

func StripTemporaryTokenQuery(rawURL *url.URL) {
	if rawURL == nil || rawURL.RawQuery == "" {
		return
	}
	values := rawURL.Query()
	if _, ok := values[TemporaryTokenQueryParam]; !ok {
		return
	}
	values.Del(TemporaryTokenQueryParam)
	rawURL.RawQuery = values.Encode()
}

func ClientIP(r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	peer := peerIP(r)
	// Only trust client-supplied forwarding headers when the immediate peer is
	// a trusted proxy (loopback or private range, where the in-cluster Sealos
	// ingress terminates). A directly-connected public peer can forge these
	// headers to bypass IP allow/denylists, so we ignore them in that case.
	if trustedProxyPeer(peer) {
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			if ip := net.ParseIP(realIP); ip != nil {
				return ip
			}
		}
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			for i := len(parts) - 1; i >= 0; i-- {
				part := parts[i]
				if ip := net.ParseIP(strings.TrimSpace(part)); ip != nil {
					return ip
				}
			}
		}
	}
	return peer
}

func peerIP(r *http.Request) net.IP {
	host := r.RemoteAddr
	if parsedHost, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = parsedHost
	}
	return net.ParseIP(strings.TrimSpace(host))
}

// trustedProxyPeer reports whether the immediate TCP peer is allowed to set
// X-Real-IP / X-Forwarded-For. Loopback and RFC1918/RFC4193 private addresses
// are treated as trusted because the Sealtun server only ever receives public
// traffic through the cluster ingress on a private network.
func trustedProxyPeer(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

type ipMatcher struct {
	ipNet *net.IPNet
	ip    net.IP
}

func (m ipMatcher) Contains(ip net.IP) bool {
	if m.ipNet != nil {
		return m.ipNet.Contains(ip)
	}
	return m.ip != nil && ip != nil && m.ip.Equal(ip)
}

func parseIPMatcher(value string) (ipMatcher, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ipMatcher{}, fmt.Errorf("entry is empty")
	}
	if strings.Contains(value, "/") {
		_, ipNet, err := net.ParseCIDR(value)
		if err != nil {
			return ipMatcher{}, err
		}
		return ipMatcher{ipNet: ipNet}, nil
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return ipMatcher{}, fmt.Errorf("entry must be an IP address or CIDR range")
	}
	return ipMatcher{ip: ip}, nil
}

func cachedIPMatcher(value string) (ipMatcher, error) {
	value = strings.TrimSpace(value)
	if cached, ok := ipMatcherCache.Load(value); ok {
		return cached.(ipMatcher), nil
	}
	matcher, err := parseIPMatcher(value)
	if err != nil {
		return ipMatcher{}, err
	}
	ipMatcherCache.Store(value, matcher)
	return matcher, nil
}

func validateTokenHash(hash string) error {
	hash = strings.TrimSpace(hash)
	if !strings.HasPrefix(hash, "sha256:") {
		return fmt.Errorf("must use sha256:<hex>")
	}
	value := strings.TrimPrefix(hash, "sha256:")
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("invalid sha256 length")
	}
	for _, ch := range value {
		if ch >= 'A' && ch <= 'F' {
			return fmt.Errorf("sha256 hex must be lowercase")
		}
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("invalid sha256 hex")
	}
	return nil
}

func bearerTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(authHeader) <= len(prefix) || !strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(authHeader[len(prefix):])
}

func tokenMatchesAny(token string, hashes []string) bool {
	token = strings.TrimSpace(token)
	if len(token) < minTokenLength {
		return false
	}
	tokenHash := hashToken(token)
	for _, hash := range hashes {
		if tokenHashMatches(tokenHash, hash) {
			return true
		}
	}
	return false
}

func tokenHashMatches(tokenHash, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(tokenHash), []byte(strings.TrimSpace(hash))) == 1
}
