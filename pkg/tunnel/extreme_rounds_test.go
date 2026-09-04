package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labring/sealtun/pkg/routes"
)

// extremeRig wires a relay server, a local client, and two real local apps
// with a /api route, mirroring the production data path in-process.
type extremeRig struct {
	base    string
	cancel  context.CancelFunc
	cleanup func()
}

func newExtremeRig(t *testing.T, apiHandler http.Handler, routePort int) *extremeRig {
	t.Helper()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("primary:" + r.URL.Path))
	}))
	primaryPort := mustPort(t, primary.URL)

	relay := NewServerWithOptions("secret", 0, "https", primaryPort, ServerOptions{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := newTunnelHTTPServer(relay)
	go func() { _ = httpServer.Serve(listener) }()

	ctx, cancel := context.WithCancel(context.Background())
	connected := make(chan struct{})
	go func() {
		_ = DialServerAndServeTargetWithOptions(ctx, "ws://"+listener.Addr().String()+"/_sealtun/ws", "secret", primaryPort, "", "https", TargetOptions{
			Routes: []routes.Route{{Path: "/api", Port: routePort}},
		}, func() { close(connected) })
	}()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not connect")
	}
	rig := &extremeRig{base: "http://" + listener.Addr().String(), cancel: cancel}
	rig.cleanup = func() {
		cancel()
		_ = httpServer.Close()
		primary.Close()
	}
	return rig
}

func startFixedApp(t *testing.T, listener net.Listener, handler http.Handler) *http.Server {
	t.Helper()
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return server
}

func TestR2ConcurrentMixedPathsAndAppFailure(t *testing.T) {
	apiListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	apiPort := mustAtoiExtreme(t, apiListener)
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api:" + r.URL.Path))
	})
	apiServer := startFixedApp(t, apiListener, apiHandler)

	rig := newExtremeRig(t, apiHandler, apiPort)
	defer rig.cleanup()

	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) (int, string) {
		resp, err := client.Get(rig.base + path)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	// 50 concurrent requests across routed and fallback paths.
	var wg sync.WaitGroup
	failures := make(chan string, 100)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path, want := "/", "primary:/"
			if i%2 == 0 {
				path, want = fmt.Sprintf("/api/n%d", i), fmt.Sprintf("api:/n%d", i)
			}
			status, body := get(path)
			if status != http.StatusOK || body != want {
				failures <- fmt.Sprintf("%s -> %d %q, want %q", path, status, body, want)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Fatal(f)
	}

	// Kill the routed app: routed paths degrade, the primary target is unaffected.
	_ = apiServer.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, body := get("/api/gone")
		if status >= 500 && strings.Contains(body, "not reachable") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected unavailable page for dead routed app, got %d %.100q", status, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status, body := get("/"); status != http.StatusOK || body != "primary:/" {
		t.Fatalf("primary target must survive routed app failure, got %d %q", status, body)
	}

	// Revive on the same port: routed traffic recovers without touching the tunnel.
	revivedListener, err := net.Listen("tcp", apiListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	startFixedApp(t, revivedListener, apiHandler)
	deadline = time.Now().Add(3 * time.Second)
	for {
		status, body := get("/api/back")
		if status == http.StatusOK && body == "api:/back" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("routed app did not recover, got %d %.100q", status, body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestR6WebSocketSubprotocolsLargeFramesAndUnmatched(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin:  func(*http.Request) bool { return true },
		Subprotocols: []string{"chat"},
	}
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	})
	apiMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api:http"))
	})
	apiApp := httptest.NewServer(apiMux)
	defer apiApp.Close()

	rig := newExtremeRig(t, nil, mustAtoiExtreme(t, apiApp.Listener))
	defer rig.cleanup()
	wsBase := "ws://" + strings.TrimPrefix(rig.base, "http://")

	// Subprotocol negotiation must survive both proxies.
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second, Subprotocols: []string{"chat"}}
	conn, resp, err := dialer.Dial(wsBase+"/api/ws", nil)
	if err != nil {
		t.Fatalf("WS dial failed: %v", err)
	}
	if resp.Header.Get("Sec-Websocket-Protocol") != "chat" {
		t.Fatalf("subprotocol lost: %q", resp.Header.Get("Sec-Websocket-Protocol"))
	}

	// 1 MiB message round trip.
	big := make([]byte, 1<<20)
	for i := range big {
		big[i] = byte(i % 251)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteMessage(websocket.BinaryMessage, big); err != nil {
		t.Fatalf("write 1MiB: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, echo, err := conn.ReadMessage()
	if err != nil || len(echo) != len(big) {
		t.Fatalf("1MiB echo failed: %v len=%d", err, len(echo))
	}

	// Control frames: ping must get a pong through the chain.
	pong := make(chan string, 1)
	conn.SetPongHandler(func(data string) error {
		pong <- data
		return nil
	})
	if err := conn.WriteControl(websocket.PingMessage, []byte("pingdata"), time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("ping: %v", err)
	}
	go func() { _, _, _ = conn.ReadMessage() }()
	select {
	case data := <-pong:
		if data != "pingdata" {
			t.Fatalf("pong data = %q", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no pong through the tunnel")
	}
	_ = conn.Close()

	// WS dial to an UNMATCHED path goes to the primary target, which here has
	// no WS endpoint: the failure must be an ordinary bad handshake, not a
	// tunnel-level error.
	_, resp, err = dialer.Dial(wsBase+"/nowhere/ws", nil)
	if err == nil {
		t.Fatal("WS to unmatched path should fail against the primary HTTP app")
	}
	if resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("unmatched path must not be upgraded")
	}
}

func mustAtoiExtreme(t *testing.T, listener net.Listener) int {
	t.Helper()
	addr := listener.Addr().String()
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		t.Fatalf("bad addr %q", addr)
	}
	var port int
	if _, err := fmt.Sscanf(addr[idx+1:], "%d", &port); err != nil {
		t.Fatal(err)
	}
	return port
}
