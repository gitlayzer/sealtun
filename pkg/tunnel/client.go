package tunnel

import (
	"context"
	"fmt"
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
		conn.Close()
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
		
		io.WriteString(stream, "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/html\r\nConnection: close\r\n\r\n" +
			"<html><head><title>502 Bad Gateway - Sealtun</title><style>" +
			"body { font-family: -apple-system, system-ui, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; background: #f9fafb; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; color: #1f2937; }" +
			".card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1); max-width: 400px; width: 100%; border-top: 4px solid #ef4444; }" +
			"h1 { font-size: 1.5rem; margin-top: 0; color: #111827; }" +
			"p { line-height: 1.5; font-size: 0.95rem; color: #4b5563; }" +
			".hint { background: #fee2e2; padding: 0.75rem; border-radius: 6px; font-size: 0.85rem; color: #991b1b; margin-top: 1rem; border-left: 3px solid #f87171; }" +
			"</style></head><body><div class='card'>" +
			"<h1>👋 Local service is down</h1>" +
			"p>Sealtun has established the tunnel correctly, but it cannot connect to your local application.</p>" +
			"<div class='hint'><strong>Action required:</strong> Make sure your application is running on port <strong>" + localPort + "</strong> and matching the protocol.</div>" +
			"</div></body></html>")
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
