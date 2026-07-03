package clusterconnect

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type PodDialer interface {
	DialPod(ctx context.Context, namespace, podName string, port int32) (net.Conn, error)
}

type PortForwardDialer struct {
	RESTConfig *rest.Config
	Clientset  kubernetes.Interface
}

func (d *PortForwardDialer) DialPod(ctx context.Context, namespace, podName string, port int32) (net.Conn, error) {
	if d == nil || d.RESTConfig == nil || d.Clientset == nil {
		return nil, fmt.Errorf("pod port-forward dialer is not initialized")
	}
	if namespace == "" || podName == "" || port <= 0 {
		return nil, fmt.Errorf("namespace, pod name, and port are required")
	}
	transport, upgrader, err := spdy.RoundTripperFor(d.RESTConfig)
	if err != nil {
		return nil, err
	}
	serverURL := &url.URL{
		Scheme: "https",
		Path:   fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName),
		Host:   stringsTrimScheme(d.RESTConfig.Host),
	}
	if parsed, parseErr := url.Parse(d.RESTConfig.Host); parseErr == nil && parsed.Scheme != "" {
		serverURL.Scheme = parsed.Scheme
		serverURL.Host = parsed.Host
	}
	spdyDialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, serverURL)

	stopChan := make(chan struct{})
	readyChan := make(chan struct{})
	// ForwardPorts returns exactly once; keep this buffered so Close can stop
	// the forwarder without waiting for its final return path to be observed.
	errChan := make(chan error, 1)
	var fw *portforward.PortForwarder
	go func() {
		var err error
		fw, err = portforward.NewOnAddresses(spdyDialer, []string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", port)}, stopChan, readyChan, io.Discard, io.Discard)
		if err != nil {
			errChan <- err
			return
		}
		errChan <- fw.ForwardPorts()
	}()

	select {
	case <-readyChan:
	case err := <-errChan:
		close(stopChan)
		return nil, err
	case <-ctx.Done():
		close(stopChan)
		return nil, ctx.Err()
	}
	if fw == nil {
		close(stopChan)
		return nil, fmt.Errorf("pod port-forward did not initialize")
	}
	ports, err := fw.GetPorts()
	if err != nil {
		close(stopChan)
		return nil, err
	}
	if len(ports) == 0 || ports[0].Local == 0 {
		close(stopChan)
		return nil, fmt.Errorf("pod port-forward did not report a local port")
	}
	localPort := int(ports[0].Local)

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)))
	if err != nil {
		close(stopChan)
		return nil, err
	}
	forwarded := &forwardedConn{Conn: conn, stop: stopChan, done: make(chan struct{})}
	go forwarded.closeWhenForwarderStops(errChan)
	return forwarded, nil
}

type forwardedConn struct {
	net.Conn
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	closeErr error
}

func (c *forwardedConn) Close() error {
	c.once.Do(func() {
		close(c.stop)
		c.closeErr = c.Conn.Close()
		close(c.done)
	})
	return c.closeErr
}

func (c *forwardedConn) closeWhenForwarderStops(errc <-chan error) {
	select {
	case <-errc:
		_ = c.Close()
	case <-c.done:
	}
}

func stringsTrimScheme(host string) string {
	if u, err := url.Parse(host); err == nil && u.Host != "" {
		return u.Host
	}
	return host
}
