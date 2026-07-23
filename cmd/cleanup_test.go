package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

func TestCleanupAllRejectsSpecificTunnelID(t *testing.T) {
	oldCleanupAll := cleanupAll
	cleanupAll = true
	t.Cleanup(func() { cleanupAll = oldCleanupAll })

	cmd := *cleanupCmd
	err := cmd.RunE(&cmd, []string{"abc123"})
	if err == nil || !strings.Contains(err.Error(), "--all cannot be used with a specific tunnel id") {
		t.Fatalf("expected cleanup conflict error, got %v", err)
	}
}

func TestCleanupSpecificTunnelAllowsErrorSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	previousCleanup := cleanupSessionResources
	cleaned := 0
	cleanupSessionResources = func(context.Context, session.TunnelSession) error {
		cleaned++
		return nil
	}
	t.Cleanup(func() { cleanupSessionResources = previousCleanup })

	if err := session.Save(session.TunnelSession{
		TunnelID:        "errtun",
		ConnectionState: session.ConnectionStateError,
		UpdatedAt:       time.Now().Format(time.RFC3339),
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	cmd := *cleanupCmd
	cmd.SetContext(context.Background())
	if err := cmd.RunE(&cmd, []string{"errtun"}); err != nil {
		t.Fatalf("expected error session cleanup to be allowed, got %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("expected one cleanup call, got %d", cleaned)
	}
	if _, err := session.Get("errtun"); err == nil {
		t.Fatal("expected local error session to be deleted")
	}
}

func TestCleanupSpecificTunnelRejectsActiveSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "activecleanup",
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		ConnectionState: session.ConnectionStateConnected,
		UpdatedAt:       time.Now().Format(time.RFC3339),
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	cmd := *cleanupCmd
	cmd.SetContext(context.Background())
	err := cmd.RunE(&cmd, []string{"activecleanup"})
	if err == nil || !strings.Contains(err.Error(), "refusing cleanup") {
		t.Fatalf("expected active cleanup refusal, got %v", err)
	}
}

func TestCleanupWaitsForTunnelOperationLock(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:        "cleanlocked",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	releaseLock := holdTunnelOperationLock(t, "cleanlocked")
	defer releaseLock()
	previousCleanup := cleanupSessionResources
	cleanupCalled := make(chan struct{}, 1)
	want := fmt.Errorf("stop after lock")
	cleanupSessionResources = func(context.Context, session.TunnelSession) error {
		cleanupCalled <- struct{}{}
		return want
	}
	t.Cleanup(func() { cleanupSessionResources = previousCleanup })

	done := make(chan error, 1)
	go func() {
		cmd := *cleanupCmd
		cmd.SetContext(context.Background())
		done <- cmd.RunE(&cmd, []string{"cleanlocked"})
	}()
	assertOperationBlocked(t, cleanupCalled)
	releaseLock()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("cleanup error = %v, want %v", err, want)
	}
}
