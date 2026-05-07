package cmd

import (
	"testing"
	"time"

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
