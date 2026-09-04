package cmd

import (
	"strings"
	"testing"
)

func TestValidateExposeRoutesFlag(t *testing.T) {
	origRoutes, origTarget := exposeRoutes, exposeTarget
	t.Cleanup(func() { exposeRoutes, exposeTarget = origRoutes, origTarget })

	exposeTarget = ""
	exposeRoutes = nil
	if parsed, err := validateExposeRoutesFlag("https"); err != nil || parsed != nil {
		t.Fatalf("no routes should parse to nil, got %#v, %v", parsed, err)
	}

	exposeRoutes = []string{"/api=8080", "/admin=9090"}
	parsed, err := validateExposeRoutesFlag("https")
	if err != nil || len(parsed) != 2 || parsed[0].Port != 8080 {
		t.Fatalf("expected two parsed routes, got %#v, %v", parsed, err)
	}

	for _, p := range []string{"ssh", "tcp"} {
		if _, err := validateExposeRoutesFlag(p); err == nil || !strings.Contains(err.Error(), "--route is only supported for https tunnels") {
			t.Fatalf("expected https-only rejection for %s, got %v", p, err)
		}
	}

	exposeTarget = "http://10.0.0.12:8080"
	if _, err := validateExposeRoutesFlag("https"); err == nil || !strings.Contains(err.Error(), "cannot be combined with --target") {
		t.Fatalf("expected --target conflict, got %v", err)
	}
	exposeTarget = ""

	exposeRoutes = []string{"api=8080"}
	if _, err := validateExposeRoutesFlag("https"); err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("expected path validation error, got %v", err)
	}

	exposeRoutes = []string{"/api=8080", "/api=9090"}
	if _, err := validateExposeRoutesFlag("https"); err == nil || !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
}
