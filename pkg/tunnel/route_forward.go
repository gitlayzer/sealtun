package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"sync"
	"time"

	"github.com/labring/sealtun/pkg/routes"
)

// handleRoutedForwarding serves HTTP over a single tunnel stream and forwards
// each request to the local port matched by path prefix, falling back to the
// tunnel's primary target. Running a full http.Server on the stream (instead
// of hand-parsing the request line) keeps keep-alive, request bodies, and
// WebSocket upgrades working through the standard library's reverse proxy.
func handleRoutedForwarding(stream net.Conn, target Target) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == target.Address {
				// Primary upstream fallback; may need TLS per target config.
				return dialTarget(target)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	defer transport.CloseIdleConnections()

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			if route, ok := routes.MatchRoute(target.Routes, req.URL.Path); ok {
				// The local service owns its root: forward "/api/users" via
				// route "/api" as "/users". RawPath is dropped so it cannot
				// contradict the rewritten Path.
				req.URL.Path = routes.StripPrefix(route.Path, req.URL.Path)
				req.URL.RawPath = ""
				req.URL.Host = net.JoinHostPort("localhost", strconv.Itoa(route.Port))
			} else {
				req.URL.Host = target.Address
			}
			req.Host = req.URL.Host
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			WriteUnavailablePage(w, r.URL.Host, fmt.Sprintf("The local service for this path is not reachable yet: %v", err))
		},
	}
	server := &http.Server{
		Handler: proxy,
		// Headers must arrive promptly; IdleTimeout recycles keep-alive
		// streams as a backstop to the relay's own idle connection timeout.
		// Read/WriteTimeout stay unset on purpose: they would kill
		// long-lived hijacked WebSocket connections.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	_ = server.Serve(newSingleConnListener(stream))
}

var errSingleConnExhausted = errors.New("single-conn listener exhausted")

// singleConnListener adapts one already-established stream to net.Listener so
// http.Serve can drive it. The first Accept returns the stream; later calls
// block until the server finishes serving that connection, so http.Serve only
// returns once the stream is truly done — returning earlier would let the
// caller's deferred stream.Close kill in-flight requests.
type singleConnListener struct {
	conn net.Conn
	done chan struct{}
	once sync.Once
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{conn: conn, done: make(chan struct{})}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = &closeNotifyConn{Conn: l.conn, onClose: func() { close(l.done) }}
	})
	if conn == nil {
		<-l.done
		return nil, errSingleConnExhausted
	}
	return conn, nil
}

func (l *singleConnListener) Close() error { return nil }

func (l *singleConnListener) Addr() net.Addr {
	if l.conn.LocalAddr() != nil {
		return l.conn.LocalAddr()
	}
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}

// closeNotifyConn signals when http.Server closes the connection after its
// serve loop ends (peer close, protocol error, or hijacked connection done).
type closeNotifyConn struct {
	net.Conn
	onClose func()
	once    sync.Once
}

func (c *closeNotifyConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.onClose)
	return err
}
