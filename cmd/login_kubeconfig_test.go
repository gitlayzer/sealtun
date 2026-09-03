package cmd

import (
	"strings"
	"testing"
)

const validLoginKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://gzg.sealos.run:6443
    insecure-skip-tls-verify: true
  name: sealos
contexts:
- context:
    cluster: sealos
    user: u
    namespace: ns-x
  name: ctx
current-context: ctx
users:
- name: u
  user:
    token: fake
`

func TestValidateLoginKubeconfigAcceptsValidConfig(t *testing.T) {
	if err := validateLoginKubeconfig(validLoginKubeconfig); err != nil {
		t.Fatalf("valid kubeconfig rejected: %v", err)
	}
}

func TestValidateLoginKubeconfigRejectsExecPlugin(t *testing.T) {
	malicious := strings.Replace(validLoginKubeconfig, "token: fake", "exec:\n      command: /bin/sh\n      apiVersion: client.authentication.k8s.io/v1", 1)
	err := validateLoginKubeconfig(malicious)
	if err == nil || !strings.Contains(err.Error(), "exec credential plugin") {
		t.Fatalf("expected exec plugin rejection, got %v", err)
	}
}

func TestValidateLoginKubeconfigRejectsHTTPCluster(t *testing.T) {
	malicious := strings.Replace(validLoginKubeconfig, "https://gzg.sealos.run:6443", "http://evil.example.com:6443", 1)
	err := validateLoginKubeconfig(malicious)
	if err == nil || !strings.Contains(err.Error(), "non-TLS") {
		t.Fatalf("expected non-TLS rejection, got %v", err)
	}
}

func TestValidateLoginKubeconfigRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "not yaml at all", "apiVersion: v1\nkind: Config\n"} {
		if err := validateLoginKubeconfig(bad); err == nil {
			t.Fatalf("expected rejection for %q", bad)
		}
	}
}
