package mesh

import (
	"fmt"
	"strings"
	"testing"
)

func TestConfigRoutesForRegion(t *testing.T) {
	config := NewConfig("Global", "gzg", "token")
	if err := config.UpsertRegion(Region{Name: "gzg", GatewayHost: "mesh-gzg.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertRegion(Region{Name: "hzh", GatewayHost: "mesh-hzh.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertService(Service{
		Name:      "API",
		Protocol:  "https",
		From:      "hzh",
		Namespace: "Default",
		Service:   "backend",
		Port:      8080,
		Imports:   []string{"gzg", "gzg"},
	}); err != nil {
		t.Fatal(err)
	}

	routes := config.RoutesForRegion("gzg")
	if len(routes) != 1 {
		t.Fatalf("expected one route, got %d", len(routes))
	}
	route := routes[0]
	if route.Name != "api" || route.Protocol != ProtocolHTTP {
		t.Fatalf("unexpected route identity: %#v", route)
	}
	if route.RemoteGatewayURL != "https://mesh-hzh.example.com" {
		t.Fatalf("unexpected remote gateway: %s", route.RemoteGatewayURL)
	}
	if route.ListenPort != ImportPort("api") {
		t.Fatalf("unexpected listen port: %d", route.ListenPort)
	}

	sourceRoutes := config.RoutesForRegion("hzh")
	if len(sourceRoutes) != 1 {
		t.Fatalf("expected one source route, got %d", len(sourceRoutes))
	}
	if sourceRoutes[0].RemoteGatewayURL != "" {
		t.Fatalf("source route must not point to a remote gateway: %#v", sourceRoutes[0])
	}
	if sourceRoutes[0].TargetService != "backend" {
		t.Fatalf("unexpected source target service: %#v", sourceRoutes[0])
	}
}

func TestValidateRouteRejectsBadInputs(t *testing.T) {
	err := ValidateRoute(GatewayRoute{
		Name:            "api",
		Protocol:        "udp",
		ListenPort:      8080,
		TargetRegion:    "hzh",
		TargetNamespace: "default",
		TargetService:   "api",
		TargetPort:      80,
	})
	if err == nil {
		t.Fatal("expected unsupported protocol to be rejected")
	}
}

func TestConfigRejectsImportPortCollisions(t *testing.T) {
	first, second := collidingServiceNames(t)
	config := NewConfig("global", "gzg", "token")
	if err := config.UpsertRegion(Region{Name: "gzg"}); err != nil {
		t.Fatal(err)
	}
	service := func(name string) Service {
		return Service{Name: name, Protocol: ProtocolHTTP, From: "gzg", Namespace: "default", Service: "backend", Port: 8080}
	}
	if err := config.UpsertService(service(first)); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertService(service(second)); err == nil || !strings.Contains(err.Error(), "listen port") {
		t.Fatalf("expected colliding service ports to be rejected, got %v", err)
	}
}

func collidingServiceNames(t *testing.T) (string, string) {
	t.Helper()
	seen := map[int32]string{}
	for i := 0; i < 100000; i++ {
		name := fmt.Sprintf("service-%d", i)
		port := ImportPort(name)
		if first := seen[port]; first != "" {
			return first, name
		}
		seen[port] = name
	}
	t.Fatal("failed to find an import port collision")
	return "", ""
}
