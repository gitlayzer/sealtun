package cmd

import "testing"

func TestServerProtocolValidation(t *testing.T) {
	t.Parallel()

	valid := []string{"https", "HTTPS"}
	for _, protocol := range valid {
		if err := validateProtocol(protocol); err != nil {
			t.Fatalf("expected expose protocol %s to be valid: %v", protocol, err)
		}
	}

	invalid := []string{"http", "grpc", "grpcs", "tcp", "udp", "wss"}
	for _, protocol := range invalid {
		if err := validateProtocol(protocol); err == nil {
			t.Fatalf("expected expose protocol %s to be rejected", protocol)
		}
	}
}

func TestResolveServerSecretFromEnv(t *testing.T) {
	got, err := resolveServerSecret("flag-secret", "SEALTUN_SECRET", func(name string) string {
		if name != "SEALTUN_SECRET" {
			t.Fatalf("unexpected env lookup: %s", name)
		}
		return "env-secret"
	})
	if err != nil {
		t.Fatalf("resolveServerSecret returned error: %v", err)
	}
	if got != "env-secret" {
		t.Fatalf("expected env secret to win, got %q", got)
	}
}

func TestResolveServerSecretRejectsEmptyEnv(t *testing.T) {
	_, err := resolveServerSecret("", "SEALTUN_SECRET", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected empty secret env to fail")
	}
}
