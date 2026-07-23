package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

func TestBuildUpPlanUsesExplicitPortAndInheritedExposeFlags(t *testing.T) {
	resetUpTestGlobals(t)
	protocol = tunnelprotocol.HTTPS
	basicAuthUser = "admin"
	basicAuthPasswordEnv = "SEALTUN_TEST_PASSWORD"
	accessRateLimit = "60/m"
	accessAuditEnabled = true

	cmd := upCmd
	plan, err := buildUpPlan(cmd, []string{"3000"}, upOptions{}, true)
	if err != nil {
		t.Fatalf("build up plan: %v", err)
	}
	if plan.Source != "explicit" {
		t.Fatalf("expected explicit source, got %q", plan.Source)
	}
	if plan.Protocol != tunnelprotocol.HTTPS {
		t.Fatalf("expected https protocol, got %q", plan.Protocol)
	}
	if plan.LocalPort != "3000" || plan.TargetURL != "http://localhost:3000" {
		t.Fatalf("unexpected target: port=%s url=%s", plan.LocalPort, plan.TargetURL)
	}
	if basicAuthUser != "admin" || basicAuthPasswordEnv != "SEALTUN_TEST_PASSWORD" || accessRateLimit != "60/m" || !accessAuditEnabled {
		t.Fatal("up should preserve expose auth and access policy flag globals for runExpose")
	}
}

func TestBuildUpPlanTargetUsesHTTPSProtocol(t *testing.T) {
	resetUpTestGlobals(t)
	protocol = tunnelprotocol.HTTPS
	exposeTarget = "https://192.168.0.201:8006"
	targetTLSInsecureSkipVerify = true

	plan, err := buildUpPlan(upCmd, nil, upOptions{}, true)
	if err != nil {
		t.Fatalf("build up target plan: %v", err)
	}
	if plan.LocalPort != "8006" || plan.TargetURL != "https://192.168.0.201:8006" {
		t.Fatalf("unexpected target plan: %#v", plan)
	}
	if !targetTLSInsecureSkipVerify {
		t.Fatal("up should preserve target insecure flag for runExpose")
	}
}

func TestValidateUpPlanRejectsTargetWithTCPProtocol(t *testing.T) {
	resetUpTestGlobals(t)
	plan := &upPlan{
		Protocol:  tunnelprotocol.TCP,
		LocalPort: "8080",
		TargetURL: "http://127.0.0.1:8080",
	}
	err := validateUpPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "--target is only supported for https tunnels") {
		t.Fatalf("expected target protocol rejection, got %v", err)
	}
}

func TestValidateUpPlanRejectsTargetInsecureWithoutTarget(t *testing.T) {
	resetUpTestGlobals(t)
	targetTLSInsecureSkipVerify = true
	plan := &upPlan{
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "8080",
		TargetURL: "",
	}
	err := validateUpPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "--target-insecure-skip-verify requires --target") {
		t.Fatalf("expected target tls rejection, got %v", err)
	}
}

func TestValidateUpPlanRejectsMissingBasicAuthEnv(t *testing.T) {
	resetUpTestGlobals(t)
	basicAuthUser = "admin"
	basicAuthPasswordEnv = "MISSING_SEALTUN_BASIC_AUTH_PASSWORD"
	t.Setenv("MISSING_SEALTUN_BASIC_AUTH_PASSWORD", "")

	plan := &upPlan{
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "8080",
		TargetURL: "http://localhost:8080",
	}
	err := validateUpPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "environment variable MISSING_SEALTUN_BASIC_AUTH_PASSWORD is empty or unset") {
		t.Fatalf("expected missing basic auth env rejection, got %v", err)
	}
}

func TestValidateUpPlanRejectsInvalidRateLimit(t *testing.T) {
	resetUpTestGlobals(t)
	accessRateLimit = "nope"

	plan := &upPlan{
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "8080",
		TargetURL: "http://localhost:8080",
	}
	err := validateUpPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected invalid rate limit rejection, got %v", err)
	}
}

func TestBuildUpPlanTemplateSelectsMatchingDiscoveredPort(t *testing.T) {
	resetUpTestGlobals(t)
	protocol = tunnelprotocol.HTTPS
	previous := upPortDiscoverer
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{
		{Port: 3000, ProcessName: "node"},
		{Port: 5432, ProcessName: "postgres"},
	}}
	t.Cleanup(func() { upPortDiscoverer = previous })

	plan, err := buildUpPlan(upCmd, nil, upOptions{Template: "postgres", Yes: true}, true)
	if err != nil {
		t.Fatalf("build template plan: %v", err)
	}
	if plan.LocalPort != "5432" || plan.Protocol != tunnelprotocol.TCP || plan.Template != "postgres" {
		t.Fatalf("unexpected template plan: %#v", plan)
	}
}

func TestBuildUpPlanNonInteractiveDiscoveryRequiresExplicitSelection(t *testing.T) {
	resetUpTestGlobals(t)
	protocol = tunnelprotocol.HTTPS
	previous := upPortDiscoverer
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{{Port: 3000, ProcessName: "node"}}}
	t.Cleanup(func() { upPortDiscoverer = previous })

	cmd := *upCmd
	cmd.SetIn(strings.NewReader(""))
	_, err := buildUpPlan(&cmd, nil, upOptions{}, false)
	if err == nil || !strings.Contains(err.Error(), "non-interactive up requires an explicit port or --yes") {
		t.Fatalf("expected non-interactive discovery error, got %v", err)
	}
}

func TestBuildUpPlanYesAllowsSingleDiscoveredPort(t *testing.T) {
	resetUpTestGlobals(t)
	protocol = tunnelprotocol.HTTPS
	previous := upPortDiscoverer
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{{Port: 3000, ProcessName: "node"}}}
	t.Cleanup(func() { upPortDiscoverer = previous })

	cmd := *upCmd
	cmd.SetIn(strings.NewReader(""))
	plan, err := buildUpPlan(&cmd, nil, upOptions{Yes: true}, false)
	if err != nil {
		t.Fatalf("build yes discovery plan: %v", err)
	}
	if plan.LocalPort != "3000" || plan.Source != "discovered" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestRunUpReusesProjectStateBeforeDiscovery(t *testing.T) {
	resetUpTestGlobals(t)
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := saveProjectTunnelState(projectTunnelState{
		TunnelID:  "abc123",
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "3000",
		PublicURL: "https://example.sealos.site",
		Namespace: "ns-test",
	}); err != nil {
		t.Fatalf("save project state: %v", err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "3000",
		Host:      "example.sealos.site",
		Namespace: "ns-test",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	cmd := *upCmd
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runUp(&cmd, nil, upOptions{}); err != nil {
		t.Fatalf("run up: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Tunnel ID: abc123") || !strings.Contains(text, "https://example.sealos.site") {
		t.Fatalf("unexpected state output: %s", text)
	}
}

func TestRunUpGuidedBypassesExistingProjectState(t *testing.T) {
	resetUpTestGlobals(t)
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := saveProjectTunnelState(projectTunnelState{
		TunnelID:  "abc123",
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "9999",
		PublicURL: "https://existing.example",
		Namespace: "ns-test",
	}); err != nil {
		t.Fatalf("save project state: %v", err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "9999",
		Host:      "existing.example",
		Namespace: "ns-test",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{{Port: 3000, ProcessName: "node"}}}
	upStatusCollector = loggedInUpStatus
	upCommandInteractive = func(*cobra.Command) bool { return true }

	cmd := *upCmd
	cmd.SetIn(strings.NewReader("\n\n\nn\nn\nn\nn\ny\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runUp(&cmd, nil, upOptions{Guided: true, JSON: true}); err != nil {
		t.Fatalf("run guided up json: %v\noutput:\n%s", err, out.String())
	}
	text := out.String()
	if !strings.Contains(text, `"source": "guided"`) || !strings.Contains(text, `"localPort": "3000"`) {
		t.Fatalf("expected guided plan instead of reused state, got:\n%s", text)
	}
	if strings.Contains(text, "abc123") || strings.Contains(text, "existing.example") {
		t.Fatalf("guided up should bypass existing project state, got:\n%s", text)
	}
}

func TestRunUpIgnoresStaleProjectState(t *testing.T) {
	resetUpTestGlobals(t)
	t.Chdir(t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if err := saveProjectTunnelState(projectTunnelState{
		TunnelID:  "missing",
		Protocol:  tunnelprotocol.HTTPS,
		LocalPort: "3000",
		PublicURL: "https://old.example",
	}); err != nil {
		t.Fatalf("save project state: %v", err)
	}
	previous := upPortDiscoverer
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{{Port: 3001, ProcessName: "node"}}}
	t.Cleanup(func() { upPortDiscoverer = previous })

	cmd := *upCmd
	cmd.SetIn(strings.NewReader(""))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runUp(&cmd, nil, upOptions{Yes: true, JSON: true}); err != nil {
		t.Fatalf("run up fallback plan: %v", err)
	}
	if !strings.Contains(out.String(), `"localPort": "3001"`) {
		t.Fatalf("expected stale state fallback to discovered port, got %s", out.String())
	}
	if _, err := loadProjectTunnelState(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale project state to be removed, got %v", err)
	}
}

func TestRunUpJSONPrintsPlanWithoutCreatingTunnel(t *testing.T) {
	resetUpTestGlobals(t)
	protocol = tunnelprotocol.HTTPS
	called := false
	previousRunExpose := runExpose
	runExpose = func(cmd *cobra.Command, args []string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { runExpose = previousRunExpose })

	cmd := *upCmd
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runUp(&cmd, []string{"3000"}, upOptions{JSON: true}); err != nil {
		t.Fatalf("run up json: %v", err)
	}
	if called {
		t.Fatal("json mode should only print the up plan")
	}
	if !strings.Contains(out.String(), `"localPort": "3000"`) {
		t.Fatalf("unexpected json output: %s", out.String())
	}
}

func TestBuildGuidedUpPlanFullHTTPSOptionsAndConfig(t *testing.T) {
	resetUpTestGlobals(t)
	t.Chdir(t.TempDir())
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{{Port: 3000, ProcessName: "node"}}}
	upStatusCollector = loggedInUpStatus
	upCommandInteractive = func(*cobra.Command) bool { return true }
	upConfigFileName = "sealtun.yaml"

	cmd := *upCmd
	cmd.SetIn(strings.NewReader("\n\n\n" +
		"y\n\n\n" +
		"y\n\n\n" +
		"y\napp.example.com\n\n" +
		"y\n" +
		"y\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)

	plan, err := buildUpPlan(&cmd, nil, upOptions{Guided: true}, false)
	if err != nil {
		t.Fatalf("build guided plan: %v\noutput:\n%s", err, out.String())
	}
	if plan.Source != "guided" || plan.LocalPort != "3000" || plan.Protocol != tunnelprotocol.HTTPS {
		t.Fatalf("unexpected guided plan: %#v", plan)
	}
	if !plan.SaveConfig || plan.Name != "web" {
		t.Fatalf("expected default web name and saved config, got name=%q save=%v", plan.Name, plan.SaveConfig)
	}
	if basicAuthUser != "admin" || basicAuthPasswordEnv != "SEALTUN_BASIC_AUTH_PASSWORD" {
		t.Fatalf("expected env-backed basic auth, got user=%q env=%q", basicAuthUser, basicAuthPasswordEnv)
	}
	if accessRateLimit != "60/m" || !accessAuditEnabled {
		t.Fatalf("expected rate limit and audit, got rate=%q audit=%v", accessRateLimit, accessAuditEnabled)
	}
	if customDomain != "app.example.com" || waitDomain {
		t.Fatalf("unexpected domain settings: domain=%q wait=%v", customDomain, waitDomain)
	}
	if err := writeUpPlanConfigIfRequested(plan); err != nil {
		t.Fatalf("write guided config: %v", err)
	}
	data, err := os.ReadFile("sealtun.yaml")
	if err != nil {
		t.Fatalf("read guided config: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"name: web",
		"localPort: 3000",
		"basicAuth:",
		"username: admin",
		"passwordEnv: SEALTUN_BASIC_AUTH_PASSWORD",
		"rateLimit: 60/m",
		"enabled: true",
		"domain: app.example.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected generated config to contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "password:") {
		t.Fatalf("generated config must not write plaintext password:\n%s", text)
	}
}

func TestWriteUpPlanConfigRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside.yaml")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "sealtun.yaml")
	if err := os.Symlink(target, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := writeUpPlanConfigIfRequested(&upPlan{
		SaveConfig: true,
		ConfigPath: linked,
		Protocol:   tunnelprotocol.HTTPS,
		LocalPort:  "3000",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked config to be rejected, got %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "original" {
		t.Fatalf("outside target changed: data=%q err=%v", data, readErr)
	}
}

func TestProjectStateRejectsSymlinkedDirectoryAndFile(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		cwd := t.TempDir()
		t.Chdir(cwd)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(cwd, projectStateDirName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		err := saveProjectTunnelState(projectTunnelState{TunnelID: "abc123"})
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlinked state directory rejection, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(outside, projectStateFileName)); !os.IsNotExist(statErr) {
			t.Fatalf("state escaped project directory, stat err=%v", statErr)
		}
	})

	t.Run("file", func(t *testing.T) {
		cwd := t.TempDir()
		t.Chdir(cwd)
		stateDir := filepath.Join(cwd, projectStateDirName)
		if err := os.Mkdir(stateDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(cwd, "outside-state.json")
		if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(stateDir, projectStateFileName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		err := saveProjectTunnelState(projectTunnelState{TunnelID: "abc123"})
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlinked state file rejection, got %v", err)
		}
		data, readErr := os.ReadFile(target)
		if readErr != nil || string(data) != "original" {
			t.Fatalf("outside target changed: data=%q err=%v", data, readErr)
		}
	})
}

func TestBuildGuidedUpPlanCanSkipOptionalHTTPSOptions(t *testing.T) {
	resetUpTestGlobals(t)
	upPortDiscoverer = fakePortDiscoverer{items: []discoverItem{{Port: 3000, ProcessName: "node"}}}
	upStatusCollector = loggedInUpStatus
	upCommandInteractive = func(*cobra.Command) bool { return true }

	cmd := *upCmd
	cmd.SetIn(strings.NewReader("\n\n\nn\nn\nn\nn\ny\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)

	plan, err := buildUpPlan(&cmd, nil, upOptions{Guided: true}, false)
	if err != nil {
		t.Fatalf("build guided skip plan: %v\noutput:\n%s", err, out.String())
	}
	if plan.SaveConfig {
		t.Fatal("expected config save to be skipped")
	}
	if basicAuthUser != "" || basicAuthPasswordEnv != "" || accessRateLimit != "" || accessAuditEnabled || customDomain != "" {
		t.Fatalf("expected optional HTTPS settings to stay empty, auth=%q/%q rate=%q audit=%v domain=%q", basicAuthUser, basicAuthPasswordEnv, accessRateLimit, accessAuditEnabled, customDomain)
	}
	if !strings.Contains(out.String(), "4. Public access") || !strings.Contains(out.String(), "5. Domain") {
		t.Fatalf("expected guided output to show public access and domain steps:\n%s", out.String())
	}
}

func TestBuildGuidedUpPlanRequiresLogin(t *testing.T) {
	resetUpTestGlobals(t)
	upStatusCollector = func() (*statusPayload, error) {
		return &statusPayload{LoggedIn: false}, nil
	}
	upCommandInteractive = func(*cobra.Command) bool { return true }

	cmd := *upCmd
	cmd.SetIn(strings.NewReader(""))
	_, err := buildUpPlan(&cmd, nil, upOptions{Guided: true}, false)
	if err == nil || !strings.Contains(err.Error(), "sealtun login") {
		t.Fatalf("expected login guidance error, got %v", err)
	}
}

func TestBuildGuidedUpPlanTargetForcesHTTPSProtocol(t *testing.T) {
	resetUpTestGlobals(t)
	exposeTarget = "https://192.168.0.201:8006"
	targetTLSInsecureSkipVerify = true
	upStatusCollector = loggedInUpStatus
	upCommandInteractive = func(*cobra.Command) bool { return true }

	cmd := *upCmd
	cmd.SetIn(strings.NewReader("\nn\nn\nn\nn\ny\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)

	plan, err := buildUpPlan(&cmd, nil, upOptions{Guided: true}, true)
	if err != nil {
		t.Fatalf("build guided target plan: %v\noutput:\n%s", err, out.String())
	}
	if plan.Protocol != tunnelprotocol.HTTPS || plan.TargetURL != "https://192.168.0.201:8006" || plan.LocalPort != "8006" {
		t.Fatalf("unexpected guided target plan: %#v", plan)
	}
	if !strings.Contains(out.String(), "Remote HTTP upstream targets use the HTTPS public tunnel protocol") {
		t.Fatalf("expected target protocol explanation:\n%s", out.String())
	}
}

func resetUpTestGlobals(t *testing.T) {
	t.Helper()
	oldProtocol := protocol
	oldExposeTarget := exposeTarget
	oldTargetTLS := targetTLSInsecureSkipVerify
	oldBasicAuthCredential := basicAuthCredential
	oldBasicAuthUser := basicAuthUser
	oldBasicAuthPassword := basicAuthPassword
	oldBasicAuthPasswordEnv := basicAuthPasswordEnv
	oldBearerToken := bearerToken
	oldBearerTokenEnv := bearerTokenEnv
	oldIPAllowlist := ipAllowlist
	oldIPDenylist := ipDenylist
	oldTemporaryAccessToken := temporaryAccessToken
	oldTemporaryAccessTokenEnv := temporaryAccessTokenEnv
	oldAccessRateLimit := accessRateLimit
	oldAccessAuditEnabled := accessAuditEnabled
	oldCustomDomain := customDomain
	oldWaitDomain := waitDomain
	oldUpPortDiscoverer := upPortDiscoverer
	oldUpStatusCollector := upStatusCollector
	oldUpCommandInteractive := upCommandInteractive
	oldUpConfigFileName := upConfigFileName
	t.Cleanup(func() {
		protocol = oldProtocol
		exposeTarget = oldExposeTarget
		targetTLSInsecureSkipVerify = oldTargetTLS
		basicAuthCredential = oldBasicAuthCredential
		basicAuthUser = oldBasicAuthUser
		basicAuthPassword = oldBasicAuthPassword
		basicAuthPasswordEnv = oldBasicAuthPasswordEnv
		bearerToken = oldBearerToken
		bearerTokenEnv = oldBearerTokenEnv
		ipAllowlist = oldIPAllowlist
		ipDenylist = oldIPDenylist
		temporaryAccessToken = oldTemporaryAccessToken
		temporaryAccessTokenEnv = oldTemporaryAccessTokenEnv
		accessRateLimit = oldAccessRateLimit
		accessAuditEnabled = oldAccessAuditEnabled
		customDomain = oldCustomDomain
		waitDomain = oldWaitDomain
		upPortDiscoverer = oldUpPortDiscoverer
		upStatusCollector = oldUpStatusCollector
		upCommandInteractive = oldUpCommandInteractive
		upConfigFileName = oldUpConfigFileName
	})
	protocol = tunnelprotocol.HTTPS
	exposeTarget = ""
	targetTLSInsecureSkipVerify = false
	basicAuthCredential = ""
	basicAuthUser = ""
	basicAuthPassword = ""
	basicAuthPasswordEnv = ""
	bearerToken = ""
	bearerTokenEnv = ""
	ipAllowlist = nil
	ipDenylist = nil
	temporaryAccessToken = ""
	temporaryAccessTokenEnv = ""
	accessRateLimit = ""
	accessAuditEnabled = false
	customDomain = ""
	waitDomain = false
	upPortDiscoverer = fakePortDiscoverer{}
	upStatusCollector = collectStatus
	upCommandInteractive = defaultUpCommandInteractive
	upConfigFileName = "sealtun.yaml"
}

func loggedInUpStatus() (*statusPayload, error) {
	return &statusPayload{
		LoggedIn: true,
		Region:   "https://gzg.sealos.run",
		Kubeconfig: kubeconfigStatus{
			Present:   true,
			Namespace: "ns-test",
		},
	}, nil
}
