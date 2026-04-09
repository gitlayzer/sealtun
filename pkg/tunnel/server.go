package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type Server struct {
	secret string
	port   int

	mu             sync.RWMutex
	activeSession  *yamux.Session
	upgrader       websocket.Upgrader
	reverseProxy   *httputil.ReverseProxy
}

func NewServer(secret string, port int) *Server {
	s := &Server{
		secret: secret,
		port:   port,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}

	// Create reverse proxy
	director := func(req *http.Request) {
		// Target schema is irrelevant since we dial over yamux stream, but httputil requires it
		req.URL.Scheme = "http" 
		req.URL.Host = "tunnel-target"
	}

	transport := &http.Transport{
		DialContext: s.proxyDialContext,
		// Performance tuning
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	s.reverseProxy = &httputil.ReverseProxy{
		Director:  director,
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "Tunnel is currently unavailable or disconnected: %v", err)
		},
	}

	return s
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
	// 1. Check if it's the internal tunnel negotiation endpoint
	if r.URL.Path == "/_sealtun/ws" {
		s.handleTunnelConnection(w, r)
		return
	}

	// 2. All other requests are public traffic -> Forward to Local Client via Reverse Proxy
	s.reverseProxy.ServeHTTP(w, r)
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
	return http.ListenAndServe(addr, h2c.NewHandler(s, h2s))
}
