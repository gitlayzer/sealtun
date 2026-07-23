package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithTunnelOperationLockRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEALTUN_HOME", home)
	configDir := filepath.Join(home, ".sealtun")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	target := filepath.Join(home, "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatalf("create target file: %v", err)
	}
	lockPath := filepath.Join(configDir, "tunnel-abc123.lock")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatalf("create lock symlink: %v", err)
	}

	called := false
	err := withTunnelOperationLock("abc123", func() error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if called {
		t.Fatal("operation callback ran for an unsafe lock path")
	}
}

func TestWithTunnelOperationLocksDeduplicatesTunnelIDs(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	called := 0
	err := withTunnelOperationLocks([]string{"second", "first", "second"}, func() error {
		called++
		return nil
	})
	if err != nil {
		t.Fatalf("withTunnelOperationLocks returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("callback called %d times, want 1", called)
	}
}

func TestWithTunnelOperationLocksWaitsForEveryTunnel(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	releaseLock := holdTunnelOperationLock(t, "second")
	defer releaseLock()

	called := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- withTunnelOperationLocks([]string{"second", "first"}, func() error {
			called <- struct{}{}
			return nil
		})
	}()
	assertOperationBlocked(t, called)
	releaseLock()
	if err := <-done; err != nil {
		t.Fatalf("withTunnelOperationLocks returned error: %v", err)
	}
}
