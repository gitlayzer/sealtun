package cmd

import (
	"testing"

	"github.com/labring/sealtun/pkg/mesh"
)

func TestParseKubernetesServiceTarget(t *testing.T) {
	namespace, service, port, err := parseKubernetesServiceTarget("prod/api:8080")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	if namespace != "prod" || service != "api" || port != 8080 {
		t.Fatalf("unexpected target: %s/%s:%d", namespace, service, port)
	}

	namespace, service, port, err = parseKubernetesServiceTarget("redis:6379")
	if err != nil {
		t.Fatalf("parse default namespace target: %v", err)
	}
	if namespace != "default" || service != "redis" || port != 6379 {
		t.Fatalf("unexpected default namespace target: %s/%s:%d", namespace, service, port)
	}
}

func TestParseKubernetesServiceTargetRejectsInvalidPort(t *testing.T) {
	if _, _, _, err := parseKubernetesServiceTarget("api:70000"); err == nil {
		t.Fatal("expected invalid port to be rejected")
	}
}

func TestResolveImportRegionsAllExcludesSource(t *testing.T) {
	config := mesh.NewConfig("global", "gzg", "token")
	for _, name := range []string{"gzg", "hzh", "bja"} {
		if err := config.UpsertRegion(mesh.Region{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	regions, err := resolveImportRegions(&config, "all", "gzg")
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 || regions[0] != "bja" || regions[1] != "hzh" {
		t.Fatalf("unexpected import regions: %#v", regions)
	}
}

func TestResolveImportRegionsRejectsEmptyExplicitSelection(t *testing.T) {
	config := mesh.NewConfig("global", "gzg", "token")
	if err := config.UpsertRegion(mesh.Region{Name: "gzg"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveImportRegions(&config, "gzg", "gzg"); err == nil {
		t.Fatal("expected source-only explicit imports to be rejected")
	}
}
