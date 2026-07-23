package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

func TestStopMarksSessionStoppedBeforePausingResources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	previousPause := pauseSessionResources
	pauseCalled := false
	pauseSessionResources = func(_ context.Context, sess session.TunnelSession) error {
		pauseCalled = true
		latest, err := session.Get(sess.TunnelID)
		if err != nil {
			t.Fatalf("load session during pause: %v", err)
		}
		if latest.ConnectionState != session.ConnectionStateStopped {
			t.Fatalf("expected stopped state before remote pause, got %q", latest.ConnectionState)
		}
		if latest.PID != 0 {
			t.Fatalf("expected PID to be cleared before remote pause, got %d", latest.PID)
		}
		return nil
	}
	t.Cleanup(func() { pauseSessionResources = previousPause })

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stoptun",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-test",
		Protocol:        "tcp",
		Host:            "stoptun.example.com",
		LocalPort:       "18081",
		Mode:            "foreground",
		PID:             currentPIDForTest(),
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	cmd := *stopCmd
	cmd.SetContext(context.Background())
	if err := cmd.RunE(&cmd, []string{"stoptun"}); err != nil {
		t.Fatalf("stop command returned error: %v", err)
	}
	if !pauseCalled {
		t.Fatal("expected remote pause to be called")
	}
	latest, err := session.Get("stoptun")
	if err != nil {
		t.Fatalf("load final session: %v", err)
	}
	if latest.ConnectionState != session.ConnectionStateStopped {
		t.Fatalf("expected final stopped state, got %q", latest.ConnectionState)
	}
}

func TestStopRestoresSessionWhenRemotePauseFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	previousPause := pauseSessionResources
	pauseSessionResources = func(context.Context, session.TunnelSession) error {
		return fmt.Errorf("api unavailable")
	}
	t.Cleanup(func() { pauseSessionResources = previousPause })

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stopfail",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-test",
		Protocol:        "https",
		Host:            "stopfail.example.com",
		LocalPort:       "18082",
		Mode:            "foreground",
		PID:             currentPIDForTest(),
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	sess, err := findSession("stopfail")
	if err != nil {
		t.Fatal(err)
	}
	_, err = stopTunnelSession(context.Background(), sess)
	if err == nil || !strings.Contains(err.Error(), "api unavailable") {
		t.Fatalf("expected pause failure, got %v", err)
	}
	latest, err := session.Get("stopfail")
	if err != nil {
		t.Fatalf("load final session: %v", err)
	}
	if latest.ConnectionState != session.ConnectionStateConnected {
		t.Fatalf("expected original connected state to be restored, got %q", latest.ConnectionState)
	}
	if latest.PID == 0 {
		t.Fatal("expected original PID to be restored")
	}
	if !strings.Contains(latest.LastError, "api unavailable") {
		t.Fatalf("expected pause failure to be recorded, got %q", latest.LastError)
	}
}

func TestStartRollbackMarksErrorWhenPauseRollbackFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	previousResume := resumeSessionResources
	previousPause := pauseSessionResources
	previousEnsure := ensureDaemonRunningFn
	resumeSessionResources = func(context.Context, session.TunnelSession) error {
		return nil
	}
	pauseSessionResources = func(context.Context, session.TunnelSession) error {
		return fmt.Errorf("pause rollback failed")
	}
	ensureDaemonRunningFn = func() error {
		return fmt.Errorf("daemon unavailable")
	}
	t.Cleanup(func() {
		resumeSessionResources = previousResume
		pauseSessionResources = previousPause
		ensureDaemonRunningFn = previousEnsure
	})

	if err := session.Save(session.TunnelSession{
		TunnelID:        "startfail",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-test",
		Protocol:        "https",
		Host:            "startfail.example.com",
		LocalPort:       "18083",
		Secret:          "secret",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	sess, err := findSession("startfail")
	if err != nil {
		t.Fatal(err)
	}
	err = startTunnelSession(context.Background(), sess)
	if err == nil || !strings.Contains(err.Error(), "rollback pause failed") {
		t.Fatalf("expected rollback pause failure, got %v", err)
	}
	latest, err := session.Get("startfail")
	if err != nil {
		t.Fatalf("load final session: %v", err)
	}
	if latest.ConnectionState != session.ConnectionStateError {
		t.Fatalf("expected error state when rollback pause fails, got %q", latest.ConnectionState)
	}
	if !strings.Contains(latest.LastError, "daemon unavailable") {
		t.Fatalf("expected original start failure in LastError, got %q", latest.LastError)
	}
}

func TestStartRollsBackRemoteResourcesWhenLocalUpdateFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	previousResume := resumeSessionResources
	previousPause := pauseSessionResources
	previousUpdate := startSessionUpdate
	resumeCalled := false
	pauseCalled := false
	resumeSessionResources = func(context.Context, session.TunnelSession) error {
		resumeCalled = true
		return nil
	}
	pauseSessionResources = func(context.Context, session.TunnelSession) error {
		pauseCalled = true
		return nil
	}
	startSessionUpdate = func(session.TunnelSession) error {
		return fmt.Errorf("local disk unavailable")
	}
	t.Cleanup(func() {
		resumeSessionResources = previousResume
		pauseSessionResources = previousPause
		startSessionUpdate = previousUpdate
	})

	if err := session.Save(session.TunnelSession{
		TunnelID:        "startsavefail",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-test",
		Protocol:        "https",
		Host:            "startsavefail.example.com",
		LocalPort:       "18084",
		Secret:          "secret",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	sess, err := findSession("startsavefail")
	if err != nil {
		t.Fatal(err)
	}
	err = startTunnelSession(context.Background(), sess)
	if err == nil || !strings.Contains(err.Error(), "local disk unavailable") {
		t.Fatalf("expected local update failure, got %v", err)
	}
	if !resumeCalled {
		t.Fatal("expected remote resources to be resumed")
	}
	if !pauseCalled {
		t.Fatal("expected resumed remote resources to be rolled back")
	}
	latest, err := session.Get("startsavefail")
	if err != nil {
		t.Fatalf("load final session: %v", err)
	}
	if latest.ConnectionState != session.ConnectionStateStopped {
		t.Fatalf("expected stopped state after rollback, got %q", latest.ConnectionState)
	}
}

func TestStartWaitsForTunnelOperationLock(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:        "startlocked",
		Secret:          "secret",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	releaseLock := holdTunnelOperationLock(t, "startlocked")
	defer releaseLock()
	previousResume := resumeSessionResources
	resumeCalled := make(chan struct{}, 1)
	want := fmt.Errorf("stop after lock")
	resumeSessionResources = func(context.Context, session.TunnelSession) error {
		resumeCalled <- struct{}{}
		return want
	}
	t.Cleanup(func() { resumeSessionResources = previousResume })

	done := make(chan error, 1)
	go func() {
		cmd := *startCmd
		cmd.SetContext(context.Background())
		done <- cmd.RunE(&cmd, []string{"startlocked"})
	}()
	assertOperationBlocked(t, resumeCalled)
	releaseLock()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("start error = %v, want %v", err, want)
	}
}

func TestStopWaitsForTunnelOperationLock(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:        "stoplocked",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	releaseLock := holdTunnelOperationLock(t, "stoplocked")
	defer releaseLock()
	previousPause := pauseSessionResources
	pauseCalled := make(chan struct{}, 1)
	want := fmt.Errorf("stop after lock")
	pauseSessionResources = func(context.Context, session.TunnelSession) error {
		pauseCalled <- struct{}{}
		return want
	}
	t.Cleanup(func() { pauseSessionResources = previousPause })

	done := make(chan error, 1)
	go func() {
		cmd := *stopCmd
		cmd.SetContext(context.Background())
		done <- cmd.RunE(&cmd, []string{"stoplocked"})
	}()
	assertOperationBlocked(t, pauseCalled)
	releaseLock()
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("stop error = %v, want %v", err, want)
	}
}

func holdTunnelOperationLock(t *testing.T, tunnelID string) func() {
	t.Helper()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withTunnelOperationLock(tunnelID, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out acquiring test tunnel operation lock")
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
			if err := <-done; err != nil {
				t.Errorf("release test tunnel operation lock: %v", err)
			}
		})
	}
}

func assertOperationBlocked(t *testing.T, called <-chan struct{}) {
	t.Helper()
	select {
	case <-called:
		t.Fatal("tunnel mutation started while the operation lock was held")
	case <-time.After(100 * time.Millisecond):
	}
}
