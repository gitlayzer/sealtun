package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
