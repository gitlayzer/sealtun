package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

func TestValidateLocalPort(t *testing.T) {
	t.Parallel()

	validPorts := []string{"1", "8080", "65535"}
	for _, port := range validPorts {
		if err := validateLocalPort(port); err != nil {
			t.Fatalf("expected port %s to be valid, got error: %v", port, err)
		}
	}

	invalidPorts := []string{"0", "65536", "-1", "abc"}
	for _, port := range invalidPorts {
		if err := validateLocalPort(port); err == nil {
			t.Fatalf("expected port %s to be invalid", port)
		}
	}
}

func TestValidateProtocol(t *testing.T) {
	t.Parallel()

	validProtocols := []string{"https", "HTTPS", "ssh", "SSH", "tcp", "TCP"}
	for _, protocol := range validProtocols {
		if err := validateProtocol(protocol); err != nil {
			t.Fatalf("expected protocol %s to be valid, got error: %v", protocol, err)
		}
	}

	invalidProtocols := []string{"http", "grpc", "grpcs", "udp", "ws", "wss", ""}
	for _, protocol := range invalidProtocols {
		if err := validateProtocol(protocol); err == nil {
			t.Fatalf("expected %s to be rejected", protocol)
		}
	}
}

func TestNewTunnelIDUsesSixtyFourBitsOfEntropy(t *testing.T) {
	t.Parallel()

	id := newTunnelID()
	if len(id) != 16 {
		t.Fatalf("expected 16 hex chars, got %q", id)
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("expected lowercase hex tunnel id, got %q", id)
		}
	}
}

func TestResolveExposeTargetDefaultsLocalPort(t *testing.T) {
	t.Parallel()

	localPort, targetURL, err := resolveExposeTarget([]string{"3000"}, "")
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if localPort != "3000" || targetURL != "http://localhost:3000" {
		t.Fatalf("unexpected target: localPort=%s target=%s", localPort, targetURL)
	}
}

func TestResolveExposeTargetAcceptsRemoteHTTPUpstream(t *testing.T) {
	t.Parallel()

	localPort, targetURL, err := resolveExposeTarget(nil, "http://10.0.0.12:8080")
	if err != nil {
		t.Fatalf("resolve remote target: %v", err)
	}
	if localPort != "8080" || targetURL != "http://10.0.0.12:8080" {
		t.Fatalf("unexpected remote target: localPort=%s target=%s", localPort, targetURL)
	}
}

func TestResolveExposeTargetRejectsMismatchedPortAndTarget(t *testing.T) {
	t.Parallel()

	if _, _, err := resolveExposeTarget([]string{"3000"}, "http://10.0.0.12:8080"); err == nil {
		t.Fatal("expected mismatched positional port and target port to fail")
	}
}

func TestValidateTargetTLSOptionsRequiresHTTPSTarget(t *testing.T) {
	t.Parallel()

	if err := validateTargetTLSOptions("", true); err == nil {
		t.Fatal("expected missing target to fail")
	}
	if err := validateTargetTLSOptions("http://10.0.0.12:8080", true); err == nil {
		t.Fatal("expected http target to reject insecure TLS option")
	}
	if err := validateTargetTLSOptions("https://10.0.0.12:8443", true); err != nil {
		t.Fatalf("expected https target to accept insecure TLS option: %v", err)
	}
}

func TestDialForegroundTunnelWithRetry(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := dialForegroundTunnelWithRetry(context.Background(), time.Second, time.Millisecond, func(ctx context.Context, onConnected func()) error {
		attempts++
		if attempts < 3 {
			return errors.New("failed to dial tunnel server wss://example/_sealtun/ws: websocket: bad handshake")
		}
		onConnected()
		return nil
	})
	if err != nil {
		t.Fatalf("expected retry to connect, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestDialForegroundTunnelWithRetryStopsAfterConnectedSessionEnds(t *testing.T) {
	t.Parallel()

	err := dialForegroundTunnelWithRetry(context.Background(), time.Second, time.Millisecond, func(ctx context.Context, onConnected func()) error {
		onConnected()
		return errors.New("accept stream error: use of closed network connection")
	})
	if err == nil {
		t.Fatal("expected connected session error to be returned without retrying")
	}
}

func TestForegroundCleanupRequiresCurrentProcessOwnership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "foreown",
		Mode:            "foreground",
		PID:             os.Getpid(),
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save foreground session: %v", err)
	}
	if !foregroundSessionOwnedByCurrentProcess("foreown") {
		t.Fatal("expected current foreground process to own the session")
	}

	latest, err := session.Get("foreown")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	latest.Mode = "daemon"
	latest.PID = 0
	latest.ConnectionState = session.ConnectionStatePending
	if err := session.Update(*latest); err != nil {
		t.Fatalf("update daemon session: %v", err)
	}
	if foregroundSessionOwnedByCurrentProcess("foreown") {
		t.Fatal("foreground cleanup must not own a session that has moved to daemon mode")
	}
}

func TestRecoverStaleSessionsRechecksEligibilityAfterLock(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:        "recoverlocked",
		Namespace:       "ns-demo",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateError,
		ExpiresAt:       time.Now().Add(-time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	releaseLock := holdTunnelOperationLock(t, "recoverlocked")
	defer releaseLock()
	previousCleanup := cleanupSessionResources
	cleanupCalled := make(chan struct{}, 1)
	cleanupSessionResources = func(context.Context, session.TunnelSession) error {
		cleanupCalled <- struct{}{}
		return nil
	}
	t.Cleanup(func() { cleanupSessionResources = previousCleanup })

	done := make(chan error, 1)
	go func() {
		done <- recoverStaleSessions(context.Background(), &bytes.Buffer{})
	}()
	assertOperationBlocked(t, cleanupCalled)

	current, err := session.Get("recoverlocked")
	if err != nil {
		t.Fatal(err)
	}
	current.ExpiresAt = ""
	current.ConnectionState = session.ConnectionStateStopped
	if err := session.Update(*current); err != nil {
		t.Fatal(err)
	}
	releaseLock()
	if err := <-done; err != nil {
		t.Fatalf("recoverStaleSessions returned error: %v", err)
	}
	select {
	case <-cleanupCalled:
		t.Fatal("recovery cleaned a tunnel that became ineligible while waiting for its lock")
	default:
	}
}
