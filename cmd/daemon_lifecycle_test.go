package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	daemonstate "github.com/labring/sealtun/pkg/daemon"
)

func TestDaemonHeartbeatCleanupWaitsBeforeDeletingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const pid = 4242
	if err := daemonstate.SaveState(pid); err != nil {
		t.Fatalf("save daemon state: %v", err)
	}

	heartbeatStarted := make(chan struct{})
	releaseHeartbeat := make(chan struct{})
	stopAndWait := startDaemonHeartbeat(context.Background(), func(context.Context) {
		close(heartbeatStarted)
		<-releaseHeartbeat
		if err := daemonstate.TouchStateForPID(pid); err != nil {
			t.Errorf("touch daemon state: %v", err)
		}
	})
	<-heartbeatStarted

	cleanupDone := make(chan struct{})
	go func() {
		stopAndWait()
		if err := daemonstate.DeleteStateForPID(pid); err != nil {
			t.Errorf("delete daemon state: %v", err)
		}
		close(cleanupDone)
	}()

	select {
	case <-cleanupDone:
		t.Fatal("cleanup returned before the active heartbeat exited")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := daemonstate.LoadState(); err != nil {
		t.Fatalf("state must remain until heartbeat exits: %v", err)
	}

	close(releaseHeartbeat)
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish after heartbeat exited")
	}
	if _, err := daemonstate.LoadState(); !os.IsNotExist(err) {
		t.Fatalf("daemon state should be deleted after heartbeat join, got %v", err)
	}
}

func TestWaitForDaemonStartupReportsEarlyExitAndReapsChild(t *testing.T) {
	cmd := daemonLaunchHelperCommand(t, 0, 23)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	started := time.Now()
	err := waitForDaemonStartup(cmd, 2*time.Second, 10*time.Millisecond, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "exited before publishing state") {
		t.Fatalf("expected early-exit error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("early exit took too long to detect: %s", elapsed)
	}
	if err := cmd.Process.Signal(os.Interrupt); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("child should be reaped, signal returned %v", err)
	}
}

func TestWaitForDaemonStartupReturnsWithoutWaitingForLiveChildAndReapsLater(t *testing.T) {
	cmd := daemonLaunchHelperCommand(t, 500*time.Millisecond, 0)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	var checks atomic.Int32
	started := time.Now()
	err := waitForDaemonStartup(cmd, 2*time.Second, 10*time.Millisecond, func() bool {
		return checks.Add(1) >= 1
	})
	if err != nil {
		t.Fatalf("waitForDaemonStartup returned error: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 250*time.Millisecond {
		t.Fatalf("successful launch waited for child exit: %s", elapsed)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := cmd.Process.Signal(os.Interrupt)
		if errors.Is(err, os.ErrProcessDone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child was not reaped after exit, last signal error: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func daemonLaunchHelperCommand(t *testing.T, delay time.Duration, exitCode int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonLaunchHelperProcess$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_DAEMON_LAUNCH_HELPER=1",
		"DAEMON_LAUNCH_HELPER_DELAY="+delay.String(),
		"DAEMON_LAUNCH_HELPER_EXIT="+strconv.Itoa(exitCode),
	)
	return cmd
}

func TestDaemonLaunchHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DAEMON_LAUNCH_HELPER") != "1" {
		return
	}
	delay, err := time.ParseDuration(os.Getenv("DAEMON_LAUNCH_HELPER_DELAY"))
	if err != nil {
		os.Exit(98)
	}
	time.Sleep(delay)
	exitCode, err := strconv.Atoi(os.Getenv("DAEMON_LAUNCH_HELPER_EXIT"))
	if err != nil {
		os.Exit(99)
	}
	os.Exit(exitCode)
}
