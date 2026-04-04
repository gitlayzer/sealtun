package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
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

	netConn := NewWSConn(conn)

	// Since the Remote Server will OPEN streams to send traffic to us, 
	// the Local Client must act as the Yamux Server to ACCEPT those streams.
	session, err := yamux.Server(netConn, yamux.DefaultConfig())
	if err != nil {
		return fmt.Errorf("failed to mount yamux server: %w", err)
	}
	defer session.Close()

	fmt.Printf("Tunnel established! Forwarding to localhost:%s\n", localPort)

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if err == io.EOF || err == yamux.ErrSessionShutdown {
				// normal shutdown
				return nil
			}
			return fmt.Errorf("accept stream error: %w", err)
		}

		go handleLocalForwarding(stream, localPort)
	}
}

func handleLocalForwarding(stream net.Conn, localPort string) {
	defer stream.Close()

	localAddr := fmt.Sprintf("localhost:%s", localPort)
	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		fmt.Printf("failed to dial local service %s: %v\n", localAddr, err)
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
