package clusterconnect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/session"
)

const connectStateDirName = "connect"
const connectStateFileName = "state.json"
const connectRuntimeLockFileName = "runtime.lock"

type State struct {
	Mode          string         `json:"mode"`
	Namespace     string         `json:"namespace"`
	Region        string         `json:"region,omitempty"`
	Profile       string         `json:"profile,omitempty"`
	Listen        string         `json:"listen,omitempty"`
	RouteCount    int            `json:"routeCount,omitempty"`
	HostCount     int            `json:"hostCount,omitempty"`
	Rules         []RedirectRule `json:"rules,omitempty"`
	Hosts         []HostEntry    `json:"hosts,omitempty"`
	PID           int            `json:"pid"`
	PIDStartToken string         `json:"pidStartToken,omitempty"`
	StartedAt     string         `json:"startedAt"`
}

func statePath(create bool) (string, error) {
	var root string
	var err error
	if create {
		root, err = auth.GetSealosDir()
	} else {
		root, err = auth.CurrentSealtunDir()
	}
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, connectStateDirName)
	if create {
		if _, err := auth.EnsurePrivateDir(dir, "connect state directory"); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, connectStateFileName), nil
}

func AcquireRuntimeLock() (func(), error) {
	path, err := statePath(true)
	if err != nil {
		return nil, err
	}
	path = filepath.Join(filepath.Dir(path), connectRuntimeLockFileName)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("connect runtime lock %s is not a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed lock path under the private Sealtun config directory.
	if err != nil {
		return nil, err
	}
	releaseLock, err := session.LockFileForExternalUse(file, 0)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire connect runtime lock: %w", err)
	}
	return func() {
		releaseLock()
		_ = file.Close()
	}, nil
}

func SaveState(state State) error {
	if state.PID == 0 {
		state.PID = os.Getpid()
	}
	if state.PIDStartToken == "" {
		state.PIDStartToken = session.ProcessStartToken(state.PID)
	}
	if state.StartedAt == "" {
		state.StartedAt = time.Now().Format(time.RFC3339)
	}
	path, err := statePath(true)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeStateFileAtomic(path, append(data, '\n'))
}

func LoadState() (*State, string, error) {
	path, err := statePath(false)
	if err != nil {
		return nil, "", err
	}
	data, err := readStateFile(path)
	if err != nil {
		return nil, path, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, path, err
	}
	return &state, path, nil
}

func RemoveState() error {
	path, err := statePath(false)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s State) Alive() bool {
	if s.PID <= 0 {
		return false
	}
	if !session.ProcessAlive(s.PID) {
		return false
	}
	if s.PIDStartToken == "" {
		return true
	}
	return session.ProcessStartToken(s.PID) == s.PIDStartToken
}

func StopStateProcess(state State) error {
	if !state.Alive() {
		return nil
	}
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return err
	}
	if err := signalInterrupt(process); err != nil {
		return fmt.Errorf("failed to interrupt connect process %d: %w", state.PID, err)
	}
	for i := 0; i < 20; i++ {
		if !state.Alive() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if state.Alive() {
		return fmt.Errorf("interrupt sent but connect process %d is still running", state.PID)
	}
	return nil
}

func readStateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("connect state file %s is not a regular file", path)
	}
	return os.ReadFile(path) // #nosec G304 -- path is fixed under the private Sealtun config directory.
}

func writeStateFileAtomic(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("connect state file %s is not a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	tmpPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(path), os.Getpid(), time.Now().UnixNano()))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- temp file is created under the private Sealtun config directory.
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
