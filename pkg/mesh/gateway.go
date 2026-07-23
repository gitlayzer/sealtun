package mesh

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labring/sealtun/pkg/tunnel"
)

type GatewayOptions struct {
	Listen string
	Token  string
	Routes []GatewayRoute
}

type GatewayStatus struct {
	OK     bool           `json:"ok"`
	Routes []GatewayRoute `json:"routes,omitempty"`
}

const gatewayTokenHeader = "X-Sealtun-Mesh-Token" // #nosec G101 -- header name, not a credential value.

var gatewayWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
}

func RunGateway(ctx context.Context, opts GatewayOptions) error {
	if strings.TrimSpace(opts.Listen) == "" {
		opts.Listen = ":" + strconv.Itoa(int(DefaultGatewayPort))
	}
	if strings.TrimSpace(opts.Token) == "" {
		return fmt.Errorf("mesh gateway token is required")
	}
	if err := ValidateGatewayRoutes(opts.Routes); err != nil {
		return err
	}

	mux := http.NewServeMux()
	gateway := &gatewayServer{
		token:       opts.Token,
		routes:      routesByName(opts.Routes),
		dialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	mux.HandleFunc("/_sealtun/mesh/healthz", gateway.handleHealthz)
	mux.HandleFunc("/_sealtun/mesh/proxy/", gateway.handleProxy)
	mux.HandleFunc("/_sealtun/mesh/tcp/", gateway.handleTCP)

	errc := make(chan error, len(opts.Routes)+1)
	servers := make([]*http.Server, 0, len(opts.Routes)+1)
	listeners := make([]net.Listener, 0, len(opts.Routes)+1)
	closeAll := func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
		for _, server := range servers {
			_ = server.Close()
		}
	}
	defer closeAll()

	startHTTPServer := func(addr string, handler http.Handler) error {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		server := &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
		listeners = append(listeners, listener)
		servers = append(servers, server)
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				errc <- err
			}
		}()
		return nil
	}
	if err := startHTTPServer(opts.Listen, mux); err != nil {
		return fmt.Errorf("listen for mesh management traffic on %s: %w", opts.Listen, err)
	}

	for _, route := range opts.Routes {
		route := route
		if strings.TrimSpace(route.RemoteGatewayURL) == "" {
			continue
		}
		switch route.Protocol {
		case ProtocolHTTP:
			addr := ":" + strconv.Itoa(int(route.ListenPort))
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHTTPToRemote(w, r, route, opts.Token) })
			if err := startHTTPServer(addr, handler); err != nil {
				return fmt.Errorf("listen for mesh route %s on %s: %w", route.Name, addr, err)
			}
		case ProtocolTCP:
			ln, err := net.Listen("tcp", ":"+strconv.Itoa(int(route.ListenPort)))
			if err != nil {
				return fmt.Errorf("listen for mesh route %s on port %d: %w", route.Name, route.ListenPort, err)
			}
			listeners = append(listeners, ln)
			go acceptTCPRoute(ctx, ln, route, opts.Token, errc)
		}
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, server := range servers {
			_ = server.Shutdown(shutdownCtx)
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		return nil
	case err := <-errc:
		return err
	}
}

func ValidateRoute(route GatewayRoute) error {
	if err := ValidateName("route name", NormalizeName(route.Name)); err != nil {
		return err
	}
	if err := ValidateProtocol(route.Protocol); err != nil {
		return err
	}
	if route.ListenPort < 1 || route.ListenPort > 65535 {
		return fmt.Errorf("invalid route %s listen port %d", route.Name, route.ListenPort)
	}
	if err := ValidateName("target region", NormalizeName(route.TargetRegion)); err != nil {
		return err
	}
	if err := ValidateName("target namespace", NormalizeName(route.TargetNamespace)); err != nil {
		return err
	}
	if err := ValidateName("target service", NormalizeName(route.TargetService)); err != nil {
		return err
	}
	if route.TargetPort < 1 || route.TargetPort > 65535 {
		return fmt.Errorf("invalid route %s target port %d", route.Name, route.TargetPort)
	}
	if strings.TrimSpace(route.RemoteGatewayURL) != "" {
		u, err := url.Parse(route.RemoteGatewayURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid route %s remote gateway URL %q", route.Name, route.RemoteGatewayURL)
		}
	}
	return nil
}

func validateGatewayRoutes(routes []GatewayRoute) error {
	names := make(map[string]struct{}, len(routes))
	listenPorts := make(map[int32]string, len(routes))
	for _, route := range routes {
		if err := ValidateRoute(route); err != nil {
			return err
		}
		name := NormalizeName(route.Name)
		if _, exists := names[name]; exists {
			return fmt.Errorf("duplicate mesh route name %q", name)
		}
		names[name] = struct{}{}

		if strings.TrimSpace(route.RemoteGatewayURL) == "" {
			continue
		}
		if existing, exists := listenPorts[route.ListenPort]; exists {
			return fmt.Errorf("mesh routes %q and %q use the same listen port %d", existing, name, route.ListenPort)
		}
		listenPorts[route.ListenPort] = name
	}
	return nil
}

func ValidateGatewayRoutes(routes []GatewayRoute) error {
	return validateGatewayRoutes(routes)
}

type gatewayServer struct {
	token       string
	routes      map[string]GatewayRoute
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func routesByName(routes []GatewayRoute) map[string]GatewayRoute {
	out := make(map[string]GatewayRoute, len(routes))
	for _, route := range routes {
		out[NormalizeName(route.Name)] = route
	}
	return out
}

func (g *gatewayServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GatewayStatus{OK: true})
}

func (g *gatewayServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !g.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name, rest := splitRoutePath(strings.TrimPrefix(r.URL.Path, "/_sealtun/mesh/proxy/"))
	route, ok := g.routes[NormalizeName(name)]
	if !ok {
		http.Error(w, "mesh route not found", http.StatusNotFound)
		return
	}
	if route.Protocol != ProtocolHTTP {
		http.Error(w, "route is not http", http.StatusBadRequest)
		return
	}
	target := url.URL{
		Scheme:   "http",
		Host:     fmt.Sprintf("%s.%s.svc.cluster.local:%d", route.TargetService, route.TargetNamespace, route.TargetPort),
		Path:     rest,
		RawQuery: r.URL.RawQuery,
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = cloneTargetHeaders(r.Header, g.token)
	req.Host = target.Host
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (g *gatewayServer) handleTCP(w http.ResponseWriter, r *http.Request) {
	if !g.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name, _ := splitRoutePath(strings.TrimPrefix(r.URL.Path, "/_sealtun/mesh/tcp/"))
	route, ok := g.routes[NormalizeName(name)]
	if !ok {
		http.Error(w, "mesh route not found", http.StatusNotFound)
		return
	}
	if route.Protocol != ProtocolTCP {
		http.Error(w, "route is not tcp", http.StatusBadRequest)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	dialContext := g.dialContext
	if dialContext == nil {
		dialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	targetConn, err := dialContext(
		r.Context(),
		"tcp",
		fmt.Sprintf("%s.%s.svc.cluster.local:%d", route.TargetService, route.TargetNamespace, route.TargetPort),
	)
	if err != nil {
		http.Error(w, "mesh target unavailable", http.StatusBadGateway)
		return
	}
	defer targetConn.Close()
	ws, err := gatewayWebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	stream := tunnel.NewWSConn(ws)
	relay(r.Context(), targetConn, stream)
}

func (g *gatewayServer) authorized(r *http.Request) bool {
	if constantTimeTokenEqual(r.Header.Get(gatewayTokenHeader), g.token) {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return constantTimeTokenEqual(strings.TrimPrefix(header, prefix), g.token)
}

func constantTimeTokenEqual(token, want string) bool {
	token = strings.TrimSpace(token)
	want = strings.TrimSpace(want)
	if token == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

func cloneTargetHeaders(header http.Header, gatewayToken string) http.Header {
	out := header.Clone()
	removeHopByHopHeaders(out)
	out.Del(gatewayTokenHeader)
	const prefix = "Bearer "
	auth := out.Get("Authorization")
	if strings.HasPrefix(auth, prefix) && constantTimeTokenEqual(strings.TrimPrefix(auth, prefix), gatewayToken) {
		out.Del("Authorization")
	}
	return out
}

var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopByHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				header.Del(name)
			}
		}
	}
	for _, name := range hopByHopHeaders {
		header.Del(name)
	}
}

func copyResponseHeaders(dst, src http.Header) {
	headers := src.Clone()
	removeHopByHopHeaders(headers)
	for key, values := range headers {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func splitRoutePath(value string) (string, string) {
	value = strings.TrimLeft(value, "/")
	if value == "" {
		return "", "/"
	}
	name, rest, ok := strings.Cut(value, "/")
	if !ok {
		return name, "/"
	}
	return name, "/" + rest
}

func proxyHTTPToRemote(w http.ResponseWriter, r *http.Request, route GatewayRoute, token string) {
	u, err := remoteProxyURL(route, r.URL.RequestURI())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	removeHopByHopHeaders(req.Header)
	req.Header.Set(gatewayTokenHeader, token)
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func remoteProxyURL(route GatewayRoute, requestURI string) (string, error) {
	base, err := url.Parse(route.RemoteGatewayURL)
	if err != nil {
		return "", err
	}
	base.Path = "/_sealtun/mesh/proxy/" + route.Name
	if requestURI != "" && requestURI != "/" {
		path, rawQuery, _ := strings.Cut(requestURI, "?")
		base.Path += "/" + strings.TrimLeft(path, "/")
		base.RawQuery = rawQuery
	}
	return base.String(), nil
}

func acceptTCPRoute(ctx context.Context, ln net.Listener, route GatewayRoute, token string, errc chan<- error) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			errc <- err
			return
		}
		go func() {
			defer conn.Close()
			if err := proxyTCPToRemote(ctx, conn, route, token); err != nil {
				return
			}
		}()
	}
}

func proxyTCPToRemote(ctx context.Context, conn net.Conn, route GatewayRoute, token string) error {
	u, err := url.Parse(route.RemoteGatewayURL)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return fmt.Errorf("unsupported remote gateway scheme %q", u.Scheme)
	}
	u.Path = "/_sealtun/mesh/tcp/" + route.Name
	header := http.Header{}
	header.Set(gatewayTokenHeader, token)
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return err
	}
	defer ws.Close()
	relay(ctx, conn, tunnel.NewWSConn(ws))
	return nil
}

func relay(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	stopWatcher := make(chan struct{})
	watcherDone := make(chan struct{})
	var closeOnce sync.Once
	closeConnections := func() {
		closeOnce.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			closeConnections()
		case <-stopWatcher:
		}
	}()
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
	closeConnections()
	<-done
	close(stopWatcher)
	<-watcherDone
}
