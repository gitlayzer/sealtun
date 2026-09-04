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

var ensureDaemonRunningFn = ensureDaemonRunning

func ensureDaemonRunning() error {
	if daemonstate.Alive() {
		if !daemonstate.StaleExecutable() {
			return nil
		}
		// The daemon outlives CLI upgrades but its struct may predate session
		// fields written by the newer CLI; restart it rather than let it
		// silently strip those fields on its next state write.
		if err := daemonstate.Stop(5 * time.Second); err != nil {
			return fmt.Errorf("restart daemon running an older binary: %w", err)
		}
	}

	release, err := daemonstate.AcquireLaunchLock()
	if err != nil {
		// Another process is likely starting the daemon; wait for it to come up.
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			if daemonstate.Alive() {
				return nil
			}
			select {
			case <-timer.C:
				return fmt.Errorf("daemon launch is already in progress")
			case <-ticker.C:
			}
		}
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
	logFile, err := openDaemonLogFile(logPath)
	if err != nil {
		return fmt.Errorf("open daemon log file: %w", err)
	}

	cmd := exec.Command(executable, "daemon") // #nosec G204 -- executable is the current Sealtun binary and the daemon argument is fixed.
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureDetachedProcess(cmd)

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}
	_ = logFile.Close()
	return waitForDaemonStartup(cmd, 8*time.Second, 250*time.Millisecond, daemonstate.Alive)
}

func waitForDaemonStartup(cmd *exec.Cmd, timeout, pollInterval time.Duration, alive func() bool) error {
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- cmd.Wait()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if alive() {
			return nil
		}
		select {
		case err := <-waitResult:
			if alive() {
				return nil
			}
			if err != nil {
				return fmt.Errorf("daemon exited before publishing state: %w", err)
			}
			return fmt.Errorf("daemon exited before publishing state")
		case <-timer.C:
			return fmt.Errorf("daemon did not publish liveness within %s", timeout)
		case <-ticker.C:
		}
	}
}

func openDaemonLogFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("daemon log %s is not a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- daemon log path is fixed under the user-owned Sealtun config directory and Lstat-validated before opening.
}

func waitForDaemonSession(tunnelID string, timeout time.Duration) error {
	return waitForDaemonSessionAfter(tunnelID, timeout, time.Time{})
}

func waitForDaemonSessionAfter(tunnelID string, timeout time.Duration, after time.Time) error {
	var lastState string
	var lastError string
	var lastConnectedAt string
	var connectedSince time.Time
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		sess, err := session.Get(tunnelID)
		if err == nil {
			lastState = sess.ConnectionState
			lastError = sess.LastError
			lastConnectedAt = sess.LastConnectedAt
			if daemonstate.Alive() && session.RuntimeStatusWithOwner(*sess, true) == "active" {
				if !after.IsZero() {
					connectedAt, err := time.Parse(time.RFC3339, sess.LastConnectedAt)
					if err != nil || !connectedAt.After(after) {
						connectedSince = time.Time{}
						goto wait
					}
				}
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
	wait:
		select {
		case <-timer.C:
			if lastError != "" {
				return fmt.Errorf("daemon did not connect tunnel %s within %s (state=%s, last error: %s)", tunnelID, timeout, lastState, lastError)
			}
			if !after.IsZero() && lastConnectedAt != "" {
				return fmt.Errorf("daemon did not reconnect tunnel %s within %s (state=%s, last connected at: %s)", tunnelID, timeout, lastState, lastConnectedAt)
			}
			if lastState != "" {
				return fmt.Errorf("daemon did not connect tunnel %s within %s (state=%s)", tunnelID, timeout, lastState)
			}
			return fmt.Errorf("daemon did not connect tunnel %s within %s", tunnelID, timeout)
		case <-ticker.C:
		}
	}
}
