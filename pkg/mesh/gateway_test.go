package mesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestHandleTCPReturnsBadGatewayBeforeWebSocketUpgradeWhenTargetIsUnavailable(t *testing.T) {
	gateway := &gatewayServer{
		token: "secret",
		routes: routesByName([]GatewayRoute{{
			Name:            "database",
			Protocol:        ProtocolTCP,
			TargetService:   "database",
			TargetNamespace: "default",
			TargetPort:      5432,
		}}),
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("target unavailable")
		},
	}
	req := httptest.NewRequest(http.MethodGet, "http://gateway/_sealtun/mesh/tcp/database", nil)
	req.Header.Set(gatewayTokenHeader, "secret")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	recorder := httptest.NewRecorder()

	gateway.handleTCP(recorder, req)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusBadGateway, recorder.Body.String())
	}
}

func TestCloneTargetHeadersStripsMeshToken(t *testing.T) {
	header := http.Header{}
	header.Set(gatewayTokenHeader, "secret")
	header.Set("Authorization", "Bearer secret")

	out := cloneTargetHeaders(header, "secret")
	if out.Get(gatewayTokenHeader) != "" {
		t.Fatalf("gateway token header leaked to target: %#v", out)
	}
	if out.Get("Authorization") != "" {
		t.Fatalf("gateway authorization leaked to target: %#v", out)
	}
}

func TestCloneTargetHeadersPreservesApplicationAuthorization(t *testing.T) {
	header := http.Header{}
	header.Set(gatewayTokenHeader, "secret")
	header.Set("Authorization", "Bearer app-token")

	out := cloneTargetHeaders(header, "secret")
	if out.Get(gatewayTokenHeader) != "" {
		t.Fatalf("gateway token header leaked to target: %#v", out)
	}
	if out.Get("Authorization") != "Bearer app-token" {
		t.Fatalf("application authorization was not preserved: %#v", out)
	}
}

func TestCloneTargetHeadersStripsHopByHopHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("Connection", "keep-alive, X-Connection-Secret")
	header.Set("Keep-Alive", "timeout=5")
	header.Set("Proxy-Connection", "keep-alive")
	header.Set("Upgrade", "websocket")
	header.Set("X-Connection-Secret", "must-not-leak")
	header.Set("X-Application", "preserved")

	out := cloneTargetHeaders(header, "secret")
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Upgrade", "X-Connection-Secret"} {
		if got := out.Get(name); got != "" {
			t.Fatalf("hop-by-hop header %s leaked to target: %q", name, got)
		}
	}
	if got := out.Get("X-Application"); got != "preserved" {
		t.Fatalf("application header = %q, want preserved", got)
	}
}

func TestRelayStopsBothDirectionsWhenContextIsCanceled(t *testing.T) {
	leftClient, leftRelay := net.Pipe()
	rightRelay, rightClient := net.Pipe()
	defer leftClient.Close()
	defer rightClient.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay(ctx, leftRelay, rightRelay)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after context cancellation")
	}
	if _, err := io.WriteString(leftClient, "closed"); err == nil || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("left relay connection remained open: %v", err)
	}
}

func TestRunGatewayReleasesListenersWhenRouteStartupFails(t *testing.T) {
	firstPort := reserveTCPPort(t)
	occupied := listenOnRandomPort(t)
	defer occupied.Close()
	occupiedPort := occupied.Addr().(*net.TCPAddr).Port

	route := func(name, protocol string, port int) GatewayRoute {
		return GatewayRoute{
			Name:             name,
			Protocol:         protocol,
			ListenPort:       int32(port),
			TargetRegion:     "gzg",
			TargetNamespace:  "default",
			TargetService:    "backend",
			TargetPort:       8080,
			RemoteGatewayURL: "http://127.0.0.1:1",
		}
	}

	err := RunGateway(context.Background(), GatewayOptions{
		Listen: "127.0.0.1:0",
		Token:  "test-gateway-token",
		Routes: []GatewayRoute{
			route("first", ProtocolTCP, firstPort),
			route("occupied", ProtocolTCP, occupiedPort),
		},
	})
	if err == nil {
		t.Fatal("expected the occupied route port to fail gateway startup")
	}

	listener, listenErr := net.Listen("tcp", ":"+strconv.Itoa(firstPort))
	if listenErr != nil {
		t.Fatalf("first route listener leaked after startup failure: %v", listenErr)
	}
	_ = listener.Close()
}

func TestValidateGatewayRoutesRejectsDuplicateNamesAndPorts(t *testing.T) {
	route := func(name string, port int32) GatewayRoute {
		return GatewayRoute{
			Name:             name,
			Protocol:         ProtocolHTTP,
			ListenPort:       port,
			TargetRegion:     "gzg",
			TargetNamespace:  "default",
			TargetService:    "backend",
			TargetPort:       8080,
			RemoteGatewayURL: "https://mesh.example.test",
		}
	}

	for _, tt := range []struct {
		name   string
		routes []GatewayRoute
	}{
		{name: "duplicate normalized name", routes: []GatewayRoute{route("api", 18080), route("API", 18081)}},
		{name: "duplicate imported port", routes: []GatewayRoute{route("api", 18080), route("other", 18080)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateGatewayRoutes(tt.routes); err == nil {
				t.Fatal("expected duplicate gateway route configuration to be rejected")
			}
		})
	}
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener := listenOnRandomPort(t)
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved port %d: %v", port, err)
	}
	return port
}

func listenOnRandomPort(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(fmt.Errorf("reserve TCP port: %w", err))
	}
	return listener
}
