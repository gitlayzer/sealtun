package accesspolicy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func BenchmarkTokenMatchesAnyManyHashes(b *testing.B) {
	hashes := make([]string, 64)
	for i := range hashes {
		hash, err := HashToken(fmt.Sprintf("stored-token-%02d", i))
		if err != nil {
			b.Fatal(err)
		}
		hashes[i] = hash
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if tokenMatchesAny("request-token", hashes) {
			b.Fatal("unexpected token match")
		}
	}
}

func TestNetworkAllowedUsesDenyBeforeAllow(t *testing.T) {
	policy := &Policy{
		IPAllowlist: []string{"10.0.0.0/8"},
		IPDenylist:  []string{"10.0.0.9"},
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.9, 192.0.2.1")

	ok, reason := NetworkAllowed(policy, req)
	if ok {
		t.Fatal("expected denied IP to be rejected")
	}
	if reason == "" {
		t.Fatal("expected rejection reason")
	}
}

func TestClientIPPrefersRealIPHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "10.0.0.9")
	req.Header.Set("X-Real-IP", "10.0.0.6")

	if got := ClientIP(req).String(); got != "10.0.0.6" {
		t.Fatalf("expected X-Real-IP to win, got %s", got)
	}
}

func TestClientIPUsesProxyForwardedForFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.200, 10.0.0.9")

	if got := ClientIP(req).String(); got != "10.0.0.9" {
		t.Fatalf("expected proxy-reported X-Forwarded-For IP to win, got %s", got)
	}
}

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.RemoteAddr = "203.0.113.5:443"
	req.Header.Set("X-Real-IP", "10.0.0.6")
	req.Header.Set("X-Forwarded-For", "10.0.0.9")

	if got := ClientIP(req).String(); got != "203.0.113.5" {
		t.Fatalf("expected spoofed headers to be ignored and peer IP used, got %s", got)
	}
}

func TestNetworkAllowedRejectsSpoofedAllowlistHeaderFromPublicPeer(t *testing.T) {
	policy := &Policy{IPAllowlist: []string{"10.0.0.0/8"}}
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.RemoteAddr = "203.0.113.5:443"
	req.Header.Set("X-Real-IP", "10.0.0.6")

	if ok, _ := NetworkAllowed(policy, req); ok {
		t.Fatal("expected spoofed allowlist header from public peer to be rejected")
	}
}

func TestTokenAuthorizedAcceptsBearerTokenHash(t *testing.T) {
	hash, err := HashToken("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	policy := &Policy{BearerTokenHashes: []string{hash}}
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")

	if !TokenAuthorized(policy, req, time.Now()) {
		t.Fatal("expected bearer token to authorize")
	}
}

func TestTokenAuthorizedAcceptsUnexpiredTemporaryQueryToken(t *testing.T) {
	hash, err := HashToken("preview-token")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	policy := &Policy{TemporaryTokens: []TemporaryToken{{
		TokenHash: hash,
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
	}}}
	req := httptest.NewRequest(http.MethodGet, "https://example.test/?_sealtun_token=preview-token", nil)

	if !TokenAuthorized(policy, req, now) {
		t.Fatal("expected unexpired temporary token to authorize")
	}
	if TokenAuthorized(policy, req, now.Add(2*time.Hour)) {
		t.Fatal("expected expired temporary token to be rejected")
	}
}

func TestHashTokenRejectsShortTokens(t *testing.T) {
	if _, err := HashToken("short"); err == nil {
		t.Fatal("expected short token to be rejected")
	}
}

func TestParseRateLimit(t *testing.T) {
	t.Parallel()

	spec, err := ParseRateLimit("60/m")
	if err != nil {
		t.Fatalf("ParseRateLimit returned error: %v", err)
	}
	if spec.Limit != 60 || spec.Window != time.Minute || spec.Raw != "60/m" {
		t.Fatalf("unexpected spec: %#v", spec)
	}
	for _, value := range []string{"", "0/m", "60/day", "abc/m", "60"} {
		if _, err := ParseRateLimit(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestRateLimiterAllowsWithinWindowAndResets(t *testing.T) {
	t.Parallel()

	limiter := NewRateLimiter(RateLimitSpec{Limit: 2, Window: time.Minute})
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	if !limiter.Allow("203.0.113.1", now) {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow("203.0.113.1", now.Add(10*time.Second)) {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow("203.0.113.1", now.Add(20*time.Second)) {
		t.Fatal("third request in same window should be denied")
	}
	if !limiter.Allow("203.0.113.1", now.Add(time.Minute)) {
		t.Fatal("request in next window should be allowed")
	}
	if !limiter.Allow("198.51.100.1", now.Add(20*time.Second)) {
		t.Fatal("separate client key should have its own bucket")
	}
}

func TestRateLimiterBoundsUniqueClientBuckets(t *testing.T) {
	limiter := NewRateLimiter(RateLimitSpec{Limit: 2, Window: time.Hour})
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	for i := 0; i < rateLimiterMaxBuckets+100; i++ {
		limiter.Allow(fmt.Sprintf("client-%d", i), now)
	}
	if got := len(limiter.buckets); got > rateLimiterMaxBuckets {
		t.Fatalf("rate limiter retained %d buckets, max is %d", got, rateLimiterMaxBuckets)
	}
	if limiter.Allow("another-new-client", now) {
		t.Fatal("overflow clients should share a fail-closed bucket after its limit is exhausted")
	}
}

func TestValidateAcceptsRateLimitAndAuditOnlyPolicy(t *testing.T) {
	t.Parallel()

	policy := &Policy{RateLimit: "1000/h", Audit: &AuditConfig{Enabled: true}}
	if err := Validate(policy); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if Empty(policy) {
		t.Fatal("rate limit and audit should make policy non-empty")
	}
}

func TestStripTemporaryTokenQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.test/path?_sealtun_token=secret&a=1", nil)

	StripTemporaryTokenQuery(req.URL)

	if got := req.URL.Query().Get(TemporaryTokenQueryParam); got != "" {
		t.Fatalf("expected temporary token query to be stripped, got %q", got)
	}
	if got := req.URL.Query().Get("a"); got != "1" {
		t.Fatalf("expected unrelated query value to remain, got %q", got)
	}
}
