package tunnel

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// DialServerAndServe connects to the tunnel Server and serves local requests
func DialServerAndServe(ctx context.Context, wsURL, secret, localPort string) error {
	return dialServerAndServe(ctx, wsURL, secret, localPort, nil)
}

// DialServerAndServeWithOnConnected invokes onConnected after the tunnel handshake succeeds.
func DialServerAndServeWithOnConnected(ctx context.Context, wsURL, secret, localPort string, onConnected func()) error {
	return dialServerAndServe(ctx, wsURL, secret, localPort, onConnected)
}

func dialServerAndServe(ctx context.Context, wsURL, secret, localPort string, onConnected func()) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	headers := http.Header{}
	headers.Add("Authorization", fmt.Sprintf("Bearer %s", secret))

	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return fmt.Errorf("failed to dial tunnel server %s: %w", wsURL, err)
	}
	defer conn.Close()

	// Intercept context cancellation to close TCP connection eagerly
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	netConn := NewWSConn(conn)

	// Since the Remote Server will OPEN streams to send traffic to us,
	// the Local Client must act as the Yamux Server to ACCEPT those streams.
	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.EnableKeepAlive = true
	yamuxConfig.KeepAliveInterval = 10 * time.Second

	session, err := yamux.Server(netConn, yamuxConfig)
	if err != nil {
		return fmt.Errorf("failed to mount yamux server: %w", err)
	}
	defer session.Close()

	if onConnected != nil {
		onConnected()
	}

	fmt.Printf("Tunnel established! Forwarding to localhost:%s\n", localPort)

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if err == io.EOF || err == yamux.ErrSessionShutdown || ctx.Err() != nil {
				return nil
			}
			// Catch aggressive closed network errors triggered right at Ctrl+C
			if strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}
			return fmt.Errorf("accept stream error: %w", err)
		}

		go handleLocalForwarding(stream, localPort)
	}
}

var lastWarning time.Time
var warningMu sync.Mutex

func handleLocalForwarding(stream net.Conn, localPort string) {
	defer stream.Close()

	localAddr := fmt.Sprintf("localhost:%s", localPort)
	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		warningMu.Lock()
		if time.Since(lastWarning) > 2*time.Second {
			fmt.Printf("🚦 Hint: Request received, but local service %s is not running yet. Please start it.\n", localAddr)
			lastWarning = time.Now()
		}
		warningMu.Unlock()

		_, _ = io.WriteString(stream, unavailableResponse(localPort))
		return
	}
	defer localConn.Close()

	errc := make(chan error, 2)

	go func() {
		_, err := io.Copy(localConn, stream)
		errc <- err
	}()

	go func() {
		_, err := io.Copy(stream, localConn)
		errc <- err
	}()

	<-errc
}

func unavailableResponse(localPort string) string {
	body := unavailableHTML(localPort, "Your public tunnel is online, but the local app is not listening yet.", "Sealtun has received this request successfully. The remote ingress and tunnel server are working, but the client machine is not serving traffic on the configured local port.")

	return fmt.Sprintf(
		"HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(body),
		body,
	)
}

// WriteUnavailablePage renders the public fallback page when the server cannot reach the local client.
func WriteUnavailablePage(w http.ResponseWriter, localPort string, detail string) {
	body := unavailableHTML(localPort, "Your public tunnel is online, but the local client is not connected yet.", detail)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = io.WriteString(w, body)
}

func unavailableHTML(localPort string, heading string, detail string) string {
	port := html.EscapeString(localPort)
	if port == "" {
		port = "unknown"
	}
	heading = html.EscapeString(heading)
	detail = html.EscapeString(detail)

	return "<html><head><title>502 Bad Gateway - Sealtun</title><style>" +
		"body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: radial-gradient(circle at top, #15325b 0%, #08111f 55%, #030712 100%); display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; color: #e5eefb; padding: 24px; box-sizing: border-box; }" +
		".shell { width: 100%; max-width: 760px; background: rgba(9, 17, 31, 0.88); border: 1px solid rgba(148, 163, 184, 0.18); border-radius: 24px; box-shadow: 0 30px 80px rgba(0,0,0,0.45); overflow: hidden; }" +
		".topbar { display: flex; align-items: center; gap: 10px; padding: 16px 20px; background: rgba(15, 23, 42, 0.95); border-bottom: 1px solid rgba(148, 163, 184, 0.14); }" +
		".dot { width: 10px; height: 10px; border-radius: 999px; background: #fb7185; box-shadow: 22px 0 0 #fbbf24, 44px 0 0 #34d399; margin-right: 44px; }" +
		".brand { font-size: 13px; letter-spacing: 0.14em; text-transform: uppercase; color: #93c5fd; }" +
		".content { padding: 32px; display: grid; gap: 20px; }" +
		".badge { display: inline-flex; width: fit-content; padding: 6px 10px; border-radius: 999px; background: rgba(248, 113, 113, 0.14); color: #fca5a5; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; }" +
		"h1 { font-size: 34px; line-height: 1.1; margin: 0; color: #f8fafc; }" +
		"p { margin: 0; line-height: 1.7; color: #cbd5e1; font-size: 16px; }" +
		".panel { display: grid; gap: 12px; background: rgba(15, 23, 42, 0.86); border: 1px solid rgba(96, 165, 250, 0.18); border-radius: 18px; padding: 18px; }" +
		".label { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: #7dd3fc; }" +
		".value { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 18px; color: #f8fafc; }" +
		".list { margin: 0; padding-left: 18px; color: #cbd5e1; }" +
		".list li { margin: 6px 0; }" +
		"</style></head><body><div class='shell'><div class='topbar'><div class='dot'></div><div class='brand'>Sealtun Tunnel Status</div></div><div class='content'>" +
		"<div class='badge'>Local Port Offline</div>" +
		"<h1>" + heading + "</h1>" +
		"<p>" + detail + "</p>" +
		"<div class='panel'><div class='label'>Expected local target</div><div class='value'>localhost:" + port + "</div></div>" +
		"<div class='panel'><div class='label'>What to do next</div><ul class='list'>" +
		"<li>Start your local application on port <strong>" + port + "</strong>.</li>" +
		"<li>Keep the <code>sealtun expose " + port + "</code> process running.</li>" +
		"<li>Refresh this page after the local service is ready.</li>" +
		"</ul></div>" +
		"</div></div></body></html>"
}
