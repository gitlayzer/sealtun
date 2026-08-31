package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
)

func TestPolicyShowPayloadRedactsSecrets(t *testing.T) {
	hash, err := accesspolicy.HashToken("access-token")
	if err != nil {
		t.Fatal(err)
	}
	tempHash, err := accesspolicy.HashToken("preview-token")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := policyShowPayloadFromSession(session.TunnelSession{
		TunnelID: "web",
		Protocol: "https",
		AccessPolicy: &session.AccessPolicy{
			BearerTokenHashes: []string{hash},
			IPAllowlist:       []string{"10.0.0.0/8"},
			RateLimit:         "60/m",
			Audit:             &session.AuditConfig{Enabled: true},
			TemporaryTokens: []session.TemporaryToken{{
				Name:      "review",
				TokenHash: tempHash,
				ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
			}},
		},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leaked := range []string{"access-token", "preview-token", hash, tempHash} {
		if strings.Contains(text, leaked) {
			t.Fatalf("policy payload leaked %q: %s", leaked, text)
		}
	}
	if payload.BearerTokens != 1 || payload.RateLimit != "60/m" || !payload.AuditEnabled || len(payload.TemporaryLinks) != 1 {
		t.Fatalf("unexpected policy payload: %#v", payload)
	}
}

func TestPolicyShowRejectsNonHTTPS(t *testing.T) {
	if _, err := policyShowPayloadFromSession(session.TunnelSession{TunnelID: "ssh", Protocol: "ssh"}, time.Now()); err == nil {
		t.Fatal("expected non-https tunnel policy show to fail")
	}
}

func TestPolicyShowTreatsMissingProtocolAsHTTPS(t *testing.T) {
	payload, err := policyShowPayloadFromSession(session.TunnelSession{
		TunnelID: "legacy",
		AccessPolicy: &session.AccessPolicy{
			RateLimit: "60/m",
			Audit:     &session.AuditConfig{Enabled: true},
		},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if payload.RateLimit != "60/m" || !payload.AuditEnabled {
		t.Fatalf("unexpected legacy policy payload: %#v", payload)
	}
}

func TestApplyPolicySettingsCanClearRateLimit(t *testing.T) {
	got, err := applyPolicySettings(&session.AccessPolicy{
		RateLimit: "60/m",
		Audit:     &session.AuditConfig{Enabled: true},
	}, "", true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.RateLimit != "" || got.Audit == nil || !got.Audit.Enabled {
		t.Fatalf("expected rate limit cleared and audit preserved, got %#v", got)
	}
}

func TestApplyPolicySettingsRejectsRateLimitAndClearTogether(t *testing.T) {
	if _, err := applyPolicySettings(nil, "60/m", true, false, false); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected conflicting rate limit settings to fail, got %v", err)
	}
}

func TestPolicySetCommandRejectsRateLimitAndClearTogether(t *testing.T) {
	oldRateLimit, oldClear := policySetRateLimit, policySetClearRateLimit
	oldAudit, oldNoAudit := policySetAudit, policySetNoAudit
	t.Cleanup(func() {
		policySetRateLimit = oldRateLimit
		policySetClearRateLimit = oldClear
		policySetAudit = oldAudit
		policySetNoAudit = oldNoAudit
	})
	policySetRateLimit = "60/m"
	policySetClearRateLimit = true
	policySetAudit = false
	policySetNoAudit = false
	if err := policySetCmd.RunE(policySetCmd, []string{"web"}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected conflicting rate limit flags to fail, got %v", err)
	}
}

func TestPolicyAuditValidation(t *testing.T) {
	if _, err := collectPolicyAudit(context.Background(), "web", -time.Second, 200); err == nil {
		t.Fatal("expected negative since to fail")
	}
	if _, err := collectPolicyAudit(context.Background(), "web", time.Minute, 0); err == nil {
		t.Fatal("expected invalid limit to fail")
	}
}

func TestShowPolicyRefreshesRemoteState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return &k8s.TunnelRemoteState{
			Protocol: "https",
			AccessPolicy: &accesspolicy.Policy{
				RateLimit: "60/m",
				Audit:     &accesspolicy.AuditConfig{Enabled: true},
			},
			DeploymentOK: true,
			AuthSecretOK: true,
		}, nil
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Protocol:  "https",
		LocalPort: "3000",
		Region:    "https://gzg.sealos.run",
		Namespace: "ns-demo",
	}); err != nil {
		t.Fatal(err)
	}

	payload, err := showPolicy("web", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if payload.RateLimit != "60/m" || !payload.AuditEnabled {
		t.Fatalf("expected refreshed remote policy, got %#v", payload)
	}
}

func TestFetchServerAuditRetriesTransientEndpointFailures(t *testing.T) {
	t.Parallel()

	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if req.Header.Get("Authorization") != "Bearer secret" {
				t.Fatalf("expected bearer secret, got %q", req.Header.Get("Authorization"))
			}
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Status:     "503 Service Unavailable",
					Body:       io.NopCloser(strings.NewReader("starting")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"events":[{"decision":"allow","reason":"basic-auth"}],"total":1}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	payload, err := fetchServerAuditURLWithRetry(context.Background(), client, "https://example.test/_sealtun/audit", "secret", time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}
	if payload.Total != 1 || len(payload.Events) != 1 || payload.Events[0].Reason != "basic-auth" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestFetchServerAuditDoesNotRetryAuthFailures(t *testing.T) {
	t.Parallel()

	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Status:     "401 Unauthorized",
				Body:       io.NopCloser(strings.NewReader("unauthorized")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	_, err := fetchServerAuditURLWithRetry(context.Background(), client, "https://example.test/_sealtun/audit", "wrong", time.Second, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected no retry for auth failure, got %d attempts", attempts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPrintPolicyAudit(t *testing.T) {
	var out bytes.Buffer
	cmd := *policyAuditCmd
	cmd.SetOut(&out)
	printPolicyAudit(&cmd, &policyAuditPayload{
		TunnelID: "web",
		Total:    1,
		Events: []accesspolicy.AuditEvent{{
			Time:     "2026-06-09T10:00:00Z",
			Decision: "deny",
			Reason:   "rate-limit",
			Method:   "GET",
			Path:     "/app",
			Status:   429,
			ClientIP: "203.0.113.7",
		}},
	})
	text := out.String()
	if !strings.Contains(text, "rate-limit") || !strings.Contains(text, "/app") {
		t.Fatalf("unexpected audit output: %s", text)
	}
}
