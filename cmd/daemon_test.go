package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

func TestDaemonTunnelFingerprintChangesForForwardingInputs(t *testing.T) {
	base := session.TunnelSession{
		TunnelID:   "abc123",
		SealosHost: "sealtun-abc123-default.sealosgzg.site",
		LocalPort:  "3000",
		Protocol:   "https",
		Secret:     "secret",
	}
	baseFingerprint := daemonTunnelFingerprint(base)

	tests := []struct {
		name string
		mut  func(*session.TunnelSession)
	}{
		{name: "control host", mut: func(sess *session.TunnelSession) { sess.SealosHost = "other.sealosgzg.site" }},
		{name: "local port", mut: func(sess *session.TunnelSession) { sess.LocalPort = "3001" }},
		{name: "protocol", mut: func(sess *session.TunnelSession) { sess.Protocol = "http" }},
		{name: "secret", mut: func(sess *session.TunnelSession) { sess.Secret = "new-secret" }},
		{name: "basic auth enabled", mut: func(sess *session.TunnelSession) {
			sess.BasicAuth = &session.BasicAuthConfig{
				Enabled:      true,
				Username:     "admin",
				PasswordHash: "hash",
			}
		}},
		{name: "access policy", mut: func(sess *session.TunnelSession) {
			sess.AccessPolicy = &session.AccessPolicy{
				BearerTokenHashes: []string{"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			}
		}},
		{name: "ttl", mut: func(sess *session.TunnelSession) { sess.TTL = "2h" }},
		{name: "expires at", mut: func(sess *session.TunnelSession) { sess.ExpiresAt = "2026-05-13T10:00:00Z" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.mut(&next)
			if got := daemonTunnelFingerprint(next); got == baseFingerprint {
				t.Fatalf("expected fingerprint to change for %s", tt.name)
			}
		})
	}
}

func TestWaitForDaemonSessionAfterRequiresFreshConnection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "abc123",
		Mode:            "daemon",
		PID:             os.Getpid(),
		ConnectionState: session.ConnectionStateConnected,
		LastConnectedAt: time.Now().Add(-time.Minute).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	err := waitForDaemonSessionAfter("abc123", 20*time.Millisecond, time.Now())
	if err == nil || !strings.Contains(err.Error(), "did not reconnect") {
		t.Fatalf("expected stale connection to be rejected, got %v", err)
	}
}

func TestOpenDaemonLogFileRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(home, "outside.log")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(home, "daemon.log")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	file, err := openDaemonLogFile(linked)
	if err == nil {
		_ = file.Close()
		t.Fatal("expected symlinked daemon log to be rejected")
	}
}

func TestStoppedSessionGuardPreservesStoppedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stopped123",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	latest, getErr := session.Get("stopped123")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if shouldPreserveStoppedSession(latest) {
		current, err := session.Get("stopped123")
		if err != nil {
			t.Fatal(err)
		}
		if current.ConnectionState != session.ConnectionStateStopped {
			t.Fatalf("expected stopped state to be preserved, got %s", current.ConnectionState)
		}
		return
	}

	latest.Mode = "daemon"
	latest.PID = os.Getpid()
	latest.ConnectionState = session.ConnectionStateConnected
	latest.LastError = ""
	latest.LastConnectedAt = time.Now().Format(time.RFC3339)
	if saveErr := session.Update(*latest); saveErr != nil {
		t.Fatal(saveErr)
	}
	t.Fatal("stopped session guard did not preserve stopped state")
}

func TestReconcileDaemonWorkersWaitsForCanceledWorkerBeforeReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managed := map[string]*managedTunnel{}
	var mu sync.Mutex
	var active atomic.Int32
	var maxActive atomic.Int32
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})

	run := func(ctx context.Context, sess session.TunnelSession) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		started <- sess.Secret
		if sess.Secret == "old" {
			<-ctx.Done()
			<-releaseFirst
			return
		}
		<-ctx.Done()
	}

	oldSession := session.TunnelSession{TunnelID: "abc123", Secret: "old"}
	newSession := session.TunnelSession{TunnelID: "abc123", Secret: "new"}
	reconcileDaemonWorkers(ctx, &mu, managed, map[string]session.TunnelSession{"abc123": oldSession}, run)
	if got := <-started; got != "old" {
		t.Fatalf("first worker secret = %q, want old", got)
	}

	reconcileDaemonWorkers(ctx, &mu, managed, map[string]session.TunnelSession{"abc123": newSession}, run)
	select {
	case got := <-started:
		t.Fatalf("replacement worker %q started before canceled worker exited", got)
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseFirst)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		_, exists := managed["abc123"]
		mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled worker did not exit")
		}
		time.Sleep(time.Millisecond)
	}

	reconcileDaemonWorkers(ctx, &mu, managed, map[string]session.TunnelSession{"abc123": newSession}, run)
	if got := <-started; got != "new" {
		t.Fatalf("replacement worker secret = %q, want new", got)
	}
	cancel()
	if maxActive.Load() != 1 {
		t.Fatalf("daemon workers overlapped: max active = %d", maxActive.Load())
	}
}
