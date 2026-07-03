package clusterconnect

import (
	"net"
	"testing"
	"time"
)

func TestForwardedConnCloseStopsDrainGoroutine(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	conn := &forwardedConn{
		Conn: client,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	errc := make(chan error, 1)
	drained := make(chan struct{})
	go func() {
		conn.closeWhenForwarderStops(errc)
		close(drained)
	}()

	if err := conn.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	assertChannelClosed(t, conn.stop, "stop")
	assertChannelClosed(t, conn.done, "done")
	assertChannelClosedWithin(t, drained, "drain goroutine")

	select {
	case errc <- nil:
	default:
		t.Fatal("expected buffered forwarder error channel to accept the final ForwardPorts result")
	}
}

func TestForwardedConnForwarderStopClosesConnection(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	conn := &forwardedConn{
		Conn: client,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	errc := make(chan error, 1)
	drained := make(chan struct{})
	go func() {
		conn.closeWhenForwarderStops(errc)
		close(drained)
	}()

	errc <- nil
	assertChannelClosedWithin(t, drained, "drain goroutine")
	assertChannelClosed(t, conn.stop, "stop")
	assertChannelClosed(t, conn.done, "done")
}

func assertChannelClosed(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatalf("expected %s channel to be closed", name)
	}
}

func assertChannelClosedWithin(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s to close", name)
	}
}
