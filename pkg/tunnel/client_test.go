package tunnel

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
)

func TestUnavailableResponse(t *testing.T) {
	t.Parallel()

	response := unavailableResponse("http://localhost:3000")

	if !strings.HasPrefix(response, "HTTP/1.1 502 Bad Gateway\r\n") {
		t.Fatalf("unexpected status line: %q", response)
	}
	if !strings.Contains(response, "Content-Type: text/html; charset=utf-8\r\n") {
		t.Fatal("missing content type header")
	}
	if !strings.Contains(response, "Sealtun Tunnel Status") {
		t.Fatal("missing sealtun status shell")
	}
	if !strings.Contains(response, "localhost:3000") {
		t.Fatal("response should show expected local target")
	}
	if !strings.Contains(response, "Refresh this page after the target is ready.") {
		t.Fatal("response should explain recovery step")
	}
	if !strings.Contains(response, "http://localhost:3000") {
		t.Fatal("response should mention the target")
	}
}

func TestDialTargetHTTPSRequiresValidCertificateByDefault(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	target, err := ParseTarget(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialTarget(target)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected self-signed upstream certificate to fail by default")
	}
}

func TestDialTargetHTTPSAllowsExplicitInsecureSkipVerify(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	target, err := ParseTargetWithOptions(upstream.URL, TargetOptions{TLSInsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dialTarget(target)
	if err != nil {
		t.Fatalf("expected explicit insecure target TLS mode to connect: %v", err)
	}
	defer conn.Close()

	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "ok") {
		t.Fatalf("unexpected upstream response: %q", payload)
	}
}

func TestDialTargetHTTPSTimesOutStalledHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stop := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		<-stop
	}()

	target, err := ParseTargetWithOptions("https://"+listener.Addr().String(), TargetOptions{TLSInsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	conn, err := dialTargetWithTimeout(target, 30*time.Millisecond)
	if conn != nil {
		_ = conn.Close()
	}
	close(stop)
	<-serverDone
	if err == nil {
		t.Fatal("expected stalled TLS handshake to time out")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("stalled TLS handshake took %s, want bounded timeout", elapsed)
	}
}

func TestRawTCPLocalForwardingDoesNotWriteHTTPFallback(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		handleLocalForwarding(server, "1", tunnelprotocol.TCP)
		close(done)
	}()

	buffer := make([]byte, 1)
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	n, err := client.Read(buffer)
	_ = client.Close()
	<-done

	if n != 0 {
		t.Fatalf("raw TCP fallback wrote %d bytes", n)
	}
	if err == nil {
		t.Fatal("expected raw TCP fallback to close without HTTP response bytes")
	}
}

func TestRawTCPLocalForwardingKeepsResponseAfterClientHalfClose(t *testing.T) {
	t.Parallel()

	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer localListener.Close()

	localDone := make(chan struct{})
	go func() {
		defer close(localDone)
		conn, err := localListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("ready\n"))
		data, _ := io.ReadAll(conn)
		if len(data) > 0 {
			_, _ = conn.Write(append([]byte("echo:"), data...))
		}
	}()

	_, port, err := net.SplitHostPort(localListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	streamListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer streamListener.Close()

	acceptc := make(chan net.Conn, 1)
	go func() {
		conn, err := streamListener.Accept()
		if err == nil {
			acceptc <- conn
		}
	}()
	client, err := net.Dial("tcp", streamListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-acceptc

	forwardDone := make(chan struct{})
	go func() {
		defer close(forwardDone)
		handleLocalForwarding(server, port, tunnelprotocol.TCP)
	}()

	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if tcp, ok := client.(*net.TCPConn); ok {
		if err := tcp.CloseWrite(); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatal("expected tcp client connection")
	}

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, "ready\n") || !strings.Contains(text, "echo:ping") {
		t.Fatalf("expected ready banner and echo response, got %q", text)
	}

	<-forwardDone
	<-localDone
}
