package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/auth"
)

func TestCollectStatusLoggedOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	status, err := collectStatus()
	if err != nil {
		t.Fatalf("collectStatus returned error: %v", err)
	}

	if status.LoggedIn {
		t.Fatal("expected logged out status")
	}
	if status.ConfigDir == "" {
		t.Fatal("expected config dir to be populated")
	}
	if status.AuthFile.Present {
		t.Fatal("expected auth file to be absent")
	}
}

func TestCollectStatusWithKubeconfigSummary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := auth.SaveAuthData(auth.AuthData{
		Region:          "https://gzg.sealos.run",
		AuthMethod:      "oauth2_device_grant",
		AuthenticatedAt: "2026-04-20T08:00:00Z",
		CurrentWorkspace: &auth.Workspace{
			ID:       "ns-test",
			TeamName: "demo",
		},
	}, `
apiVersion: v1
kind: Config
current-context: ctx-demo
contexts:
- name: ctx-demo
  context:
    cluster: cluster-demo
    namespace: ns-demo
clusters:
- name: cluster-demo
  cluster:
    server: https://example.com
users:
- name: user-demo
  user:
    token: abc
`); err != nil {
		t.Fatalf("SaveAuthData returned error: %v", err)
	}

	status, err := collectStatus()
	if err != nil {
		t.Fatalf("collectStatus returned error: %v", err)
	}

	if !status.LoggedIn {
		t.Fatal("expected logged in status")
	}
	if status.SealosDomain != "" {
		t.Fatalf("expected empty sealos domain, got %s", status.SealosDomain)
	}
	if status.Kubeconfig.CurrentContext != "ctx-demo" {
		t.Fatalf("unexpected current context: %s", status.Kubeconfig.CurrentContext)
	}
	if status.Kubeconfig.Cluster != "cluster-demo" {
		t.Fatalf("unexpected cluster: %s", status.Kubeconfig.Cluster)
	}
	if status.Kubeconfig.Namespace != "ns-demo" {
		t.Fatalf("unexpected namespace: %s", status.Kubeconfig.Namespace)
	}
	if status.WorkspaceID != "ns-test" {
		t.Fatalf("unexpected workspace id: %s", status.WorkspaceID)
	}
}

func TestCollectStatusIncludesSealosDomain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := auth.SaveAuthData(auth.AuthData{
		Region:          "https://hzh.sealos.run",
		SealosDomain:    "sealoshzh.site",
		AuthMethod:      "oauth2_device_grant",
		AuthenticatedAt: "2026-05-06T08:00:00Z",
	}, `
apiVersion: v1
kind: Config
current-context: ctx-demo
contexts:
- name: ctx-demo
  context:
    cluster: cluster-demo
clusters:
- name: cluster-demo
  cluster:
    server: https://example.com
users:
- name: user-demo
  user:
    token: abc
`); err != nil {
		t.Fatalf("SaveAuthData returned error: %v", err)
	}

	status, err := collectStatus()
	if err != nil {
		t.Fatalf("collectStatus returned error: %v", err)
	}
	if status.SealosDomain != "sealoshzh.site" {
		t.Fatalf("expected sealos domain sealoshzh.site, got %s", status.SealosDomain)
	}
}

func TestStatusJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := auth.GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kubeconfig"), []byte("not yaml"), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	status, err := collectStatus()
	if err != nil {
		t.Fatalf("collectStatus returned error: %v", err)
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	jsonText := string(data)
	if !strings.Contains(jsonText, `"loggedIn":false`) {
		t.Fatalf("expected loggedIn field in json output: %s", jsonText)
	}
	if !strings.Contains(jsonText, `"kubeconfig"`) {
		t.Fatalf("expected kubeconfig field in json output: %s", jsonText)
	}
}
