package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/publicauth"
	"github.com/labring/sealtun/pkg/session"
)

func TestRunApplyDryRunDoesNotRequireLogin(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runApply(context.Background(), path, true)
	if err != nil {
		t.Fatalf("dry-run apply should not require login: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].TunnelID != "web" || results[0].LocalPort != "3000" || results[0].Status != "planned" {
		t.Fatalf("unexpected dry-run result: %+v", results[0])
	}
	if results[0].Protocol != "https" {
		t.Fatalf("expected dry-run protocol to be reported, got %q", results[0].Protocol)
	}
}

func TestRunApplyDryRunReportsSSHProtocol(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: ssh-dev
    localPort: 22
    protocol: ssh
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runApply(context.Background(), path, true)
	if err != nil {
		t.Fatalf("dry-run apply should accept ssh tunnels: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Protocol != "ssh" {
		t.Fatalf("expected ssh protocol to be reported, got %+v", results[0])
	}
}

func TestRunApplyDryRunReportsTCPProtocol(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: postgres
    localPort: 5432
    protocol: tcp
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runApply(context.Background(), path, true)
	if err != nil {
		t.Fatalf("dry-run apply should accept tcp tunnels: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Protocol != "tcp" {
		t.Fatalf("expected tcp protocol to be reported, got %+v", results[0])
	}
}

func TestBuildApplySessionRecordPersistsCustomDomain(t *testing.T) {
	basicAuth, err := newSessionBasicAuth("admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	record := buildApplySessionRecord(normalizedApplyTunnel{
		TunnelID:  "web",
		LocalPort: "3000",
		Protocol:  "https",
		BasicAuth: basicAuth,
	}, &auth.AuthData{Region: "https://gzg.sealos.run"}, "ns-demo", "kubeconfig", "secret", k8s.TunnelHosts{
		PublicHost:   "app.example.com",
		SealosHost:   "sealtun-web-ns-demo.sealosgzg.site",
		CustomDomain: "app.example.com",
	}, "2026-05-09T00:00:00Z")

	if record.CustomDomain != "app.example.com" {
		t.Fatalf("expected custom domain to be persisted, got %q", record.CustomDomain)
	}
	if record.Host != "app.example.com" || record.SealosHost != "sealtun-web-ns-demo.sealosgzg.site" {
		t.Fatalf("unexpected hosts in session record: %#v", record)
	}
	if record.BasicAuth == nil || !record.BasicAuth.Enabled || record.BasicAuth.Username != "admin" {
		t.Fatalf("expected basic auth to be persisted without plain password, got %#v", record.BasicAuth)
	}
	if record.BasicAuth.PasswordHash == "" || record.BasicAuth.PasswordHash == "secret" {
		t.Fatal("basic auth password must not be persisted in plain text")
	}
}

func TestNormalizeApplyTunnelRejectsUnsafeNames(t *testing.T) {
	t.Parallel()

	invalid := []string{"Web", "-web", "web_", "../web", ""}
	for _, name := range invalid {
		if _, err := normalizeApplyTunnel(applyTunnel{Name: name, LocalPort: 3000}); err == nil {
			t.Fatalf("expected invalid apply tunnel name %q to fail", name)
		}
	}
}

func TestApplyTunnelIDRejectsMeshReservedPrefix(t *testing.T) {
	t.Parallel()

	_, err := applyTunnelID("mesh-global")
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved mesh prefix error, got %v", err)
	}
}

func TestNormalizeApplyTunnelDefaultsProtocol(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeApplyTunnel(applyTunnel{Name: "api", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Protocol != "https" {
		t.Fatalf("expected default https protocol, got %q", normalized.Protocol)
	}
	if normalized.LocalPort != "8080" {
		t.Fatalf("expected port alias to be used, got %q", normalized.LocalPort)
	}
}

func TestNormalizeApplyTunnelAcceptsHTTPUpstreamTarget(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeApplyTunnel(applyTunnel{Name: "api", Target: "http://10.0.0.12:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.LocalPort != "8080" || normalized.TargetURL != "http://10.0.0.12:8080" {
		t.Fatalf("unexpected target normalization: %#v", normalized)
	}
}

func TestNormalizeApplyTunnelAcceptsHTTPSTargetTLSInsecureSkipVerify(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:   "api",
		Target: "https://10.0.0.12:8443",
		TargetTLS: &applyTargetTLS{
			InsecureSkipVerify: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.TargetTLS == nil || !normalized.TargetTLS.InsecureSkipVerify {
		t.Fatalf("expected target TLS option to be normalized: %#v", normalized.TargetTLS)
	}
}

func TestNormalizeApplyTunnelRejectsTargetTLSWithoutHTTPSTarget(t *testing.T) {
	t.Parallel()

	for _, item := range []applyTunnel{
		{Name: "api", LocalPort: 8080, TargetTLS: &applyTargetTLS{InsecureSkipVerify: true}},
		{Name: "api", Target: "http://10.0.0.12:8080", TargetTLS: &applyTargetTLS{InsecureSkipVerify: true}},
	} {
		if _, err := normalizeApplyTunnel(item); err == nil {
			t.Fatalf("expected target TLS config to fail for %#v", item)
		}
	}
}

func TestNormalizeApplyTunnelRejectsMismatchedLocalPortAndTarget(t *testing.T) {
	t.Parallel()

	if _, err := normalizeApplyTunnel(applyTunnel{Name: "api", LocalPort: 3000, Target: "http://10.0.0.12:8080"}); err == nil {
		t.Fatal("expected mismatched localPort and target port to fail")
	}
}

func TestNormalizeApplyTunnelRejectsHTTPOnlyOptionsForSSH(t *testing.T) {
	t.Setenv("SEALTUN_TEST_BEARER", "secret-token")

	tests := []struct {
		name string
		item applyTunnel
	}{
		{
			name: "domain",
			item: applyTunnel{Name: "ssh", LocalPort: 22, Protocol: "ssh", Domain: "dev.example.com"},
		},
		{
			name: "wait domain",
			item: applyTunnel{Name: "ssh", LocalPort: 22, Protocol: "ssh", WaitDomain: true},
		},
		{
			name: "basic auth",
			item: applyTunnel{Name: "ssh", LocalPort: 22, Protocol: "ssh", BasicAuth: &applyBasicAuth{Credential: "admin:secret"}},
		},
		{
			name: "access policy",
			item: applyTunnel{Name: "ssh", LocalPort: 22, Protocol: "ssh", AccessPolicy: &applyAccessPolicy{BearerTokenEnv: "SEALTUN_TEST_BEARER"}},
		},
		{
			name: "target",
			item: applyTunnel{Name: "ssh", Target: "http://10.0.0.12:22", Protocol: "ssh"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeApplyTunnel(tt.item); err == nil {
				t.Fatal("expected ssh tunnel with HTTP-only option to fail")
			}
		})
	}
}

func TestNormalizeApplyTunnelRejectsHTTPOnlyOptionsForTCP(t *testing.T) {
	t.Setenv("SEALTUN_TEST_BEARER", "secret-token")

	tests := []struct {
		name string
		item applyTunnel
	}{
		{
			name: "domain",
			item: applyTunnel{Name: "postgres", LocalPort: 5432, Protocol: "tcp", Domain: "db.example.com"},
		},
		{
			name: "wait domain",
			item: applyTunnel{Name: "postgres", LocalPort: 5432, Protocol: "tcp", WaitDomain: true},
		},
		{
			name: "basic auth",
			item: applyTunnel{Name: "postgres", LocalPort: 5432, Protocol: "tcp", BasicAuth: &applyBasicAuth{Credential: "admin:secret"}},
		},
		{
			name: "access policy",
			item: applyTunnel{Name: "postgres", LocalPort: 5432, Protocol: "tcp", AccessPolicy: &applyAccessPolicy{BearerTokenEnv: "SEALTUN_TEST_BEARER"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeApplyTunnel(tt.item); err == nil {
				t.Fatal("expected tcp tunnel with HTTP-only option to fail")
			}
		})
	}
}

func TestNormalizeApplyTunnelResolvesBasicAuthPasswordEnv(t *testing.T) {
	t.Setenv("SEALTUN_TEST_BASIC_AUTH_PASSWORD", "secret")

	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		BasicAuth: &applyBasicAuth{
			Username:    "admin",
			PasswordEnv: "SEALTUN_TEST_BASIC_AUTH_PASSWORD",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.BasicAuth == nil || !normalized.BasicAuth.Enabled {
		t.Fatal("expected basic auth to be enabled")
	}
	if normalized.BasicAuth.Username != "admin" {
		t.Fatalf("unexpected basic auth username: %q", normalized.BasicAuth.Username)
	}
	if normalized.BasicAuth.PasswordHash == "" || normalized.BasicAuth.PasswordHash == "secret" {
		t.Fatalf("expected hashed password, got %q", normalized.BasicAuth.PasswordHash)
	}
}

func TestNormalizeApplyTunnelResolvesBasicAuthCredential(t *testing.T) {
	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		BasicAuth: &applyBasicAuth{
			Credential: "admin:secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.BasicAuth == nil || !normalized.BasicAuth.Enabled {
		t.Fatal("expected basic auth to be enabled")
	}
	if normalized.BasicAuth.Username != "admin" {
		t.Fatalf("unexpected basic auth username: %q", normalized.BasicAuth.Username)
	}
	if normalized.BasicAuth.PasswordHash == "" || normalized.BasicAuth.PasswordHash == "secret" {
		t.Fatalf("expected hashed password, got %q", normalized.BasicAuth.PasswordHash)
	}
}

func TestNormalizeApplyTunnelResolvesAccessPolicyAndTTL(t *testing.T) {
	t.Setenv("SEALTUN_TEST_BEARER", "secret-token")
	t.Setenv("SEALTUN_TEST_TEMP", "preview-token")

	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		TTL:       "2h",
		AccessPolicy: &applyAccessPolicy{
			BearerTokenEnv: "SEALTUN_TEST_BEARER",
			IPAllowlist:    []string{"10.0.0.0/8"},
			IPDenylist:     []string{"10.0.0.9"},
			RateLimit:      "60/m",
			Audit:          &applyAuditConfig{Enabled: true},
			TemporaryLinks: []applyTemporaryLink{{
				Name:     "review",
				TokenEnv: "SEALTUN_TEST_TEMP",
				TTL:      "1h",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.AccessPolicy == nil {
		t.Fatal("expected access policy")
	}
	if len(normalized.AccessPolicy.BearerTokenHashes) != 1 || strings.Contains(normalized.AccessPolicy.BearerTokenHashes[0], "secret-token") {
		t.Fatalf("expected bearer token hash, got %#v", normalized.AccessPolicy.BearerTokenHashes)
	}
	if len(normalized.AccessPolicy.TemporaryTokens) != 1 || normalized.AccessPolicy.TemporaryTokens[0].Name != "review" {
		t.Fatalf("expected temporary link config, got %#v", normalized.AccessPolicy.TemporaryTokens)
	}
	if normalized.AccessPolicy.RateLimit != "60/m" || normalized.AccessPolicy.Audit == nil || !normalized.AccessPolicy.Audit.Enabled {
		t.Fatalf("expected rate limit and audit config, got %#v", normalized.AccessPolicy)
	}
	if normalized.ExpiresAt == "" {
		t.Fatal("expected ttl to produce expiresAt")
	}
	if _, err := time.Parse(time.RFC3339, normalized.ExpiresAt); err != nil {
		t.Fatalf("expected RFC3339 expiresAt, got %q", normalized.ExpiresAt)
	}
}

func TestNormalizeApplyTunnelResources(t *testing.T) {
	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		Resources: &applyResources{
			Requests: &applyResourceValues{CPU: "25m"},
			Limits:   &applyResourceValues{Memory: "192Mi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Resources == nil || normalized.Resources.Requests == nil || normalized.Resources.Limits == nil {
		t.Fatalf("expected resources to be normalized, got %#v", normalized.Resources)
	}
	if normalized.Resources.Requests.CPU != "25m" || normalized.Resources.Requests.Memory != k8s.DefaultRequestMemory {
		t.Fatalf("unexpected requests: %#v", normalized.Resources.Requests)
	}
	if normalized.Resources.Limits.CPU != k8s.DefaultLimitCPU || normalized.Resources.Limits.Memory != "192Mi" {
		t.Fatalf("unexpected limits: %#v", normalized.Resources.Limits)
	}

	if _, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		Resources: &applyResources{
			Requests: &applyResourceValues{Memory: "512Mi"},
			Limits:   &applyResourceValues{Memory: "128Mi"},
		},
	}); err == nil || !strings.Contains(err.Error(), "resources") {
		t.Fatalf("expected invalid resources error, got %v", err)
	}
}

func TestTemporaryAccessURLEscapesToken(t *testing.T) {
	got := temporaryAccessURL("app.example.com", "token with&symbols?")
	want := "https://app.example.com/?_sealtun_token=token+with%26symbols%3F"
	if got != want {
		t.Fatalf("expected escaped temporary URL %q, got %q", want, got)
	}
}

func TestApplyTemporaryAccessURLsOnlyPrintsInlineTokens(t *testing.T) {
	got := applyTemporaryAccessURLs("app.example.com", &applyAccessPolicy{
		TemporaryLinks: []applyTemporaryLink{
			{Token: "inline-token", TTL: "1h"},
			{TokenEnv: "SEALTUN_TEMP_TOKEN", TTL: "1h"},
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected only inline temporary token URL to be printable, got %#v", got)
	}
	if got[0] != "https://app.example.com/?_sealtun_token=inline-token" {
		t.Fatalf("unexpected temporary URL: %q", got[0])
	}
}

func TestReuseExistingBasicAuthHashKeepsApplyIdempotent(t *testing.T) {
	existing, err := newSessionBasicAuth("admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		BasicAuth: &applyBasicAuth{
			Credential: "admin:secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.BasicAuth == nil || normalized.BasicAuth.PasswordHash == existing.PasswordHash {
		t.Fatal("test setup expected a fresh bcrypt hash before reuse")
	}

	reuseExistingBasicAuthHash(&normalized, existing)

	if normalized.BasicAuth.PasswordHash != existing.PasswordHash {
		t.Fatal("expected matching existing basic auth hash to be reused")
	}
}

func TestReuseExistingBasicAuthHashMigratesLegacySHA256(t *testing.T) {
	existing := &session.BasicAuthConfig{
		Enabled:        true,
		Username:       "admin",
		PasswordSHA256: publicauth.LegacySHA256Hash("secret"),
	}
	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "api",
		LocalPort: 8080,
		BasicAuth: &applyBasicAuth{
			Credential: "admin:secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	freshHash := normalized.BasicAuth.PasswordHash

	reuseExistingBasicAuthHash(&normalized, existing)

	if normalized.BasicAuth.PasswordHash == existing.PasswordSHA256 {
		t.Fatal("legacy SHA-256 hash should not be reused")
	}
	if normalized.BasicAuth.PasswordHash != freshHash {
		t.Fatal("expected fresh bcrypt hash to be kept while migrating legacy SHA-256 session")
	}
	if normalized.BasicAuth.PasswordSHA256 != "" {
		t.Fatal("expected legacy SHA-256 field to be cleared after migration")
	}
}

func TestRunApplyRejectsDuplicateTunnelNames(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
  - name: web
    localPort: 3001
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runApply(context.Background(), path, true); err == nil {
		t.Fatal("expected duplicate tunnel names to be rejected")
	}
}

func TestRunDiffDetectsCreateAndUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Region:    "https://gzg.sealos.run",
		Namespace: "default",
		Protocol:  "https",
		Host:      "web.example.com",
		LocalPort: "3000",
		TargetURL: "http://localhost:3000",
		Secret:    "secret",
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    target: http://10.0.0.12:3001
  - name: api
    localPort: 8080
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two diff results, got %d", len(results))
	}
	if results[0].TunnelID != "api" && results[1].TunnelID != "api" {
		t.Fatalf("expected api create result, got %#v", results)
	}
	foundUpdate := false
	for _, result := range results {
		if result.TunnelID == "web" {
			foundUpdate = result.Action == "update" && changesContain(result.Changes, "target:")
			if result.CurrentHost != "" {
				t.Fatalf("expected currentHost to report custom domain, got public host %q", result.CurrentHost)
			}
		}
	}
	if !foundUpdate {
		t.Fatalf("expected web update diff, got %#v", results)
	}
}

func changesContain(changes []string, needle string) bool {
	for _, change := range changes {
		if strings.Contains(change, needle) {
			return true
		}
	}
	return false
}

func TestRunDiffWithoutSessionDirectoryPlansCreate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "create" {
		t.Fatalf("expected create diff without session dir, got %#v", results)
	}
}

func TestRunDiffReusesUnexpiredTTLSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Region:    "https://gzg.sealos.run",
		Namespace: "default",
		Protocol:  "https",
		Host:      "web.example.com",
		LocalPort: "3000",
		Secret:    "secret",
		TTL:       "2h",
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
    ttl: 2h
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "no-op" {
		t.Fatalf("expected ttl-only reapply to be a no-op while unexpired, got %#v", results)
	}
}

func TestRunDiffDetectsBasicAuthPasswordChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	basicAuth, err := newSessionBasicAuth("admin", "old-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Region:    "https://gzg.sealos.run",
		Namespace: "default",
		Protocol:  "https",
		Host:      "web.example.com",
		LocalPort: "3000",
		Secret:    "secret",
		BasicAuth: basicAuth,
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
    basicAuth:
      credential: admin:new-password
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "update" {
		t.Fatalf("expected basic auth password change to be an update, got %#v", results)
	}
	if !containsString(results[0].Changes, "basicAuth") {
		t.Fatalf("expected basicAuth change, got %#v", results[0].Changes)
	}
}

func TestRunDiffReusesTemporaryLinkTTLSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SEALTUN_TEST_TEMP", "preview-token")
	existingPolicy, err := resolveApplyAccessPolicy(&applyAccessPolicy{
		TemporaryLinks: []applyTemporaryLink{{
			Name:     "review",
			TokenEnv: "SEALTUN_TEST_TEMP",
			TTL:      "1h",
		}},
	}, time.Now().UTC(), getenv)
	if err != nil {
		t.Fatal(err)
	}
	existingPolicy.TemporaryTokens[0].ExpiresAt = time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	if err := session.Save(session.TunnelSession{
		TunnelID:     "web",
		Region:       "https://gzg.sealos.run",
		Namespace:    "default",
		Protocol:     "https",
		Host:         "web.example.com",
		LocalPort:    "3000",
		Secret:       "secret",
		AccessPolicy: existingPolicy,
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
    accessPolicy:
      temporaryLinks:
        - name: review
          tokenEnv: SEALTUN_TEST_TEMP
          ttl: 1h
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := runDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != "no-op" {
		t.Fatalf("expected temporary-link ttl reapply to be a no-op while unexpired, got %#v", results)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestLoadApplyFileRejectsOversizedFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := make([]byte, applyFileMaxBytes+1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadApplyFile(path); err == nil {
		t.Fatal("expected oversized apply file to be rejected")
	}
}

func TestLoadApplyFileRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
    waitDomian: true
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadApplyFile(path); err == nil || !strings.Contains(err.Error(), "field waitDomian not found") {
		t.Fatalf("expected unknown field to be rejected, got %v", err)
	}
}

func TestLoadApplyFileRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sealtun.yaml")
	data := []byte(`version: v1
tunnels:
  - name: web
    localPort: 3000
---
version: v1
tunnels:
  - name: api
    localPort: 3001
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadApplyFile(path); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("expected multiple documents to be rejected, got %v", err)
	}
}

func TestApplyOneTunnelRejectsCorruptExistingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, err := auth.GetSealosDir()
	if err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "web.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = applyOneTunnel(context.Background(), applyTunnel{Name: "web", LocalPort: 3000}, &auth.AuthData{}, nil, "", false)
	if err == nil {
		t.Fatal("expected corrupt existing session to be rejected before provisioning")
	}
}

func TestApplyOneTunnelRejectsExistingSessionFromDifferentRegion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Region:    "https://old.sealos.run",
		Namespace: "default",
		LocalPort: "3000",
		Protocol:  "https",
		Secret:    "secret",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := applyOneTunnel(context.Background(), applyTunnel{Name: "web", LocalPort: 3000}, &auth.AuthData{Region: "https://gzg.sealos.run"}, nil, "", false)
	if err == nil || !strings.Contains(err.Error(), "already belongs to region") {
		t.Fatalf("expected region mismatch to be rejected before provisioning, got %v", err)
	}
}

func TestApplyOneTunnelRejectsExistingSessionWithoutSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:        "web",
		Region:          "https://gzg.sealos.run",
		Namespace:       "default",
		LocalPort:       "3000",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateStopped,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := applyOneTunnel(context.Background(), applyTunnel{Name: "web", LocalPort: 3000}, &auth.AuthData{Region: "https://gzg.sealos.run"}, nil, "", false)
	if err == nil || !strings.Contains(err.Error(), "local secret is unavailable") {
		t.Fatalf("expected missing secret to be rejected before provisioning, got %v", err)
	}
}

func TestApplyOneTunnelRequiresVerifiedCustomDomainBeforeUpdatingExistingTunnel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	previous := session.TunnelSession{
		TunnelID:     "web",
		Region:       "https://gzg.sealos.run",
		Namespace:    "default",
		Protocol:     "https",
		Host:         "old.example.com",
		SealosHost:   "sealtun-web-default.sealosgzg.site",
		CustomDomain: "old.example.com",
		LocalPort:    "3000",
		Secret:       "secret",
		Mode:         "daemon",
	}
	if err := session.Save(previous); err != nil {
		t.Fatal(err)
	}
	originalLookup := lookupCNAME
	lookupCNAME = func(context.Context, string) (string, error) {
		return "wrong.example.com.", nil
	}
	defer func() {
		lookupCNAME = originalLookup
	}()

	_, err := applyOneTunnel(context.Background(), applyTunnel{Name: "web", LocalPort: 3001, Domain: "new.example.com"}, &auth.AuthData{Region: "https://gzg.sealos.run"}, nil, "", false)
	if err == nil || !strings.Contains(err.Error(), "custom domain DNS must be verified before updating an existing tunnel") {
		t.Fatalf("expected DNS verification error before update, got %v", err)
	}
	current, err := session.Get("web")
	if err != nil {
		t.Fatal(err)
	}
	if current.LocalPort != previous.LocalPort || current.CustomDomain != previous.CustomDomain || current.Host != previous.Host {
		t.Fatalf("existing session was modified despite DNS failure: %#v", current)
	}
}

func TestRollbackApplyResultsRestoresExistingLocalSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	previous := session.TunnelSession{
		TunnelID:     "web",
		Region:       "https://gzg.sealos.run",
		Namespace:    "default",
		Protocol:     "https",
		Host:         "old.example.com",
		SealosHost:   "sealtun-web-default.sealosgzg.site",
		CustomDomain: "old.example.com",
		LocalPort:    "3000",
		Secret:       "secret",
		Mode:         "daemon",
	}
	if err := session.Save(previous); err != nil {
		t.Fatal(err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Region:    "https://gzg.sealos.run",
		Namespace: "default",
		Protocol:  "https",
		Host:      "new.example.com",
		LocalPort: "3001",
		Secret:    "secret",
		Mode:      "daemon",
	}); err != nil {
		t.Fatal(err)
	}

	if err := rollbackApplyResults(nil, []applyResult{{
		TunnelID: "web",
		Previous: &session.TunnelSession{
			TunnelID:     previous.TunnelID,
			Region:       previous.Region,
			Namespace:    previous.Namespace,
			Protocol:     previous.Protocol,
			Host:         previous.Host,
			SealosHost:   previous.SealosHost,
			CustomDomain: previous.CustomDomain,
			LocalPort:    previous.LocalPort,
			Secret:       previous.Secret,
			Mode:         previous.Mode,
		},
	}}); err != nil {
		t.Fatalf("rollbackApplyResults returned error: %v", err)
	}

	current, err := session.Get("web")
	if err != nil {
		t.Fatal(err)
	}
	for field, got := range map[string]string{
		"host":         current.Host,
		"sealosHost":   current.SealosHost,
		"customDomain": current.CustomDomain,
		"localPort":    current.LocalPort,
	} {
		want := map[string]string{
			"host":         previous.Host,
			"sealosHost":   previous.SealosHost,
			"customDomain": previous.CustomDomain,
			"localPort":    previous.LocalPort,
		}[field]
		if got != want {
			t.Fatalf("expected restored %s %q, got %q", field, want, got)
		}
	}
}

func TestApplyOneTunnelPropagatesRemoteRefreshFailure(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:  "web",
		Region:    "https://gzg.sealos.run",
		Namespace: "default",
		LocalPort: "3000",
		Protocol:  "https",
		Secret:    "secret",
	}); err != nil {
		t.Fatal(err)
	}

	want := errors.New("remote state unavailable")
	previousCollect := collectSessionRemoteState
	collectSessionRemoteState = func(context.Context, session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return nil, want
	}
	t.Cleanup(func() { collectSessionRemoteState = previousCollect })

	_, err := applyOneTunnel(context.Background(), applyTunnel{Name: "web", LocalPort: 3000}, &auth.AuthData{Region: "https://gzg.sealos.run"}, &k8s.Client{}, "", false)
	if !errors.Is(err, want) {
		t.Fatalf("apply error = %v, want %v", err, want)
	}
}

func TestRollbackApplyResultsReturnsLocalRestoreFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEALTUN_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".sealtun"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := rollbackApplyResults(nil, []applyResult{{
		TunnelID: "web",
		Previous: &session.TunnelSession{
			TunnelID:  "web",
			Region:    "https://gzg.sealos.run",
			Namespace: "default",
			LocalPort: "3000",
			Protocol:  "https",
			Secret:    "secret",
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "restore tunnel web") {
		t.Fatalf("expected rollback failure to be reported, got %v", err)
	}
}
