package cmd

import (
	"strings"
	"testing"
)

func TestInlineSecretWarnings(t *testing.T) {
	item := applyTunnel{
		Name: "web",
		BasicAuth: &applyBasicAuth{
			Username: "admin",
			Password: "secret",
		},
		AccessPolicy: &applyAccessPolicy{
			BearerToken: "token",
			TemporaryLinks: []applyTemporaryLink{
				{Name: "review", Token: "link-token"},
			},
		},
	}
	warnings := inlineSecretWarnings(item)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings, got %v", warnings)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"passwordEnv", "bearerTokenEnv", "tokenEnv"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings should mention %s: %v", want, warnings)
		}
	}
}

func TestInlineSecretWarningsSkipsEnvReferences(t *testing.T) {
	item := applyTunnel{
		Name:      "web",
		BasicAuth: &applyBasicAuth{Username: "admin", PasswordEnv: "PW"},
		AccessPolicy: &applyAccessPolicy{
			BearerTokenEnv: "TOKEN",
			TemporaryLinks: []applyTemporaryLink{{Name: "review", TokenEnv: "LINK"}},
		},
	}
	if warnings := inlineSecretWarnings(item); len(warnings) != 0 {
		t.Fatalf("env-backed secrets should not warn, got %v", warnings)
	}
}
