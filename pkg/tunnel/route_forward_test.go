package tunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labring/sealtun/pkg/routes"
)

// TestRoutedForwardingThroughTunnel drives the full chain — relay server,
// yamux stream, local client, two local apps — and verifies that path
// prefixes dispatch to the right local port, unmatched paths fall back to the
// primary target, request bodies survive, and WebSocket upgrades work on
// routed paths.
func TestRoutedForwardingThroughTunnel(t *testing.T) {
	apiUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write([]byte("api:" + r.URL.Path + ":" + string(body)))
	})
	apiMux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/new")
		w.WriteHeader(http.StatusFound)
	})
	apiMux.HandleFunc("/selfprefix", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/api/already")
		w.WriteHeader(http.StatusFound)
	})
	apiMux.HandleFunc("/protorel", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "//cdn.example/x")
		w.WriteHeader(http.StatusFound)
	})
	apiMux.HandleFunc("/absurl", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://external.example/x")
		w.WriteHeader(http.StatusFound)
	})
	apiMux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := apiUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, append([]byte("api-echo:"), msg...)); err != nil {
				return
			}
		}
	})
	apiApp := httptest.NewServer(apiMux)
	t.Cleanup(apiApp.Close)
	apiPort := mustPort(t, apiApp.URL)
	apiPortNum, err := strconv.Atoi(apiPort)
	if err != nil {
		t.Fatal(err)
	}

	primaryMux := http.NewServeMux()
	primaryMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("primary:" + r.URL.Path))
	})
	primaryApp := httptest.NewServer(primaryMux)
	t.Cleanup(primaryApp.Close)
	primaryPort := mustPort(t, primaryApp.URL)

	relay := NewServerWithOptions("secret", 0, "https", primaryPort, ServerOptions{})
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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connected := make(chan struct{})
	go func() {
		_ = DialServerAndServeTargetWithOptions(ctx, "ws://"+listener.Addr().String()+"/_sealtun/ws", "secret", primaryPort, "", "https", TargetOptions{
			Routes: []routes.Route{{Path: "/api", Port: apiPortNum}},
		}, func() { close(connected) })
	}()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("tunnel client did not connect")
	}

	base := "http://" + listener.Addr().String()
	httpClient := &http.Client{Timeout: 3 * time.Second}

	get := func(path string) string {
		t.Helper()
		var body string
		var lastStatus int
		var lastBody string
		deadline := time.Now().Add(3 * time.Second)
		for {
			resp, err := httpClient.Get(base + path)
			if err == nil {
				data, _ := io.ReadAll(resp.Body)
				lastStatus = resp.StatusCode
				lastBody = string(data)
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					body = lastBody
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s failed: err=%v status=%d body=%.200q", path, err, lastStatus, lastBody)
			}
			time.Sleep(50 * time.Millisecond)
		}
		return body
	}

	cases := map[string]string{
		"/":          "primary:/",
		"/other":     "primary:/other",
		"/apiserver": "primary:/apiserver", // segment boundary: must not hit the api app
		"/api":       "api:/:",
		"/api/users": "api:/users:",
	}
	for path, want := range cases {
		if got := get(path); got != want {
			t.Fatalf("GET %s = %q, want %q", path, got, want)
		}
	}

	// Redirect locations must be re-prefixed so the browser stays on the
	// routed service; already-prefixed, protocol-relative, and absolute URLs
	// pass through untouched.
	redirectCases := map[string]string{
		"/api/old":        "/api/new",
		"/api/selfprefix": "/api/already",
		"/api/protorel":   "//cdn.example/x",
		"/api/absurl":     "https://external.example/x",
	}
	noFollow := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for path, wantLocation := range redirectCases {
		resp, err := noFollow.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		gotLocation := resp.Header.Get("Location")
		_ = resp.Body.Close()
		if gotLocation != wantLocation {
			t.Fatalf("GET %s Location = %q, want %q", path, gotLocation, wantLocation)
		}
	}

	// Request bodies must reach the routed app.
	resp, err := httpClient.Post(base+"/api/submit", "text/plain", strings.NewReader("payload-123"))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(data) != "api:/submit:payload-123" {
		t.Fatalf("POST body mangled: %q", data)
	}

	// WebSocket upgrade on a routed path.
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	ws, hsResp, err := dialer.Dial("ws://"+listener.Addr().String()+"/api/ws", nil)
	if err != nil {
		var hsStatus string
		var hsBody []byte
		if hsResp != nil {
			hsStatus = hsResp.Status
			hsBody, _ = io.ReadAll(hsResp.Body)
			_ = hsResp.Body.Close()
		}
		t.Fatalf("routed WebSocket dial failed: %v status=%q body=%.300q", err, hsStatus, hsBody)
	}
	_ = ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := ws.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("routed WebSocket write failed: %v", err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil || string(msg) != "api-echo:hello" {
		t.Fatalf("routed WebSocket echo = %q, %v", msg, err)
	}
	_ = ws.Close()
}

func mustPort(t *testing.T, rawURL string) string {
	t.Helper()
	idx := strings.LastIndex(rawURL, ":")
	if idx < 0 {
		t.Fatalf("cannot extract port from %q", rawURL)
	}
	return rawURL[idx+1:]
}
