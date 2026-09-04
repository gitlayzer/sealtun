package cmd

import (
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/routes"
	"github.com/labring/sealtun/pkg/session"
)

func TestNormalizeApplyTunnelAcceptsRoutes(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeApplyTunnel(applyTunnel{
		Name:      "app",
		LocalPort: 3000,
		Routes:    []routes.Route{{Path: "/api", Port: 8080}, {Path: "/admin", Port: 9090}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(normalized.Routes) != 2 || normalized.Routes[0].Port != 8080 || normalized.Routes[1].Path != "/admin" {
		t.Fatalf("routes not normalized: %#v", normalized.Routes)
	}
	if normalized.LocalPort != "3000" {
		t.Fatalf("primary local port changed: %q", normalized.LocalPort)
	}
}

func TestNormalizeApplyTunnelRejectsInvalidRoutes(t *testing.T) {
	t.Parallel()

	cases := map[string]applyTunnel{
		"with target":       {Name: "app", Target: "http://10.0.0.12:8080", Routes: []routes.Route{{Path: "/api", Port: 8080}}},
		"with ssh protocol": {Name: "app", LocalPort: 22, Protocol: "ssh", Routes: []routes.Route{{Path: "/api", Port: 8080}}},
		"duplicate prefix":  {Name: "app", LocalPort: 3000, Routes: []routes.Route{{Path: "/api", Port: 8080}, {Path: "/api", Port: 9090}}},
		"missing slash":     {Name: "app", LocalPort: 3000, Routes: []routes.Route{{Path: "api", Port: 8080}}},
		"invalid port":      {Name: "app", LocalPort: 3000, Routes: []routes.Route{{Path: "/api", Port: 0}}},
	}
	for name, item := range cases {
		if _, err := normalizeApplyTunnel(item); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestBuildApplySessionRecordCarriesRoutes(t *testing.T) {
	t.Parallel()

	record := buildApplySessionRecord(normalizedApplyTunnel{
		Name:      "app",
		TunnelID:  "app",
		LocalPort: "3000",
		Protocol:  "https",
		Routes:    []routes.Route{{Path: "/api", Port: 8080}},
	}, nil, "ns-x", "kubeconfig", "secret", k8s.TunnelHosts{}, "")
	if len(record.Routes) != 1 || record.Routes[0].Port != 8080 {
		t.Fatalf("session record lost routes: %#v", record.Routes)
	}
}

func TestDiffDetectsRouteChanges(t *testing.T) {
	t.Parallel()

	config := &applyFile{Tunnels: []applyTunnel{{
		Name:      "app",
		LocalPort: 3000,
		Routes:    []routes.Route{{Path: "/api", Port: 8080}},
	}}}
	lookup := func(string) (*session.TunnelSession, error) {
		return &session.TunnelSession{TunnelID: "app", LocalPort: "3000", Protocol: "https"}, nil
	}
	results, err := runDiffConfigWithSessionLookup(config, lookup)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one diff result, got %d", len(results))
	}
	found := false
	for _, change := range results[0].Changes {
		if strings.HasPrefix(change, "routes:") && strings.Contains(change, "/api->8080") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a routes change entry, got %#v", results[0].Changes)
	}
	if results[0].Action != "update" {
		t.Fatalf("routes change should mark the tunnel for update, got %q", results[0].Action)
	}
}

func TestDiffNoChangeWhenRoutesMatch(t *testing.T) {
	t.Parallel()

	config := &applyFile{Tunnels: []applyTunnel{{
		Name:      "app",
		LocalPort: 3000,
		Routes:    []routes.Route{{Path: "/api/", Port: 8080}},
	}}}
	lookup := func(string) (*session.TunnelSession, error) {
		return &session.TunnelSession{
			TunnelID:  "app",
			LocalPort: "3000",
			Protocol:  "https",
			Routes:    []routes.Route{{Path: "/api", Port: 8080}},
		}, nil
	}
	results, err := runDiffConfigWithSessionLookup(config, lookup)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	for _, change := range results[0].Changes {
		if strings.HasPrefix(change, "routes:") {
			t.Fatalf("equivalent routes should not diff, got %q", change)
		}
	}
}
