package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
)

func TestValidateCustomDomain(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty optional", input: "", want: ""},
		{name: "lowercase", input: "Dev.Example.COM.", want: "dev.example.com"},
		{name: "valid hyphen", input: "api-dev.example.com", want: "api-dev.example.com"},
		{name: "url rejected", input: "https://dev.example.com", wantErr: true},
		{name: "path rejected", input: "dev.example.com/path", wantErr: true},
		{name: "port rejected", input: "dev.example.com:443", wantErr: true},
		{name: "ip rejected", input: "192.0.2.10", wantErr: true},
		{name: "single label rejected", input: "localhost", wantErr: true},
		{name: "wildcard rejected", input: "*.example.com", wantErr: true},
		{name: "underscore rejected", input: "dev_api.example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateCustomDomain(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestDomainPayloadFromSessionUsesSealosHostAsCNAME(t *testing.T) {
	payload := domainPayloadFromSession(session.TunnelSession{
		TunnelID:     "abc123",
		Host:         "dev.example.com",
		SealosHost:   "sealtun-abc123-ns.sealosgzg.site",
		CustomDomain: "dev.example.com",
	})

	if payload.PublicHost != "dev.example.com" {
		t.Fatalf("unexpected public host: %s", payload.PublicHost)
	}
	if payload.SealosHost != "sealtun-abc123-ns.sealosgzg.site" {
		t.Fatalf("unexpected sealos host: %s", payload.SealosHost)
	}
	if payload.CNAME != "dev.example.com -> sealtun-abc123-ns.sealosgzg.site" {
		t.Fatalf("unexpected cname hint: %s", payload.CNAME)
	}
}

func TestDomainPayloadUsesLegacyHostAsCNAMETargetWhenSealosHostIsMissing(t *testing.T) {
	payload := domainPayloadFromSession(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "sealtun-abc123-ns.sealosgzg.site",
		CreatedAt: time.Now().Format(time.RFC3339),
	})

	if payload.SealosHost != "sealtun-abc123-ns.sealosgzg.site" {
		t.Fatalf("expected legacy host to remain the sealos target, got %s", payload.SealosHost)
	}
}

func TestDomainPayloadDoesNotInventCNAMETargetForMalformedCustomSession(t *testing.T) {
	payload := domainPayloadFromSession(session.TunnelSession{
		TunnelID:     "abc123",
		Host:         "app.example.com",
		CustomDomain: "app.example.com",
		CreatedAt:    time.Now().Format(time.RFC3339),
	})

	if payload.SealosHost != "" {
		t.Fatalf("expected missing sealos host to stay empty, got %s", payload.SealosHost)
	}
	if payload.CNAME != "" {
		t.Fatalf("expected no cname hint without a sealos host, got %s", payload.CNAME)
	}
}

func TestSessionSealosHostForDomainPrefersLegacyOfficialHost(t *testing.T) {
	got := sessionSealosHostForDomain(session.TunnelSession{
		TunnelID: "abc123",
		Host:     "sealtun-abc123-ns.sealosgzg.site",
	}, "sealtun-abc123-ns.guessed.example")
	if got != "sealtun-abc123-ns.sealosgzg.site" {
		t.Fatalf("expected stored host to win for legacy official session, got %s", got)
	}
}

func TestSessionSealosHostForDomainUsesComputedWhenHostIsCustom(t *testing.T) {
	got := sessionSealosHostForDomain(session.TunnelSession{
		TunnelID:     "abc123",
		Host:         "app.example.com",
		CustomDomain: "app.example.com",
	}, "sealtun-abc123-ns.sealosgzg.site")
	if got != "sealtun-abc123-ns.sealosgzg.site" {
		t.Fatalf("expected computed target when stored host is custom, got %s", got)
	}
}

func TestDomainVerifyResultErrorFailsWhenNotReady(t *testing.T) {
	err := domainVerifyResultError(&domainVerifyPayload{
		CustomDomain: "app.example.com",
		Ready:        false,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("expected not-ready error, got %v", err)
	}
}

func TestDomainVerifyResultErrorAllowsReadyPayload(t *testing.T) {
	if err := domainVerifyResultError(&domainVerifyPayload{
		CustomDomain: "app.example.com",
		Ready:        true,
	}, nil); err != nil {
		t.Fatalf("expected ready payload to pass, got %v", err)
	}
}

func TestVerifySessionDomainRequiresCustomDomain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "sealtun-abc123-ns.sealosgzg.site",
		Namespace: "ns-demo",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	_, err := verifySessionDomain(context.Background(), "abc123")
	if err == nil || !strings.Contains(err.Error(), "no custom domain") {
		t.Fatalf("expected no custom domain error, got %v", err)
	}
}

func TestConfigureSessionCustomDomainRejectsSSH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "sshdev",
		Protocol:  "ssh",
		Host:      "sealtun-sshdev-ns.sealosgzg.site",
		Namespace: "ns-demo",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	_, err := configureSessionCustomDomain(context.Background(), "sshdev", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "only supported for https") {
		t.Fatalf("expected ssh custom domain rejection, got %v", err)
	}
}

func TestDomainMutationsWaitForTunnelOperationLock(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, string) error
	}{
		{
			name: "configure",
			mutate: func(ctx context.Context, tunnelID string) error {
				_, err := configureSessionCustomDomain(ctx, tunnelID, "dev.example.com")
				return err
			},
		},
		{
			name: "clear",
			mutate: func(ctx context.Context, tunnelID string) error {
				_, err := clearSessionCustomDomain(ctx, tunnelID)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			tunnelID := "domain" + tt.name
			if err := session.Save(session.TunnelSession{
				TunnelID:     tunnelID,
				Protocol:     "https",
				Host:         "sealtun-" + tunnelID + "-ns.sealosgzg.site",
				SealosHost:   "sealtun-" + tunnelID + "-ns.sealosgzg.site",
				CustomDomain: "old.example.com",
				Namespace:    "ns-demo",
				CreatedAt:    time.Now().Format(time.RFC3339),
			}); err != nil {
				t.Fatalf("save session: %v", err)
			}

			releaseLock := holdTunnelOperationLock(t, tunnelID)
			defer releaseLock()
			previousCollect := collectSessionRemoteState
			remoteCalled := make(chan struct{}, 1)
			want := errors.New("remote refresh after lock")
			collectSessionRemoteState = func(context.Context, session.TunnelSession) (*k8s.TunnelRemoteState, error) {
				remoteCalled <- struct{}{}
				return nil, want
			}
			t.Cleanup(func() { collectSessionRemoteState = previousCollect })

			done := make(chan error, 1)
			go func() {
				done <- tt.mutate(context.Background(), tunnelID)
			}()
			assertOperationBlocked(t, remoteCalled)
			releaseLock()
			if err := <-done; !errors.Is(err, want) {
				t.Fatalf("mutation error = %v, want %v", err, want)
			}
		})
	}
}

func TestPlanSessionCustomDomainRejectsSSHBeforeK8sClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "sshdev",
		Protocol:  "ssh",
		Host:      "sealtun-sshdev-ns.sealosgzg.site",
		Namespace: "ns-demo",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	_, err := planSessionCustomDomain("sshdev", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "only supported for https") {
		t.Fatalf("expected ssh custom domain rejection, got %v", err)
	}
}

func TestPlanSessionCustomDomainUsesStoredSealosHostWithoutLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:   "webdev",
		Protocol:   "https",
		Host:       "sealtun-webdev-ns.sealosgzg.site",
		SealosHost: "sealtun-webdev-ns.sealosgzg.site",
		Namespace:  "ns-demo",
		CreatedAt:  time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := planSessionCustomDomain("webdev", "dev.example.com")
	if err != nil {
		t.Fatalf("planSessionCustomDomain returned error: %v", err)
	}
	if payload.CNAME != "dev.example.com -> sealtun-webdev-ns.sealosgzg.site" {
		t.Fatalf("unexpected cname: %s", payload.CNAME)
	}
}

func TestDomainPlanRejectsEmptyDomain(t *testing.T) {
	_, err := validateCustomDomain("")
	if err != nil {
		t.Fatalf("empty domain should normalize without validation error: %v", err)
	}
	cmd := *domainPlanCmd
	err = cmd.RunE(&cmd, []string{"abc123", ""})
	if err == nil || !strings.Contains(err.Error(), "custom domain is required") {
		t.Fatalf("expected custom domain required error, got %v", err)
	}
}

func TestSessionSupportsCustomDomainAllowsLegacyHTTPS(t *testing.T) {
	if !sessionSupportsCustomDomain(session.TunnelSession{}) {
		t.Fatal("legacy sessions without protocol should be treated as HTTPS")
	}
	if !sessionSupportsCustomDomain(session.TunnelSession{Protocol: "https"}) {
		t.Fatal("https sessions should support custom domains")
	}
	if sessionSupportsCustomDomain(session.TunnelSession{Protocol: "ssh"}) {
		t.Fatal("ssh sessions should not support custom domains")
	}
	if sessionSupportsCustomDomain(session.TunnelSession{Protocol: "tcp"}) {
		t.Fatal("tcp sessions should not support custom domains")
	}
}

func TestVerifySessionDomainRejectsTCPAndSSH(t *testing.T) {
	for _, protocol := range []string{"ssh", "tcp"} {
		t.Run(protocol, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := session.Save(session.TunnelSession{
				TunnelID:     "custom-" + protocol,
				Protocol:     protocol,
				CustomDomain: "app.example.com",
				Host:         "app.example.com",
				SealosHost:   "sealtun-custom-ns.sealosgzg.site",
				Namespace:    "ns-demo",
				CreatedAt:    time.Now().Format(time.RFC3339),
			}); err != nil {
				t.Fatalf("save session: %v", err)
			}

			if _, err := verifySessionDomain(context.Background(), "custom-"+protocol); err == nil || !strings.Contains(err.Error(), "https tunnels") {
				t.Fatalf("expected protocol rejection, got %v", err)
			}
			if _, err := waitForSessionDomain(context.Background(), "custom-"+protocol, time.Millisecond); err == nil || !strings.Contains(err.Error(), "https tunnels") {
				t.Fatalf("expected wait protocol rejection, got %v", err)
			}
		})
	}
}

func TestCollectDomainStatusFiltersCustomDomains(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:     "abc123",
		Host:         "app.example.com",
		SealosHost:   "sealtun-abc123-ns.sealosgzg.site",
		CustomDomain: "app.example.com",
		Namespace:    "ns-demo",
		CreatedAt:    time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save custom domain session: %v", err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "def456",
		Host:      "sealtun-def456-ns.sealosgzg.site",
		Namespace: "ns-demo",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save plain session: %v", err)
	}

	payload, err := collectDomainStatusWithVerifier(context.Background(), "", time.Second, func(ctx context.Context, sess session.TunnelSession) *domainVerifyPayload {
		return &domainVerifyPayload{
			TunnelID:          sess.TunnelID,
			PublicHost:        sess.Host,
			SealosHost:        sess.SealosHost,
			CustomDomain:      sess.CustomDomain,
			DNSReady:          true,
			IngressReady:      true,
			CertificateExists: true,
			CertificateReady:  true,
			Ready:             true,
		}
	})
	if err != nil {
		t.Fatalf("collectDomainStatusWithVerifier returned error: %v", err)
	}
	if payload.TotalSessions != 2 || payload.CustomDomains != 1 || payload.Ready != 1 || payload.NotReady != 0 {
		t.Fatalf("unexpected status summary: %#v", payload)
	}
	if len(payload.Items) != 1 || payload.Items[0].TunnelID != "abc123" {
		t.Fatalf("unexpected status items: %#v", payload.Items)
	}
}

func TestCollectDomainStatusRequiresCustomDomainForExplicitTunnel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "sealtun-abc123-ns.sealosgzg.site",
		Namespace: "ns-demo",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	_, err := collectDomainStatusWithVerifier(context.Background(), "abc123", time.Second, verifyDomainForSession)
	if err == nil || !strings.Contains(err.Error(), "no custom domain") {
		t.Fatalf("expected no custom domain error, got %v", err)
	}
}

func TestCollectDomainStatusRefreshesExplicitTunnelBeforeDomainCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "sealtun-abc123-ns.sealosgzg.site",
		Namespace: "ns-demo",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(context.Context, session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return &k8s.TunnelRemoteState{
			DeploymentOK: true,
			Protocol:     "https",
			SealosHost:   "sealtun-abc123-ns.sealosgzg.site",
			CustomDomain: "api.example.com",
		}, nil
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	payload, err := collectDomainStatusWithVerifier(context.Background(), "abc123", time.Second, func(ctx context.Context, sess session.TunnelSession) *domainVerifyPayload {
		return &domainVerifyPayload{TunnelID: sess.TunnelID, CustomDomain: sess.CustomDomain, Ready: true}
	})
	if err != nil {
		t.Fatalf("collectDomainStatusWithVerifier returned error: %v", err)
	}
	if payload.CustomDomains != 1 || len(payload.Items) != 1 || payload.Items[0].CustomDomain != "api.example.com" {
		t.Fatalf("explicit tunnel did not use refreshed custom domain: %#v", payload)
	}
}

func TestCollectDomainStatusReportsNoCustomDomains(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	payload, err := collectDomainStatusWithVerifier(context.Background(), "", time.Second, verifyDomainForSession)
	if err != nil {
		t.Fatalf("collectDomainStatusWithVerifier returned error: %v", err)
	}
	if payload.CustomDomains != 0 || len(payload.Warnings) != 1 || !strings.Contains(payload.Warnings[0], "no custom domains") {
		t.Fatalf("unexpected no-domain payload: %#v", payload)
	}
}

func TestCollectDomainStatusRequiresPositiveTimeout(t *testing.T) {
	_, err := collectDomainStatusWithVerifier(context.Background(), "", 0, verifyDomainForSession)
	if err == nil || !strings.Contains(err.Error(), "timeout must be greater than 0") {
		t.Fatalf("expected timeout validation error, got %v", err)
	}
}

func TestWaitForDomainReadyRequiresPositiveTimeout(t *testing.T) {
	_, err := waitForDomainReady(context.Background(), session.TunnelSession{
		TunnelID:     "abc123",
		Host:         "dev.example.com",
		SealosHost:   "sealtun-abc123-ns.sealosgzg.site",
		CustomDomain: "dev.example.com",
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "timeout must be greater than 0") {
		t.Fatalf("expected timeout validation error, got %v", err)
	}
}

func TestDomainListContainsNormalizesDNSNames(t *testing.T) {
	if !domainListContains([]string{"Dev.Example.com."}, "dev.example.com") {
		t.Fatal("expected normalized DNS name match")
	}
	if domainListContains([]string{"old.example.com"}, "dev.example.com") {
		t.Fatal("expected different DNS name not to match")
	}
}

func TestPrintDomainStatusPayloadHidesCertificateSecretInText(t *testing.T) {
	payload := &domainStatusPayload{
		TotalSessions: 1,
		CustomDomains: 1,
		Ready:         1,
		Items: []domainStatusItem{{
			TunnelID:          "webdev",
			CustomDomain:      "app.example.com",
			PublicHost:        "app.example.com",
			SealosHost:        "sealtun-webdev-ns.sealosgzg.site",
			CertificateExists: true,
			CertificateReady:  true,
			CertificateSecret: "sealtun-webdev-custom-tls",
			Ready:             true,
		}},
	}

	var output bytes.Buffer
	domainJSON = false
	domainStatusCmd.SetOut(&output)
	t.Cleanup(func() { domainStatusCmd.SetOut(nil) })

	if err := printDomainStatusPayload(domainStatusCmd, payload, true); err != nil {
		t.Fatalf("printDomainStatusPayload returned error: %v", err)
	}
	text := output.String()
	if strings.Contains(text, "secret=") || strings.Contains(text, "sealtun-webdev-custom-tls") {
		t.Fatalf("domain status text output should not expose certificate secret names, got:\n%s", text)
	}
}

func TestPrintDomainVerifyPayloadHidesCertificateSecretInText(t *testing.T) {
	payload := &domainVerifyPayload{
		TunnelID:          "webdev",
		CustomDomain:      "app.example.com",
		SealosHost:        "sealtun-webdev-ns.sealosgzg.site",
		CertificateExists: true,
		CertificateReady:  true,
		CertificateSecret: "sealtun-webdev-custom-tls",
		Ready:             true,
	}

	var output bytes.Buffer
	domainJSON = false
	domainVerifyCmd.SetOut(&output)
	t.Cleanup(func() { domainVerifyCmd.SetOut(nil) })

	if err := printDomainVerifyPayload(domainVerifyCmd, payload); err != nil {
		t.Fatalf("printDomainVerifyPayload returned error: %v", err)
	}
	text := output.String()
	if strings.Contains(text, "secret=") || strings.Contains(text, "sealtun-webdev-custom-tls") {
		t.Fatalf("domain verify text output should not expose certificate secret names, got:\n%s", text)
	}
}

func TestRequireDomainCNAMERequiresVerifiedTarget(t *testing.T) {
	originalLookup := lookupCNAME
	defer func() { lookupCNAME = originalLookup }()

	lookupCNAME = func(ctx context.Context, host string) (string, error) {
		if host != "app.example.com" {
			t.Fatalf("unexpected lookup host: %s", host)
		}
		return "sealtun-abc123-ns.sealosgzg.site.", nil
	}
	if err := requireDomainCNAME(context.Background(), "app.example.com", "sealtun-abc123-ns.sealosgzg.site"); err != nil {
		t.Fatalf("expected verified CNAME to pass, got %v", err)
	}

	lookupCNAME = func(ctx context.Context, host string) (string, error) {
		return "", errors.New("no such host")
	}
	err := requireDomainCNAME(context.Background(), "app.example.com", "sealtun-abc123-ns.sealosgzg.site")
	if err == nil || !strings.Contains(err.Error(), "custom domain DNS is not verified") {
		t.Fatalf("expected DNS verification error, got %v", err)
	}
}

func TestVerifyDomainForSessionDoesNotUseCustomHostAsCNAMEFallback(t *testing.T) {
	payload := verifyDomainForSession(context.Background(), session.TunnelSession{
		TunnelID:     "abc123",
		Host:         "app.example.com",
		CustomDomain: "app.example.com",
	})
	if payload.SealosHost != "" {
		t.Fatalf("expected missing Sealos host to stay empty, got %s", payload.SealosHost)
	}
	if payload.DNSReady {
		t.Fatal("expected DNS readiness to remain false without a Sealos CNAME target")
	}
	if !domainWarningsContain(payload.Warnings, "Sealos CNAME target is missing from the local session; reconfigure the domain or recreate the tunnel") {
		t.Fatalf("expected missing target warning, got %#v", payload.Warnings)
	}
}

func domainWarningsContain(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}
