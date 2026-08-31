package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
)

func TestCollectInspectPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inspectRemote = false

	if err := session.Save(session.TunnelSession{
		TunnelID:     "abc123",
		Region:       "https://gzg.sealos.run",
		Namespace:    "ns-demo",
		Protocol:     "https",
		Host:         "abc.example.com",
		SealosHost:   "sealtun-abc123-ns-demo.sealosgzg.site",
		CustomDomain: "abc.example.com",
		LocalPort:    "3000",
		PID:          0,
		CreatedAt:    time.Now().Format(time.RFC3339),
		Resources:    []string{"sealtun-abc123"},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectInspectPayload("abc123")
	if err != nil {
		t.Fatalf("collectInspectPayload: %v", err)
	}

	if payload.TunnelID != "abc123" {
		t.Fatalf("unexpected tunnel id: %s", payload.TunnelID)
	}
	if payload.Status != "stale" {
		t.Fatalf("expected stale status, got %s", payload.Status)
	}
	if len(payload.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(payload.Resources))
	}
	if payload.SealosHost != "sealtun-abc123-ns-demo.sealosgzg.site" {
		t.Fatalf("unexpected sealos host: %s", payload.SealosHost)
	}
	if payload.CustomDomain != "abc.example.com" {
		t.Fatalf("unexpected custom domain: %s", payload.CustomDomain)
	}
	if payload.ProcessAlive {
		t.Fatal("expected pid 0 session to report process not alive")
	}
}

func TestCollectInspectPayloadDegradesForegroundTunnelWhenLocalPortIsDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inspectRemote = false

	if err := session.Save(session.TunnelSession{
		TunnelID:        "fg123",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-demo",
		Protocol:        "https",
		Host:            "fg.example.com",
		LocalPort:       "65534",
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
		Resources:       []string{"sealtun-fg123"},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectInspectPayload("fg123")
	if err != nil {
		t.Fatalf("collectInspectPayload: %v", err)
	}
	if payload.Status != "degraded" {
		t.Fatalf("expected degraded status, got %s", payload.Status)
	}
}

func TestCollectInspectPayloadSkipsRemoteDiagnosticsByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inspectRemote = false

	if err := session.Save(session.TunnelSession{
		TunnelID:  "local123",
		Region:    "https://gzg.sealos.run",
		Namespace: "ns-demo",
		Protocol:  "https",
		Host:      "local.example.com",
		LocalPort: "3000",
		PID:       0,
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectInspectPayload("local123")
	if err != nil {
		t.Fatalf("collectInspectPayload: %v", err)
	}
	if payload.Remote != nil {
		t.Fatalf("expected remote diagnostics to be skipped by default, got %#v", payload.Remote)
	}
}

func TestFindSessionPreservesInvalidSessionIDError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := findSession("../auth")
	if err == nil || !strings.Contains(err.Error(), "invalid session tunnel id") {
		t.Fatalf("expected invalid session id error, got %v", err)
	}
}

func TestPrintInspectShowsSSHEndpoint(t *testing.T) {
	payload := &inspectPayload{
		TunnelID:           "sshdev",
		Status:             "active",
		Mode:               "daemon",
		Protocol:           "ssh",
		Host:               "ssh.example.com",
		SealosHost:         "control.example.com",
		PublicPort:         32022,
		LocalPort:          "22",
		Namespace:          "ns-demo",
		Region:             "https://gzg.sealos.run",
		LocalPortReachable: true,
		CreatedAt:          "2026-05-15T10:00:00+08:00",
	}

	var output bytes.Buffer
	inspectCmd.SetOut(&output)
	t.Cleanup(func() { inspectCmd.SetOut(nil) })

	printInspect(inspectCmd, payload)
	text := output.String()
	for _, want := range []string{
		"Public SSH host: ssh.example.com",
		"Public SSH port: 32022",
		"SSH command: ssh <user>@ssh.example.com -p 32022",
		"Control host: control.example.com",
		"Target: localhost:22",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected inspect output to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Public URL") {
		t.Fatalf("ssh inspect output should not show Public URL, got:\n%s", text)
	}
}

func TestPrintInspectShowsTCPEndpoint(t *testing.T) {
	payload := &inspectPayload{
		TunnelID:           "postgres",
		Status:             "active",
		Mode:               "daemon",
		Protocol:           "tcp",
		Host:               "db.example.com",
		SealosHost:         "control.example.com",
		PublicPort:         35432,
		LocalPort:          "5432",
		Namespace:          "ns-demo",
		Region:             "https://gzg.sealos.run",
		LocalPortReachable: true,
		CreatedAt:          "2026-05-15T10:00:00+08:00",
	}

	var output bytes.Buffer
	inspectCmd.SetOut(&output)
	t.Cleanup(func() { inspectCmd.SetOut(nil) })

	printInspect(inspectCmd, payload)
	text := output.String()
	for _, want := range []string{
		"Public TCP host: db.example.com",
		"Public TCP port: 35432",
		"Public TCP endpoint: db.example.com:35432",
		"Control host: control.example.com",
		"Target: localhost:5432",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected inspect output to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Public URL") || strings.Contains(text, "SSH command") {
		t.Fatalf("tcp inspect output should not show HTTPS or SSH endpoint fields, got:\n%s", text)
	}
}

func TestPrintInspectHidesRemoteCertificateSecretInText(t *testing.T) {
	payload := &inspectPayload{
		TunnelID:           "webdev",
		Status:             "active",
		Mode:               "daemon",
		Protocol:           "https",
		Host:               "web.example.com",
		LocalPort:          "3000",
		LocalPortReachable: true,
		Remote: &k8s.TunnelDiagnostics{
			Certificate: &k8s.CertificateDiagnostics{
				Exists:     true,
				Ready:      true,
				SecretName: "sealtun-webdev-custom-tls",
			},
		},
	}

	var output bytes.Buffer
	inspectCmd.SetOut(&output)
	t.Cleanup(func() { inspectCmd.SetOut(nil) })

	printInspect(inspectCmd, payload)
	text := output.String()
	if strings.Contains(text, "secret=") || strings.Contains(text, "sealtun-webdev-custom-tls") {
		t.Fatalf("inspect text output should not expose certificate secret names, got:\n%s", text)
	}
}

func TestCollectInspectPayloadRefreshesRemoteHTTPSState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inspectRemote = false

	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return &k8s.TunnelRemoteState{
			PublicHost:   "ai-gateway.code05.com",
			SealosHost:   "sealtun-abc123-ns-demo.bja.sealos.run",
			CustomDomain: "ai-gateway.code05.com",
		}, nil
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Region:    "https://bja.sealos.run",
		Namespace: "ns-demo",
		Protocol:  "https",
		Host:      "old.example.com",
		LocalPort: "3000",
		PID:       0,
		CreatedAt: time.Now().Format(time.RFC3339),
		Resources: []string{"sealtun-abc123"},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectInspectPayload("abc123")
	if err != nil {
		t.Fatalf("collectInspectPayload: %v", err)
	}
	if payload.Host != "ai-gateway.code05.com" || payload.CustomDomain != "ai-gateway.code05.com" {
		t.Fatalf("expected refreshed host/custom domain, got %#v", payload)
	}
	if payload.SealosHost != "sealtun-abc123-ns-demo.bja.sealos.run" {
		t.Fatalf("expected refreshed sealos host, got %#v", payload)
	}
}
