package mesh

import "testing"

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
