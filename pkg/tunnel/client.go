package tunnel

import (
	"context"
	"crypto/tls"
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
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
)

// DialServerAndServe connects to the tunnel Server and serves local requests
func DialServerAndServe(ctx context.Context, wsURL, secret, localPort string) error {
	return DialServerAndServeProtocol(ctx, wsURL, secret, localPort, tunnelprotocol.HTTPS, nil)
}

// DialServerAndServeWithOnConnected invokes onConnected after the tunnel handshake succeeds.
func DialServerAndServeWithOnConnected(ctx context.Context, wsURL, secret, localPort string, onConnected func()) error {
	return DialServerAndServeProtocol(ctx, wsURL, secret, localPort, tunnelprotocol.HTTPS, onConnected)
}

// DialServerAndServeProtocol connects to the tunnel Server and serves local
// requests using protocol-aware fallback behavior.
func DialServerAndServeProtocol(ctx context.Context, wsURL, secret, localPort, protocol string, onConnected func()) error {
	target, err := TargetFor(localPort, "")
	if err != nil {
		return err
	}
	return dialServerAndServe(ctx, wsURL, secret, target, protocol, onConnected)
}

func DialServerAndServeTarget(ctx context.Context, wsURL, secret, localPort, targetURL, protocol string, onConnected func()) error {
	return DialServerAndServeTargetWithOptions(ctx, wsURL, secret, localPort, targetURL, protocol, TargetOptions{}, onConnected)
}

func DialServerAndServeTargetWithOptions(ctx context.Context, wsURL, secret, localPort, targetURL, protocol string, targetOptions TargetOptions, onConnected func()) error {
	target, err := TargetForWithOptions(localPort, targetURL, targetOptions)
	if err != nil {
		return err
	}
	return dialServerAndServe(ctx, wsURL, secret, target, protocol, onConnected)
}

func dialServerAndServe(ctx context.Context, wsURL, secret string, target Target, protocol string, onConnected func()) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	headers := http.Header{}
	headers.Add("Authorization", fmt.Sprintf("Bearer %s", secret))

	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return fmt.Errorf("failed to dial tunnel server %s: %w", wsURL, err)
	}
	defer conn.Close()

	// Intercept context cancellation to close TCP connection eagerly
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
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

	fmt.Printf("Tunnel established! Forwarding to %s\n", target.URL)

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

		go handleTargetForwarding(stream, target, protocol)
	}
}

var lastWarning time.Time
var warningMu sync.Mutex

func handleLocalForwarding(stream net.Conn, localPort, protocol string) {
	target, err := LocalhostTarget(localPort)
	if err != nil {
		_ = stream.Close()
		return
	}
	handleTargetForwarding(stream, target, protocol)
}

func handleTargetForwarding(stream net.Conn, target Target, protocol string) {
	defer stream.Close()

	targetConn, err := dialTarget(target)
	if err != nil {
		warningMu.Lock()
		if time.Since(lastWarning) > 2*time.Second {
			fmt.Printf("Hint: Request received, but target %s is not reachable yet: %v\n", target.URL, err)
			lastWarning = time.Now()
		}
		warningMu.Unlock()

		if tunnelprotocol.IsHTTP(protocol) {
			_, _ = io.WriteString(stream, unavailableResponse(target.URL))
		}
		return
	}
	defer targetConn.Close()

	_ = relayBidirectional(targetConn, stream, nil)
}

func dialTarget(target Target) (net.Conn, error) {
	return dialTargetWithTimeout(target, 5*time.Second)
}

func dialTargetWithTimeout(target Target, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", target.Address, timeout)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(target.URL, "https://") {
		host, _, splitErr := net.SplitHostPort(target.Address)
		if splitErr != nil {
			_ = conn.Close()
			return nil, splitErr
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, InsecureSkipVerify: target.TLSInsecureSkipVerify}) // #nosec G402 -- only enabled by an explicit per-target user option for private upstreams.
		if err := tlsConn.SetDeadline(time.Now().Add(timeout)); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		if err := tlsConn.SetDeadline(time.Time{}); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	return conn, nil
}

func unavailableResponse(target string) string {
	body := unavailableHTML(target, "Your public tunnel is online, but the target is not reachable yet.", "Sealtun has received this request successfully. The remote ingress and tunnel server are working, but the configured target is not accepting traffic.")

	return fmt.Sprintf(
		"HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(body),
		body,
	)
}

// WriteUnavailablePage renders the public fallback page when the server cannot reach the local client.
func WriteUnavailablePage(w http.ResponseWriter, target string, detail string) {
	body := unavailableHTML(target, "Your public tunnel is online, but the local client is not connected yet.", detail)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = io.WriteString(w, body)
}

func unavailableHTML(target string, heading string, detail string) string {
	target = html.EscapeString(target)
	if target == "" {
		target = "unknown"
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
		"<div class='badge'>Target Offline</div>" +
		"<h1>" + heading + "</h1>" +
		"<p>" + detail + "</p>" +
		"<div class='panel'><div class='label'>Expected target</div><div class='value'>" + target + "</div></div>" +
		"<div class='panel'><div class='label'>What to do next</div><ul class='list'>" +
		"<li>Confirm the target is reachable from the machine running the Sealtun client.</li>" +
		"<li>Keep the <code>sealtun expose</code> process or daemon running.</li>" +
		"<li>Refresh this page after the target is ready.</li>" +
		"</ul></div>" +
		"</div></div></body></html>"
}
