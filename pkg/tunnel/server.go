package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type Server struct {
	secret    string
	port      int
	protocol  string
	localPort string

	mu            sync.RWMutex
	activeSession *yamux.Session
	upgrader      websocket.Upgrader
	reverseProxy  *httputil.ReverseProxy
	connectedAt   atomic.Int64
}

func NewServer(secret string, port int, protocol string, localPort string) *Server {
	s := &Server{
		secret:    secret,
		port:      port,
		protocol:  protocol,
		localPort: localPort,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = "tunnel-target"
	}

	s.reverseProxy = &httputil.ReverseProxy{
		Director:  director,
		Transport: s.reverseProxyTransport(),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			WriteUnavailablePage(w, s.localPort, fmt.Sprintf("The remote ingress is reachable, but the local Sealtun client is not connected to this tunnel yet: %v", err))
		},
	}

	return s
}

func (s *Server) reverseProxyTransport() http.RoundTripper {
	return &http.Transport{
		DialContext:           s.proxyDialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func (s *Server) proxyDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	s.mu.RLock()
	session := s.activeSession
	s.mu.RUnlock()

	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("local client is not connected")
	}

	return session.OpenStream()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/_sealtun/healthz" {
		s.handleHealthz(w)
		return
	}

	// 1. Check if it's the internal tunnel negotiation endpoint
	if r.URL.Path == "/_sealtun/ws" {
		s.handleTunnelConnection(w, r)
		return
	}

	// 2. All other requests are public traffic -> Forward to Local Client via Reverse Proxy
	s.reverseProxy.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter) {
	s.mu.RLock()
	connected := s.activeSession != nil && !s.activeSession.IsClosed()
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if !connected {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"ok":false,"clientConnected":false,"protocol":%q}`, s.protocol)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"ok":true,"clientConnected":true,"protocol":%q,"connectedAt":%q}`, s.protocol, time.Unix(s.connectedAt.Load(), 0).Format(time.RFC3339))
}

func (s *Server) handleTunnelConnection(w http.ResponseWriter, r *http.Request) {
	// Authenticate
	authHeader := r.Header.Get("Authorization")
	expectedAuth := fmt.Sprintf("Bearer %s", s.secret)

	if authHeader != expectedAuth {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Upgrade
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("upgrade error: %v\n", err)
		return
	}
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	})
	stopPing := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					_ = conn.Close()
					return
				}
			case <-stopPing:
				return
			}
		}
	}()
	defer close(stopPing)

	netConn := NewWSConn(conn)

	// Since we OPEN streams to the client, we act as the Yamux Client!
	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.EnableKeepAlive = true
	yamuxConfig.KeepAliveInterval = 10 * time.Second

	session, err := yamux.Client(netConn, yamuxConfig)
	if err != nil {
		fmt.Printf("yamux client setup error: %v\n", err)
		netConn.Close()
		return
	}

	// Replace active session
	s.mu.Lock()
	if s.activeSession != nil && !s.activeSession.IsClosed() {
		s.activeSession.Close() // Disconnect old client to prevent leaks
	}
	s.activeSession = session
	s.connectedAt.Store(time.Now().Unix())
	s.mu.Unlock()

	fmt.Println("Local client connected successfully to the server pod.")

	// Wait for the session to close before exiting the handler
	<-session.CloseChan()
	fmt.Println("Local client disconnected.")
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	fmt.Printf("Server listening on %s (H2C enabled)\n", addr)

	h2s := &http2.Server{}
	server := &http.Server{
		Addr:              addr,
		Handler:           h2c.NewHandler(s, h2s),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return server.ListenAndServe()
}
