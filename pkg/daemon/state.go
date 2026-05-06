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
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
	UpdatedAt string `json:"updatedAt"`
}

func statePath() (string, error) {
	root, err := auth.GetSealosDir()
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
		PID:       pid,
		StartedAt: time.Now().Format(time.RFC3339),
		UpdatedAt: time.Now().Format(time.RFC3339),
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
	return os.Rename(tmpPath, path)
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

	if info, err := os.Stat(path); err == nil {
		switch alive, ok := lockOwnerAlive(path); {
		case ok && alive:
			return nil, os.ErrExist
		case ok && !alive:
			_ = os.Remove(path)
		case time.Since(info.ModTime()) > 30*time.Second:
			_ = os.Remove(path)
		}
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

	if info, err := os.Stat(path); err == nil {
		if alive, ok := lockOwnerAlive(path); ok && alive {
			return nil, os.ErrExist
		}
		if state, stateErr := LoadState(); stateErr == nil && session.ProcessAlive(state.PID) {
			return nil, os.ErrExist
		}
		switch alive, ok := lockOwnerAlive(path); {
		case ok && !alive:
			_ = os.Remove(path)
		case time.Since(info.ModTime()) > heartbeatMaxAge:
			_ = os.Remove(path)
		}
	}

	return createOwnedLock(path)
}

func createOwnedLock(path string) (func(), error) {
	token := fmt.Sprintf("%d:%d", os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
		data, err := os.ReadFile(path)
		if err == nil && string(data) == token {
			_ = os.Remove(path)
		}
	}, nil
}

func lockOwnerAlive(path string) (bool, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	pidText, _, ok := strings.Cut(string(data), ":")
	if !ok {
		return false, false
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return false, false
	}
	return session.ProcessAlive(pid), true
}

func LoadState() (*State, error) {
	path, err := statePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse daemon state: %w", err)
	}
	return &state, nil
}

func DeleteState() error {
	path, err := statePath()
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
		if !session.ProcessAlive(state.PID) {
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
	if state == nil || !session.ProcessAlive(state.PID) {
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
