package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/session"
)

const (
	stateFileName       = "daemon.json"
	lockFileName        = "daemon.lock"
	runtimeLockFileName = "daemon.runtime.lock"
	heartbeatMaxAge     = 10 * time.Second
)

type State struct {
	PID           int    `json:"pid"`
	PIDStartToken string `json:"pidStartToken,omitempty"`
	StartedAt     string `json:"startedAt"`
	UpdatedAt     string `json:"updatedAt"`
}

func statePath() (string, error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, stateFileName), nil
}

func existingStatePath() (string, error) {
	root, err := auth.CurrentSealtunDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, stateFileName), nil
}

func SaveState(pid int) error {
	path, err := statePath()
	if err != nil {
		return err
	}

	return writeState(path, State{
		PID:           pid,
		PIDStartToken: session.ProcessStartToken(pid),
		StartedAt:     time.Now().Format(time.RFC3339),
		UpdatedAt:     time.Now().Format(time.RFC3339),
	})
}

func TouchState() error {
	return TouchStateForPID(os.Getpid())
}

func TouchStateForPID(pid int) error {
	state, err := LoadState()
	if err != nil {
		return err
	}
	if state.PID != pid {
		return nil
	}
	state.UpdatedAt = time.Now().Format(time.RFC3339)

	path, err := statePath()
	if err != nil {
		return err
	}
	return writeState(path, *state)
}

func writeState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := fmt.Sprintf("%s.%d.%d.tmp", path, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func lockPath() (string, error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, lockFileName), nil
}

func AcquireLaunchLock() (func(), error) {
	path, err := lockPath()
	if err != nil {
		return nil, err
	}

	if info, err := regularDaemonFileInfo(path, "daemon launch lock"); err == nil {
		switch alive, ok := lockOwnerAlive(path); {
		case ok && alive:
			return nil, os.ErrExist
		case ok && !alive:
			_ = os.Remove(path)
		case time.Since(info.ModTime()) > 30*time.Second:
			_ = os.Remove(path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return createOwnedLock(path)
}

func runtimeLockPath() (string, error) {
	root, err := auth.GetSealosDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, runtimeLockFileName), nil
}

func AcquireRuntimeLock() (func(), error) {
	path, err := runtimeLockPath()
	if err != nil {
		return nil, err
	}

	if info, err := regularDaemonFileInfo(path, "daemon runtime lock"); err == nil {
		// Resolve the lock owner's liveness once, then decide. Calling
		// lockOwnerAlive twice could observe contradictory states if the owner
		// exits between the two reads.
		alive, ok := lockOwnerAlive(path)
		if ok && alive {
			return nil, os.ErrExist
		}
		if state, stateErr := LoadState(); stateErr == nil && stateProcessAlive(state) {
			return nil, os.ErrExist
		}
		switch {
		case ok && !alive:
			_ = os.Remove(path)
		case time.Since(info.ModTime()) > heartbeatMaxAge:
			_ = os.Remove(path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return createOwnedLock(path)
}

func createOwnedLock(path string) (func(), error) {
	token := fmt.Sprintf("%d:%s:%d", os.Getpid(), session.ProcessStartToken(os.Getpid()), time.Now().UnixNano())
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- lock path is fixed under the user-owned Sealtun config directory.
	if err != nil {
		return nil, err
	}
	if _, err := file.WriteString(token); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}

	return func() {
		data, err := readDaemonRegularFile(path, "daemon lock")
		if err == nil && string(data) == token {
			_ = os.Remove(path)
		}
	}, nil
}

func lockOwnerAlive(path string) (bool, bool) {
	data, err := readDaemonRegularFile(path, "daemon lock")
	if err != nil {
		return false, false
	}
	parts := strings.Split(string(data), ":")
	if len(parts) < 2 {
		return false, false
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		return false, false
	}
	if !session.ProcessAlive(pid) {
		return false, true
	}
	// Legacy locks contain pid:nonce and remain PID-only. New locks contain
	// pid:start-token:nonce, allowing Linux and Windows to reject PID reuse.
	if len(parts) >= 3 && parts[1] != "" {
		current := session.ProcessStartToken(pid)
		if current == "" || current != parts[1] {
			return false, true
		}
	}
	return true, true
}

func LoadState() (*State, error) {
	path, err := existingStatePath()
	if err != nil {
		return nil, err
	}

	data, err := readDaemonRegularFile(path, "daemon state")
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse daemon state: %w", err)
	}
	return &state, nil
}

func regularDaemonFileInfo(path, label string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", label, path)
	}
	return info, nil
}

func readDaemonRegularFile(path, label string) ([]byte, error) {
	if _, err := regularDaemonFileInfo(path, label); err != nil {
		return nil, err
	}
	return os.ReadFile(path) // #nosec G304 -- daemon files are fixed under the user-owned Sealtun config directory and are Lstat-validated before reading.
}

func DeleteState() error {
	path, err := existingStatePath()
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func DeleteStateForPID(pid int) error {
	state, err := LoadState()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.PID != pid {
		return nil
	}
	return DeleteState()
}

func Stop(timeout time.Duration) error {
	state, err := LoadState()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !stateAlive(state) {
		_ = DeleteStateForPID(state.PID)
		return nil
	}

	process, err := os.FindProcess(state.PID)
	if err != nil {
		return err
	}
	if err := terminateProcess(process); err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !stateProcessAlive(state) {
			_ = DeleteStateForPID(state.PID)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("daemon process %d did not stop within %s", state.PID, timeout)
}

func Alive() bool {
	state, err := LoadState()
	if err != nil {
		return false
	}
	return stateAlive(state)
}

func stateAlive(state *State) bool {
	if !stateProcessAlive(state) {
		return false
	}
	updatedAt := state.UpdatedAt
	if updatedAt == "" {
		updatedAt = state.StartedAt
	}
	ts, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return false
	}
	return time.Since(ts) <= heartbeatMaxAge
}

func stateProcessAlive(state *State) bool {
	if state == nil || !session.ProcessAlive(state.PID) {
		return false
	}
	if state.PIDStartToken == "" {
		return true
	}
	current := session.ProcessStartToken(state.PID)
	return current != "" && current == state.PIDStartToken
}
