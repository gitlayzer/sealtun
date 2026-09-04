package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labring/sealtun/pkg/accesspolicy"
)

// TestPublicWebSocketProxyRoundTrip drives a browser-style WebSocket through
// the complete tunnel chain — public listener, reverse proxy, yamux stream,
// local client, local app — and verifies bidirectional messages plus ordinary
// HTTP traffic on the same tunnel. This guards the public Upgrade path that
// Vite HMR and other dev servers depend on; a regression here turns every
// tunneled frontend preview into a silent half-broken page.
func TestPublicWebSocketProxyRoundTrip(t *testing.T) {
	// Local app behind the tunnel: HTTP health endpoint plus WebSocket echo.
	// CheckOrigin accepts everything because, as with any reverse proxy, the
	// public Origin never matches the local Host — apps behind the tunnel must
	// treat the tunnel as their trusted proxy.
	localUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ws-echo", func(w http.ResponseWriter, r *http.Request) {
		conn, err := localUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			messageType, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	})
	app := httptest.NewServer(mux)
	t.Cleanup(app.Close)
	appPort := strings.TrimPrefix(app.URL, "http://127.0.0.1:")

	// Relay server with the access-policy middleware stack active; middleware
	// must never break the Upgrade handshake.
	relay := NewServerWithOptions("secret", 0, "https", appPort, ServerOptions{
		AccessPolicy: &accesspolicy.Policy{
			RateLimit: "60/m",
			Audit:     &accesspolicy.AuditConfig{Enabled: true},
		},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := newTunnelHTTPServer(relay)
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	t.Cleanup(func() {
		_ = httpServer.Close()
		<-serveDone
	})

	// Local tunnel client connected through the data plane.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connected := make(chan struct{})
	go func() {
		_ = DialServerAndServeWithOnConnected(ctx, "ws://"+listener.Addr().String()+"/_sealtun/ws", "secret", appPort, func() {
			close(connected)
		})
	}()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("tunnel client did not connect to the relay server")
	}

	// Public WebSocket with a browser-style Origin header through the chain.
	// Retry briefly: the session handoff on the relay side is asynchronous.
	var ws *websocket.Conn
	deadline := time.Now().Add(3 * time.Second)
	for {
		dialer := websocket.Dialer{HandshakeTimeout: time.Second}
		header := http.Header{"Origin": []string{"http://" + listener.Addr().String()}}
		conn, _, dialErr := dialer.Dial("ws://"+listener.Addr().String()+"/ws-echo", header)
		if dialErr == nil {
			ws = conn
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("public WebSocket dial failed: %v", dialErr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	for i := 1; i <= 3; i++ {
		payload := fmt.Sprintf("ping-%d", i)
		_ = ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := ws.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatalf("write %s failed: %v", payload, err)
		}
		_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("read echo for %s failed: %v", payload, err)
		}
		if string(msg) != "echo:"+payload {
			t.Fatalf("unexpected echo: got %q, want %q", msg, "echo:"+payload)
		}
	}
	// Close explicitly: the proxy only returns (and audits the request) once
	// the hijacked connection ends.
	_ = ws.Close()

	// The hijacked upgrade must be audited as 101, not misreported as 200.
	var wsAuditStatus int
	auditDeadline := time.Now().Add(2 * time.Second)
	for {
		relay.auditMu.Lock()
		for i := range relay.auditEvents {
			if relay.auditEvents[i].Path == "/ws-echo" {
				wsAuditStatus = relay.auditEvents[i].Status
			}
		}
		relay.auditMu.Unlock()
		if wsAuditStatus != 0 || time.Now().After(auditDeadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if wsAuditStatus == 0 {
		t.Fatal("expected an audit event for the WebSocket upgrade")
	}
	if wsAuditStatus != http.StatusSwitchingProtocols {
		t.Fatalf("WebSocket upgrade audited with status %d, want %d", wsAuditStatus, http.StatusSwitchingProtocols)
	}

	// Ordinary HTTP traffic keeps flowing over the same tunnel.
	httpClient := &http.Client{Timeout: 3 * time.Second}
	resp, err := httpClient.Get("http://" + listener.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("HTTP health request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("unexpected health response: status=%d body=%q", resp.StatusCode, body)
	}
}
