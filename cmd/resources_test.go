package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

func TestPrintTunnelResourcesHidesSecretDataAndShowsHints(t *testing.T) {
	payload := &k8s.TunnelResourceList{
		Namespace: "ns-demo",
		TunnelID:  "abc123",
		Resources: []k8s.TunnelResource{{
			Kind:      "Secret",
			Name:      "sealtun-abc123-auth",
			Status:    "Opaque",
			Namespace: "ns-demo",
			Managed:   true,
			CostHints: []string{"secret data hidden"},
		}},
	}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	printTunnelResources(cmd, payload)
	text := out.String()
	if !strings.Contains(text, "resource hints show Kubernetes occupancy") || !strings.Contains(text, "secret data hidden") {
		t.Fatalf("expected resources note and hint, got %s", text)
	}
	if strings.Contains(text, "Data:") || strings.Contains(text, "token") || strings.Contains(text, "tls.key") {
		t.Fatalf("resources output should not expose secret data fields: %s", text)
	}
}

func TestNormalizeResourceConfigDefaultsAndValidates(t *testing.T) {
	got, err := normalizeResourceConfig(&session.ResourceConfig{
		Requests: &session.ResourceValues{CPU: "20m"},
		Limits:   &session.ResourceValues{Memory: "256Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests.CPU != "20m" || got.Requests.Memory != k8s.DefaultRequestMemory {
		t.Fatalf("unexpected request defaults: %#v", got.Requests)
	}
	if got.Limits.CPU != k8s.DefaultLimitCPU || got.Limits.Memory != "256Mi" {
		t.Fatalf("unexpected limit defaults: %#v", got.Limits)
	}

	if _, err := normalizeResourceConfig(&session.ResourceConfig{
		Requests: &session.ResourceValues{CPU: "500m"},
		Limits:   &session.ResourceValues{CPU: "100m"},
	}); err == nil || !strings.Contains(err.Error(), "limit must be greater than or equal") {
		t.Fatalf("expected limit/request validation error, got %v", err)
	}
	if _, err := normalizeResourceConfig(&session.ResourceConfig{
		Requests: &session.ResourceValues{Memory: "bad-unit"},
	}); err == nil || !strings.Contains(err.Error(), "invalid memory request quantity") {
		t.Fatalf("expected invalid quantity error, got %v", err)
	}
}

func TestActiveScopedSessionRejectsOutsideScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := auth.SaveAuthData(auth.AuthData{Region: "https://gzg.sealos.run"}, testKubeconfig(t, "ns-a")); err != nil {
		t.Fatal(err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "otherns",
		Region:    "https://gzg.sealos.run",
		Namespace: "ns-b",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := activeScopedSession(context.Background(), "otherns"); err == nil || !strings.Contains(err.Error(), "outside the active scope") {
		t.Fatalf("expected active scope rejection, got %v", err)
	}
}
