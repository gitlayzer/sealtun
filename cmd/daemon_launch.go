package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/session"
)

func ensureDaemonRunning() error {
	if daemonstate.Alive() {
		return nil
	}

	release, err := daemonstate.AcquireLaunchLock()
	if err != nil {
		// Another process is likely starting the daemon; wait for it to come up.
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if daemonstate.Alive() {
				return nil
			}
			time.Sleep(250 * time.Millisecond)
		}
		return fmt.Errorf("daemon launch is already in progress")
	}
	defer release()

	if daemonstate.Alive() {
		return nil
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	root, err := auth.GetSealosDir()
	if err != nil {
		return err
	}

	logPath := filepath.Join(root, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log file: %w", err)
	}

	cmd := exec.Command(executable, "daemon")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureDetachedProcess(cmd)

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}
	_ = logFile.Close()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if daemonstate.Alive() {
			_ = cmd.Process.Release()
			return nil
		}

		if !session.ProcessAlive(cmd.Process.Pid) {
			_ = cmd.Process.Release()
			return fmt.Errorf("daemon exited before publishing state")
		}
		time.Sleep(250 * time.Millisecond)
	}

	_ = cmd.Process.Release()
	return fmt.Errorf("daemon did not publish liveness within 8s")
}

func waitForDaemonSession(tunnelID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastState string
	var lastError string
	var connectedSince time.Time
	for time.Now().Before(deadline) {
		sess, err := session.Get(tunnelID)
		if err == nil {
			lastState = sess.ConnectionState
			lastError = sess.LastError
			if daemonstate.Alive() && session.RuntimeStatusWithOwner(*sess, true) == "active" {
				if connectedSince.IsZero() {
					connectedSince = time.Now()
				}
				if time.Since(connectedSince) >= daemonConnectionStability {
					return nil
				}
			} else {
				connectedSince = time.Time{}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastError != "" {
		return fmt.Errorf("daemon did not connect tunnel %s within %s (state=%s, last error: %s)", tunnelID, timeout, lastState, lastError)
	}
	if lastState != "" {
		return fmt.Errorf("daemon did not connect tunnel %s within %s (state=%s)", tunnelID, timeout, lastState)
	}
	return fmt.Errorf("daemon did not connect tunnel %s within %s", tunnelID, timeout)
}
