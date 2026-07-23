package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/publicauth"
)

func TestServerUnavailablePageWhenClientDisconnected(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 status, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Sealtun Tunnel Status") {
		t.Fatal("missing fallback page shell")
	}
	if !strings.Contains(body, "http://localhost:3000") {
		t.Fatal("fallback page should include expected local port")
	}
}

func TestServerRewritesHostForHTTPUpstreamTarget(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions("secret", 8080, "https", "8080", ServerOptions{
		TargetURL: "http://api.internal:8080",
	})
	req := httptest.NewRequest(http.MethodGet, "https://public.example/app", nil)

	server.reverseProxy.Director(req)

	if req.Host != "api.internal:8080" {
		t.Fatalf("expected upstream host rewrite, got %q", req.Host)
	}
	if req.URL.Host != "tunnel-target" {
		t.Fatalf("unexpected proxy target host: %s", req.URL.Host)
	}
}

func TestServerHealthzReflectsDisconnectedClient(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 status, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"clientConnected":false`) {
		t.Fatalf("unexpected health body: %s", body)
	}
	if !strings.Contains(body, `"protocol":"https"`) {
		t.Fatalf("health body should include protocol: %s", body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
}

func TestServerHealthzRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodPost, "https://example.test/_sealtun/healthz", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 status, got %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("expected Allow GET, HEAD header, got %q", got)
	}
}

func TestServerMetricsCountsPublicTraffic(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	metricsReq := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/metrics", nil)
	metricsReq.Header.Set("Authorization", "Bearer secret")
	metricsRec := httptest.NewRecorder()
	server.ServeHTTP(metricsRec, metricsReq)

	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", metricsRec.Code)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(metricsRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["totalRequests"].(float64) != 1 {
		t.Fatalf("expected one counted public request, got %v", payload["totalRequests"])
	}
	if payload["total5xx"].(float64) != 1 {
		t.Fatalf("expected one 5xx request, got %v", payload["total5xx"])
	}
	if payload["lastStatus"].(float64) != http.StatusBadGateway {
		t.Fatalf("expected last status 502, got %v", payload["lastStatus"])
	}
}

func TestServerMetricsIncludesRawTCPCounters(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "tcp", "5432")
	server.totalTCPConnections.Store(2)
	server.activeTCPConnections.Store(1)
	server.totalTCPBytes.Store(128)
	server.totalTCPErrors.Store(1)
	server.lastTCPConnectedAt.Store(1779098400)

	metricsReq := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/metrics", nil)
	metricsReq.Header.Set("Authorization", "Bearer secret")
	metricsRec := httptest.NewRecorder()
	server.ServeHTTP(metricsRec, metricsReq)

	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", metricsRec.Code)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(metricsRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["totalTCPConnections"].(float64) != 2 {
		t.Fatalf("expected tcp connection counter, got %v", payload["totalTCPConnections"])
	}
	if payload["activeTCPConnections"].(float64) != 1 {
		t.Fatalf("expected active tcp connection counter, got %v", payload["activeTCPConnections"])
	}
	if payload["totalTCPBytes"].(float64) != 128 {
		t.Fatalf("expected tcp bytes counter, got %v", payload["totalTCPBytes"])
	}
	if payload["totalTCPErrors"].(float64) != 1 {
		t.Fatalf("expected tcp errors counter, got %v", payload["totalTCPErrors"])
	}
	if payload["lastTCPConnectedAt"] == "" {
		t.Fatalf("expected last tcp connected timestamp, got %#v", payload)
	}
}

func TestExpectedRelayClose(t *testing.T) {
	t.Parallel()

	if !expectedRelayClose(io.EOF) {
		t.Fatal("io.EOF should be treated as a normal relay close")
	}
	if !expectedRelayClose(net.ErrClosed) {
		t.Fatal("net.ErrClosed should be treated as a normal relay close")
	}
	if !expectedRelayClose(assertErr("use of closed network connection")) {
		t.Fatal("closed network connection should be treated as a normal relay close")
	}
	if expectedRelayClose(assertErr("permission denied")) {
		t.Fatal("unexpected relay errors should not be treated as normal closes")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func TestServerMetricsRequiresAuthorization(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/metrics", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 status, got %d", rec.Code)
	}
}

func TestServerMetricsRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodPost, "https://example.test/_sealtun/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 status, got %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("expected Allow GET, HEAD header, got %q", got)
	}
}

func TestServerAuditRequiresAuthorizationAndReadOnlyMethod(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "3000")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/audit", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 status, got %d", rec.Code)
	}

	postReq := httptest.NewRequest(http.MethodPost, "https://example.test/_sealtun/audit", nil)
	postReq.Header.Set("Authorization", "Bearer secret")
	postRec := httptest.NewRecorder()
	server.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 status, got %d", postRec.Code)
	}
}

func TestServerAuditRecordsReasonsWithoutSecrets(t *testing.T) {
	t.Parallel()

	hash, err := accesspolicy.HashToken("access-token")
	if err != nil {
		t.Fatal(err)
	}
	tempHash, err := accesspolicy.HashToken("preview-token")
	if err != nil {
		t.Fatal(err)
	}
	basicAuth, err := publicauth.NewBasicAuth("admin", "secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{
		BasicAuth: basicAuth,
		AccessPolicy: &accesspolicy.Policy{
			BearerTokenHashes: []string{hash},
			TemporaryTokens: []accesspolicy.TemporaryToken{{
				TokenHash: tempHash,
				ExpiresAt: "2099-01-01T00:00:00Z",
			}},
			Audit: &accesspolicy.AuditConfig{Enabled: true},
		},
	})

	missingReq := httptest.NewRequest(http.MethodGet, "https://example.test/app?token=secret", nil)
	server.ServeHTTP(httptest.NewRecorder(), missingReq)
	basicReq := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	basicReq.SetBasicAuth("admin", "secret-pass")
	server.ServeHTTP(httptest.NewRecorder(), basicReq)
	bearerReq := httptest.NewRequest(http.MethodGet, "https://example.test/api", nil)
	bearerReq.Header.Set("Authorization", "Bearer access-token")
	server.ServeHTTP(httptest.NewRecorder(), bearerReq)
	tempReq := httptest.NewRequest(http.MethodGet, "https://example.test/share?_sealtun_token=preview-token", nil)
	server.ServeHTTP(httptest.NewRecorder(), tempReq)

	auditReq := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/audit?since=1h", nil)
	auditReq.Header.Set("Authorization", "Bearer tunnel-secret")
	auditRec := httptest.NewRecorder()
	server.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("expected audit status 200, got %d: %s", auditRec.Code, auditRec.Body.String())
	}
	body := auditRec.Body.String()
	for _, leaked := range []string{"access-token", "preview-token", "secret-pass", "Authorization", "token=secret", "_sealtun_token"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("audit response leaked %q: %s", leaked, body)
		}
	}
	for _, want := range []string{`"reason":"no-auth"`, `"reason":"basic-auth"`, `"reason":"bearer"`, `"reason":"temporary-token"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("audit response missing %s: %s", want, body)
		}
	}
}

func TestServerRateLimitDeniesAndAudits(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{
			RateLimit: "1/m",
			Audit:     &accesspolicy.AuditConfig{Enabled: true},
		},
	})
	req1 := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	req1.RemoteAddr = "203.0.113.7:1234"
	server.ServeHTTP(httptest.NewRecorder(), req1)
	req2 := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	req2.RemoteAddr = "203.0.113.7:1234"
	rec2 := httptest.NewRecorder()
	server.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 status, got %d", rec2.Code)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/audit", nil)
	auditReq.Header.Set("Authorization", "Bearer tunnel-secret")
	auditRec := httptest.NewRecorder()
	server.ServeHTTP(auditRec, auditReq)
	if !strings.Contains(auditRec.Body.String(), `"reason":"rate-limit"`) {
		t.Fatalf("expected rate-limit audit reason, got %s", auditRec.Body.String())
	}
}

func TestServerAuditRecordsIPRuleReason(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{
			IPDenylist: []string{"203.0.113.7"},
			Audit:      &accesspolicy.AuditConfig{Enabled: true},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	server.ServeHTTP(httptest.NewRecorder(), req)

	auditReq := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/audit", nil)
	auditReq.Header.Set("Authorization", "Bearer tunnel-secret")
	auditRec := httptest.NewRecorder()
	server.ServeHTTP(auditRec, auditReq)
	if !strings.Contains(auditRec.Body.String(), `"reason":"ip-denylist"`) {
		t.Fatalf("expected ip-denylist audit reason, got %s", auditRec.Body.String())
	}
}

func TestServerAuditRetainsNewestEventsInOrder(t *testing.T) {
	server := NewServerWithOptions("secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{Audit: &accesspolicy.AuditConfig{Enabled: true}},
	})
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	for i := 0; i < maxAuditEvents+3; i++ {
		server.recordRequestAudit(request, "allow", fmt.Sprintf("event-%d", i), http.StatusOK, nil)
	}

	payload := server.auditPayload(0, maxAuditEvents)
	if len(payload.Events) != maxAuditEvents {
		t.Fatalf("expected %d retained events, got %d", maxAuditEvents, len(payload.Events))
	}
	if payload.Events[0].Reason != "event-3" {
		t.Fatalf("expected oldest retained event to be event-3, got %q", payload.Events[0].Reason)
	}
	if payload.Events[len(payload.Events)-1].Reason != fmt.Sprintf("event-%d", maxAuditEvents+2) {
		t.Fatalf("unexpected newest retained event: %q", payload.Events[len(payload.Events)-1].Reason)
	}
}

func BenchmarkServerRecordRequestAuditFullBuffer(b *testing.B) {
	server := NewServerWithOptions("secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{Audit: &accesspolicy.AuditConfig{Enabled: true}},
	})
	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	for i := 0; i < maxAuditEvents; i++ {
		server.recordRequestAudit(request, "allow", "none", http.StatusOK, nil)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		server.recordRequestAudit(request, "allow", "none", http.StatusOK, nil)
	}
}

func TestTunnelHTTPServerAcceptsHTTP1AndUnencryptedHTTP2(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s", r.Proto)
	})
	server := newTunnelHTTPServer(handler)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-serveDone
	})

	url := "http://" + listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	http1Req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	http1Resp, err := http.DefaultClient.Do(http1Req)
	if err != nil {
		t.Fatalf("HTTP/1 request failed: %v", err)
	}
	_ = http1Resp.Body.Close()
	if http1Resp.ProtoMajor != 1 {
		t.Fatalf("HTTP/1 response protocol = %s", http1Resp.Proto)
	}

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols}
	defer transport.CloseIdleConnections()
	http2Req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	http2Resp, err := (&http.Client{Transport: transport}).Do(http2Req)
	if err != nil {
		t.Fatalf("unencrypted HTTP/2 request failed: %v", err)
	}
	_ = http2Resp.Body.Close()
	if http2Resp.ProtoMajor != 2 {
		t.Fatalf("unencrypted HTTP/2 response protocol = %s", http2Resp.Proto)
	}
}

func TestServerTCPEndpointRequiresAuthorization(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "22")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/tcp", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 status, got %d", rec.Code)
	}
}

func TestServerTCPEndpointRequiresConnectedClient(t *testing.T) {
	t.Parallel()

	server := NewServer("secret", 8080, "https", "22")
	req := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/tcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 status, got %d", rec.Code)
	}
}

func TestServerBasicAuthProtectsPublicTrafficOnly(t *testing.T) {
	t.Parallel()

	basicAuth, err := publicauth.NewBasicAuth("admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{BasicAuth: basicAuth})

	publicReq := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	publicRec := httptest.NewRecorder()
	server.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected public traffic to require basic auth, got %d", publicRec.Code)
	}
	if got := publicRec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Basic") {
		t.Fatalf("expected Basic challenge header, got %q", got)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "https://example.test/_sealtun/healthz", nil)
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health endpoint should not require basic auth, got %d", healthRec.Code)
	}
}

func TestServerBasicAuthAcceptsMatchingCredentials(t *testing.T) {
	t.Parallel()

	basicAuth, err := publicauth.NewBasicAuth("admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{BasicAuth: basicAuth})
	req := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected authenticated request to reach proxy path, got %d", rec.Code)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("basic auth header should be consumed before proxying, got %q", got)
	}
}

func TestServerBearerTokenProtectsPublicTraffic(t *testing.T) {
	t.Parallel()

	hash, err := accesspolicy.HashToken("access-token")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{BearerTokenHashes: []string{hash}},
	})

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "https://example.test/app", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing bearer token to be rejected, got %d", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	req.Header.Set("Authorization", "Bearer access-token")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected bearer-authenticated request to reach proxy, got %d", rec.Code)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("bearer auth header should be consumed before proxying, got %q", got)
	}
}

func TestServerIPAllowlistAndDenylist(t *testing.T) {
	t.Parallel()

	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{
			IPAllowlist: []string{"10.0.0.0/8"},
			IPDenylist:  []string{"10.0.0.5"},
		},
	})

	// Simulate the in-cluster ingress as the immediate (trusted, private) peer
	// so that X-Forwarded-For is honored.
	const trustedPeer = "10.244.0.1:54321"

	deniedReq := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	deniedReq.RemoteAddr = trustedPeer
	deniedReq.Header.Set("X-Forwarded-For", "10.0.0.5")
	deniedRec := httptest.NewRecorder()
	server.ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("expected denied IP to be rejected, got %d", deniedRec.Code)
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	allowedReq.RemoteAddr = trustedPeer
	allowedReq.Header.Set("X-Forwarded-For", "10.0.0.6")
	allowedRec := httptest.NewRecorder()
	server.ServeHTTP(allowedRec, allowedReq)
	if allowedRec.Code != http.StatusBadGateway {
		t.Fatalf("expected allowed IP to reach proxy path, got %d", allowedRec.Code)
	}

	// An untrusted (public) peer must not be able to spoof an allowlisted IP.
	spoofReq := httptest.NewRequest(http.MethodGet, "https://example.test/app", nil)
	spoofReq.RemoteAddr = "203.0.113.7:40000"
	spoofReq.Header.Set("X-Forwarded-For", "10.0.0.6")
	spoofRec := httptest.NewRecorder()
	server.ServeHTTP(spoofRec, spoofReq)
	if spoofRec.Code != http.StatusForbidden {
		t.Fatalf("expected spoofed forwarded header from untrusted peer to be rejected, got %d", spoofRec.Code)
	}
}

func TestServerTemporaryAccessTokenIsStrippedBeforeProxy(t *testing.T) {
	t.Parallel()

	hash, err := accesspolicy.HashToken("preview-token")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("tunnel-secret", 8080, "https", "3000", ServerOptions{
		AccessPolicy: &accesspolicy.Policy{TemporaryTokens: []accesspolicy.TemporaryToken{{
			TokenHash: hash,
			ExpiresAt: "2099-01-01T00:00:00Z",
		}}},
	})
	req := httptest.NewRequest(http.MethodGet, "https://example.test/app?_sealtun_token=preview-token&a=1", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected temporary token request to reach proxy path, got %d", rec.Code)
	}
	if got := req.URL.Query().Get(accesspolicy.TemporaryTokenQueryParam); got != "" {
		t.Fatalf("temporary token query should be stripped, got %q", got)
	}
	if got := req.URL.Query().Get("a"); got != "1" {
		t.Fatalf("unrelated query should remain, got %q", got)
	}
}

func TestRedactedRequestPathDropsQueryValues(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "https://example.test/callback?token=secret", nil)

	got := redactedRequestPath(req)
	if got != "/callback?<redacted>" {
		t.Fatalf("expected redacted path, got %q", got)
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("redacted path leaked query value: %q", got)
	}
}

func TestStatusRecorderPreservesFirstStatus(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	status := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}

	status.WriteHeader(http.StatusCreated)
	status.WriteHeader(http.StatusInternalServerError)

	if status.status != http.StatusCreated {
		t.Fatalf("expected first status to be preserved, got %d", status.status)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected underlying recorder status 201, got %d", rec.Code)
	}
}

type fakeAddr struct{ addr string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.addr }

type fakeConn struct {
	net.Conn
	remote string
}

func (c fakeConn) RemoteAddr() net.Addr { return fakeAddr{addr: c.remote} }

func TestRawConnPeerIPParsesRemoteAddr(t *testing.T) {
	t.Parallel()

	ip := rawConnPeerIP(fakeConn{remote: "203.0.113.7:5555"})
	if ip == nil || ip.String() != "203.0.113.7" {
		t.Fatalf("expected 203.0.113.7, got %v", ip)
	}
	if got := rawConnPeerIP(nil); got != nil {
		t.Fatalf("expected nil for nil conn, got %v", got)
	}
}

func TestRawTCPPolicyEnforcesIPRulesByRealPeer(t *testing.T) {
	t.Parallel()

	policy := &accesspolicy.Policy{IPDenylist: []string{"203.0.113.7"}}

	if ok, _ := accesspolicy.NetworkAllowedForIP(policy, rawConnPeerIP(fakeConn{remote: "203.0.113.7:1000"})); ok {
		t.Fatal("expected denied peer to be rejected on the raw TCP path")
	}
	if ok, _ := accesspolicy.NetworkAllowedForIP(policy, rawConnPeerIP(fakeConn{remote: "198.51.100.4:1000"})); !ok {
		t.Fatal("expected non-denied peer to be allowed on the raw TCP path")
	}
}
