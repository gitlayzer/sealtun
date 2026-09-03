package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
)

func TestCollectDoctorPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "abc.example.com",
		LocalPort: "3000",
		PID:       0,
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}

	if payload.TotalSessions != 1 {
		t.Fatalf("expected 1 total session, got %d", payload.TotalSessions)
	}
	if payload.StaleSessions != 1 {
		t.Fatalf("expected 1 stale session, got %d", payload.StaleSessions)
	}
	if len(payload.Warnings) == 0 {
		t.Fatal("expected warnings to be present")
	}
}

func TestCollectTunnelDoctorPayloadForStoppedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stopdoc",
		Host:            "stop.example.com",
		LocalPort:       "3000",
		Protocol:        "https",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectTunnelDoctorPayload(context.Background(), "stopdoc")
	if err != nil {
		t.Fatalf("collectTunnelDoctorPayload returned error: %v", err)
	}
	if payload.Status != "stopped" {
		t.Fatalf("expected stopped status, got %s", payload.Status)
	}
	if len(payload.Checks) < 3 || payload.Checks[1].Status != "skip" || payload.Checks[2].Status != "skip" {
		t.Fatalf("expected stopped owner/local-port checks to be skipped, got %#v", payload.Checks)
	}
	if len(payload.Suggestions) == 0 || !strings.Contains(payload.Suggestions[0], "sealtun start stopdoc") {
		t.Fatalf("expected start suggestion, got %#v", payload.Suggestions)
	}
}

func TestRemoteDoctorChecksSkipScaledToZeroDeployment(t *testing.T) {
	checks := remoteDoctorChecks(&k8s.TunnelDiagnostics{
		Deployment: k8s.DeploymentDiagnostics{
			Exists:          true,
			DesiredReplicas: 0,
			ReadyReplicas:   0,
		},
		Service: k8s.ServiceDiagnostics{Exists: true},
		Ingress: k8s.IngressDiagnostics{Exists: true},
	})
	if len(checks) == 0 || checks[0].Name != "deployment" || checks[0].Status != "skip" {
		t.Fatalf("expected scaled-to-zero deployment check to be skipped, got %#v", checks)
	}
	if len(checks) < 2 || checks[1].Detail != "no ports reported" {
		t.Fatalf("expected empty service ports to have a readable detail, got %#v", checks)
	}
}

func TestRemoteDoctorChecksHideCertificateSecretName(t *testing.T) {
	checks := remoteDoctorChecks(&k8s.TunnelDiagnostics{
		Deployment: k8s.DeploymentDiagnostics{
			Exists:          true,
			DesiredReplicas: 1,
			ReadyReplicas:   1,
		},
		Service: k8s.ServiceDiagnostics{Exists: true, Ports: []string{"http:80"}},
		Ingress: k8s.IngressDiagnostics{Exists: true, Hosts: []string{"app.example.com"}},
		Certificate: &k8s.CertificateDiagnostics{
			Exists:     true,
			Ready:      true,
			SecretName: "sealtun-webdev-custom-tls",
		},
	})

	for _, check := range checks {
		if strings.Contains(check.Detail, "sealtun-webdev-custom-tls") {
			t.Fatalf("doctor check should not expose certificate secret names, got %#v", checks)
		}
	}
}

func TestCertificateDoctorDetail(t *testing.T) {
	tests := []struct {
		name string
		cert *k8s.CertificateDiagnostics
		want string
	}{
		{name: "nil", cert: nil, want: "missing"},
		{name: "missing", cert: &k8s.CertificateDiagnostics{}, want: "missing"},
		{name: "not ready", cert: &k8s.CertificateDiagnostics{Exists: true}, want: "not ready"},
		{name: "ready", cert: &k8s.CertificateDiagnostics{Exists: true, Ready: true}, want: "ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := certificateDoctorDetail(tt.cert); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCollectDoctorPayloadDoesNotRequireDaemonForForegroundSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:  "fg123",
		Host:      "fg.example.com",
		LocalPort: "3000",
		PID:       currentPIDForTest(),
		Mode:      "foreground",
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}

	for _, warning := range payload.Warnings {
		if warning == "daemon is not running; daemon-managed tunnels will not reconnect until it starts" {
			t.Fatal("foreground-only sessions should not require daemon")
		}
	}
}

func TestCollectDoctorPayloadCountsConnectingDaemonSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := daemonstate.SaveState(os.Getpid()); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = daemonstate.DeleteState()
	})

	if err := session.Save(session.TunnelSession{
		TunnelID:        "daemon123",
		Host:            "daemon.example.com",
		LocalPort:       "3000",
		PID:             os.Getpid(),
		Mode:            "daemon",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnecting,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}
	if payload.ConnectingSessions != 1 {
		t.Fatalf("expected 1 connecting session, got %d", payload.ConnectingSessions)
	}
	if payload.StaleSessions != 0 {
		t.Fatalf("expected no stale sessions, got %d", payload.StaleSessions)
	}
}

func TestSessionIsStaleTreatsStoppedDaemonSessionAsCleanupEligible(t *testing.T) {
	if !sessionIsStale(session.TunnelSession{
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		UpdatedAt:       time.Now().Format(time.RFC3339),
	}, time.Minute) {
		t.Fatal("expected stopped daemon session to be cleanup eligible")
	}
}

func TestSessionNeedsAutomaticRecoverySkipsStoppedSession(t *testing.T) {
	if sessionNeedsAutomaticRecovery(session.TunnelSession{
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		UpdatedAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
	}, time.Minute) {
		t.Fatal("expected stopped session to be preserved during automatic recovery")
	}
}

func TestSessionNeedsAutomaticRecoveryIncludesExpiredStoppedSession(t *testing.T) {
	if !sessionNeedsAutomaticRecovery(session.TunnelSession{
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		ExpiresAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt:       time.Now().Format(time.RFC3339),
	}, time.Minute) {
		t.Fatal("expected expired stopped session to be automatic cleanup eligible")
	}
}

func TestTunnelCleanupShouldPreserveStoppedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "paused123",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	if !tunnelCleanupShouldPreserve("paused123") {
		t.Fatal("expected stopped session to preserve remote resources during foreground cleanup")
	}
	if tunnelCleanupShouldPreserve("missing") {
		t.Fatal("expected missing session to allow cleanup")
	}
}

func TestStartRejectsExpiredSessionBeforeRemoteMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "expired123",
		Secret:          "secret",
		ExpiresAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	err := startCmd.RunE(startCmd, []string{"expired123"})
	if err == nil || !strings.Contains(err.Error(), "has expired") {
		t.Fatalf("expected expired start rejection, got %v", err)
	}
}

func TestRollbackStartedTunnelSessionMarksSessionStopped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	previousPause := pauseSessionResources
	pauseSessionResources = func(context.Context, session.TunnelSession) error {
		return nil
	}
	t.Cleanup(func() { pauseSessionResources = previousPause })

	sess := session.TunnelSession{
		TunnelID:        "rollbackstart",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-demo",
		Kubeconfig:      "kubeconfig",
		Protocol:        "https",
		Host:            "sealtun-rollbackstart-ns-demo.sealosgzg.site",
		LocalPort:       "3000",
		Secret:          "secret",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStatePending,
	}
	if err := session.Save(sess); err != nil {
		t.Fatal(err)
	}

	err := rollbackStartedTunnelSession(sess, fmt.Errorf("daemon failed"))
	if err == nil || !strings.Contains(err.Error(), "daemon failed") {
		t.Fatalf("expected original error, got %v", err)
	}
	got, err := session.Get(sess.TunnelID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConnectionState != session.ConnectionStateStopped {
		t.Fatalf("expected stopped state, got %q", got.ConnectionState)
	}
	if got.LastError != "daemon failed" {
		t.Fatalf("expected last error to preserve cause, got %q", got.LastError)
	}
}

func TestCollectDoctorPayloadCountsStoppedSeparately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stopped123",
		Host:            "stopped.example.com",
		LocalPort:       "3000",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}
	if payload.StoppedSessions != 1 {
		t.Fatalf("expected 1 stopped session, got %d", payload.StoppedSessions)
	}
	if payload.StaleSessions != 0 {
		t.Fatalf("expected no stale sessions, got %d", payload.StaleSessions)
	}
}

func TestCollectDoctorPayloadCountsDegradedSessionsSeparately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	activePort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "active-down",
		Host:            "active.example.com",
		LocalPort:       "65534",
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save active session: %v", err)
	}

	if err := session.Save(session.TunnelSession{
		TunnelID:        "connecting-up",
		Host:            "connecting.example.com",
		LocalPort:       activePort,
		PID:             currentPIDForTest(),
		Mode:            "daemon",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnecting,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save connecting session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}
	if payload.ActiveSessions != 0 {
		t.Fatalf("expected no active sessions, got %d", payload.ActiveSessions)
	}
	if payload.DegradedSessions != 1 {
		t.Fatalf("expected 1 degraded session, got %d", payload.DegradedSessions)
	}
	if payload.ReachableActivePorts != 0 {
		t.Fatalf("expected no reachable active ports, got %d", payload.ReachableActivePorts)
	}
	if !containsWarning(payload.Warnings, "1 tunnel session(s) have a live owner but unreachable local port") {
		t.Fatalf("expected degraded warning, got %#v", payload.Warnings)
	}
}

func TestDoctorFixDryRunDoesNotMutateStoppedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stoppedfix",
		Secret:          "secret",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	previousStart := doctorFixStartTunnel
	started := 0
	doctorFixStartTunnel = func(context.Context, *session.TunnelSession) error {
		started++
		return nil
	}
	t.Cleanup(func() { doctorFixStartTunnel = previousStart })

	payload, err := runDoctorFix(context.Background(), []string{"stoppedfix"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.DryRun || len(payload.Actions) != 1 || payload.Actions[0].Action != "start" {
		t.Fatalf("unexpected dry-run payload: %#v", payload)
	}
	if started != 0 {
		t.Fatalf("dry-run should not execute start, started=%d", started)
	}
	got, err := session.Get("stoppedfix")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConnectionState != session.ConnectionStateStopped {
		t.Fatalf("dry-run mutated session state: %s", got.ConnectionState)
	}
}

func TestDoctorFixStartWaitsForTunnelOperationLock(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:        "doctorlocked",
		Secret:          "secret",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
	}); err != nil {
		t.Fatal(err)
	}

	releaseLock := holdTunnelOperationLock(t, "doctorlocked")
	defer releaseLock()
	called := make(chan struct{}, 1)
	want := errors.New("doctor start after lock")
	previousStart := doctorFixStartTunnel
	doctorFixStartTunnel = func(context.Context, *session.TunnelSession) error {
		called <- struct{}{}
		return want
	}
	t.Cleanup(func() { doctorFixStartTunnel = previousStart })

	done := make(chan error, 1)
	go func() {
		done <- executeDoctorFixAction(context.Background(), doctorFixAction{Action: "start", TunnelID: "doctorlocked"})
	}()
	assertOperationBlocked(t, called)
	releaseLock()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("doctor fix error = %v, want %v", err, want)
	}
}

func TestDoctorFixRefusesStoppedSessionWithoutSecret(t *testing.T) {
	action := doctorFixActionsForSession(session.TunnelSession{
		TunnelID:        "scrubbed",
		ConnectionState: session.ConnectionStateStopped,
	})
	if len(action) != 1 || action[0].Allowed {
		t.Fatalf("expected scrubbed stopped tunnel start to be blocked, got %#v", action)
	}
}

func TestDoctorFixCleanupOnlyRunsForStaleOrExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "activefix",
		Secret:          "secret",
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	err := executeDoctorFixAction(context.Background(), doctorFixAction{Action: "cleanup", TunnelID: "activefix"})
	if err == nil || !strings.Contains(err.Error(), "refusing to cleanup non-stale active tunnel") {
		t.Fatalf("expected active cleanup refusal, got %v", err)
	}
}

func TestDoctorFixStartsDaemonInsteadOfCleaningDaemonSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "daemonfix",
		Secret:          "secret",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateConnecting,
		UpdatedAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
		CreatedAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	payload, err := runDoctorFix(context.Background(), []string{"daemonfix"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Actions) != 1 || payload.Actions[0].Action != "daemon-start" {
		t.Fatalf("expected daemon-start only, got %#v", payload.Actions)
	}
}

func TestDoctorFixRefusesDaemonCleanupWhenDaemonIsDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	previousCleanup := doctorFixCleanupResources
	cleanupCalled := 0
	doctorFixCleanupResources = func(context.Context, session.TunnelSession) error {
		cleanupCalled++
		return nil
	}
	t.Cleanup(func() { doctorFixCleanupResources = previousCleanup })

	if err := session.Save(session.TunnelSession{
		TunnelID:        "daemonactive",
		Secret:          "secret",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateConnected,
		UpdatedAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
		CreatedAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	err := executeDoctorFixAction(context.Background(), doctorFixAction{Action: "cleanup", TunnelID: "daemonactive"})
	if err == nil || !strings.Contains(err.Error(), "refusing to cleanup") {
		t.Fatalf("expected daemon cleanup refusal, got %v", err)
	}
	if cleanupCalled != 0 {
		t.Fatalf("daemon cleanup should not execute remote cleanup, called=%d", cleanupCalled)
	}
}

func TestDoctorFixExecutionErrorReportsFailedActions(t *testing.T) {
	payload := &doctorFixPayload{Actions: []doctorFixAction{{
		Action:  "start",
		Allowed: true,
		Error:   "daemon failed",
	}}}
	err := doctorFixExecutionError(payload)
	if err == nil || !strings.Contains(err.Error(), "failed to execute 1 doctor fix action") {
		t.Fatalf("expected failed action error, got %v", err)
	}
	payload.DryRun = true
	if err := doctorFixExecutionError(payload); err != nil {
		t.Fatalf("dry-run should not return execution error, got %v", err)
	}
}

func TestTunnelDoctorReportRedactsSecrets(t *testing.T) {
	payload := &tunnelDoctorPayload{
		TunnelID:           "report123",
		Status:             "error",
		Protocol:           "https",
		Endpoint:           "https://report.example.com",
		LocalTarget:        "http://localhost:3000",
		Mode:               "daemon",
		Region:             "https://gzg.sealos.run",
		Namespace:          "ns-demo",
		ProcessAlive:       false,
		LocalPortReachable: false,
		LastError:          "Authorization: Bearer abcdefgh secret=server-secret token=temporary-token password=hunter2",
		Checks: []doctorCheck{{
			Name:   "remote",
			Status: "warn",
			Detail: "token=check-token",
		}},
		Suggestions: []string{"retry without Authorization: Basic dXNlcjpwYXNz"},
		Warnings:    []string{"_sealtun_token=share-token should not leak"},
	}
	report := renderTunnelDoctorReport(payload)
	for _, leaked := range []string{"abcdefgh", "server-secret", "temporary-token", "hunter2", "check-token", "dXNlcjpwYXNz", "share-token"} {
		if strings.Contains(report, leaked) {
			t.Fatalf("report leaked %q:\n%s", leaked, report)
		}
	}
	for _, want := range []string{"<redacted>", "Sealtun Doctor Report", "report123"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestWriteTunnelDoctorReportUsesRequestedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doctor.md")
	got, err := writeTunnelDoctorReport(path, &tunnelDoctorPayload{TunnelID: "reportfile", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("expected report path %q, got %q", path, got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "reportfile") {
		t.Fatalf("report missing tunnel id:\n%s", data)
	}
}

func TestWriteTunnelDoctorReportRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "doctor.md")
	if err := os.Symlink(target, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := writeTunnelDoctorReport(linked, &tunnelDoctorPayload{TunnelID: "reportfile", Status: "active"})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked report to be rejected, got %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "original" {
		t.Fatalf("outside target changed: data=%q err=%v", data, readErr)
	}
}

func TestActionableErrorHint(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{name: "quota", msg: "pods is forbidden: exceeded quota cpu", want: "balance/quota"},
		{name: "dns", msg: "custom domain DNS is not verified: lookup app.example.com", want: "DNS may not have propagated"},
		{name: "tls", msg: "x509: certificate signed by unknown authority", want: "--target-insecure-skip-verify"},
		{name: "network", msg: "dial tcp 10.0.0.1:443: i/o timeout", want: "not reachable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionableErrorHintText(tt.msg)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("expected hint containing %q, got %q", tt.want, got)
			}
		})
	}
}

func TestActionableErrorHintDoesNotFireOnLocalDomainValidation(t *testing.T) {
	for _, msg := range []string{
		`invalid custom domain "1.2.3.4": custom domain must be a DNS hostname, not an IP address`,
		`invalid custom domain "app_example.com": label "app_example" is not DNS compatible`,
	} {
		if got := actionableErrorHintText(msg); got != "" {
			t.Fatalf("local domain validation error %q should not produce a DNS hint, got %q", msg, got)
		}
	}
}

func TestActionableErrorHintQuotaUsesCurrentCommands(t *testing.T) {
	got := actionableErrorHintText("pods is forbidden: exceeded quota cpu")
	if strings.Contains(got, "resources set") {
		t.Fatalf("quota hint references the removed resources set command: %q", got)
	}
	if !strings.Contains(got, "sealtun apply") {
		t.Fatalf("quota hint should point to YAML apply, got %q", got)
	}
}

func TestActionableErrorHintClusterTLSMentionsReLogin(t *testing.T) {
	got := actionableErrorHintText(`Get "https://gzg.sealos.run:6443/api/v1/namespaces/ns-x/secrets/s": tls: failed to verify certificate: x509: certificate signed by unknown authority`)
	if !strings.Contains(got, "login") {
		t.Fatalf("cluster CA hint should mention re-login, got %q", got)
	}
}

func TestCommandErrorWithHint(t *testing.T) {
	got := commandErrorWithHint(fmt.Errorf("x509: certificate signed by unknown authority"))
	if !strings.Contains(got, "Hint:") || !strings.Contains(got, "--target-insecure-skip-verify") {
		t.Fatalf("expected hint in command error, got %q", got)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}

func TestActionableErrorHintDoesNotFireOnLocalFilePermission(t *testing.T) {
	for _, msg := range []string{
		`open /tmp/sealtun.yaml: permission denied`,
		`read /Users/x/.sealtun/auth.json: permission denied`,
		`lstat /Users/x/.sealtun/sessions.lock: permission denied`,
		`mkdir /Users/x/.sealtun/sessions: permission denied`,
	} {
		if got := actionableErrorHintText(msg); got != "" {
			t.Fatalf("local filesystem permission error %q should not produce a re-login hint, got %q", msg, got)
		}
	}
}

func TestActionableErrorHintClusterRBACStillFires(t *testing.T) {
	got := actionableErrorHintText(`deployments.apps "sealtun-web" is forbidden: User "u" cannot get resource`)
	if !strings.Contains(got, "re-login") && !strings.Contains(got, "permission") {
		t.Fatalf("cluster RBAC error should still produce the login hint, got %q", got)
	}
}

func TestRedactSensitiveTextCoversURLUserinfo(t *testing.T) {
	input := "Target: https://admin:secretpass@10.0.0.1:8443/api"
	got := redactSensitiveText(input)
	if got == input {
		t.Fatalf("url userinfo should be redacted, got %q", got)
	}
	if !strings.Contains(got, "https://admin:<redacted>@10.0.0.1:8443") {
		t.Fatalf("unexpected redaction result: %q", got)
	}
	clean := redactSensitiveText("Target: https://10.0.0.1:8443/api")
	if clean != "Target: https://10.0.0.1:8443/api" {
		t.Fatalf("urls without userinfo must not be modified, got %q", clean)
	}
}
