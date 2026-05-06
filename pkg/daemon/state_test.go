package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLaunchLockReleaseDoesNotRemoveReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	release, err := AcquireLaunchLock()
	if err != nil {
		t.Fatalf("AcquireLaunchLock returned error: %v", err)
	}

	path := filepath.Join(home, ".sealtun", lockFileName)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove owned lock: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement lock: %v", err)
	}

	release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("replacement lock should remain: %v", err)
	}
	if string(data) != "replacement" {
		t.Fatalf("unexpected replacement lock content: %q", string(data))
	}
}

func TestLaunchLockRemovesDeadOwnerTokenWithoutWaitingForMaxAge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	path := filepath.Join(root, lockFileName)
	if err := os.WriteFile(path, []byte("999999:1"), 0o600); err != nil {
		t.Fatalf("write dead owner lock: %v", err)
	}

	release, err := AcquireLaunchLock()
	if err != nil {
		t.Fatalf("AcquireLaunchLock should replace dead owner lock: %v", err)
	}
	defer release()
}

func TestRuntimeLockReleaseDoesNotRemoveReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	release, err := AcquireRuntimeLock()
	if err != nil {
		t.Fatalf("AcquireRuntimeLock returned error: %v", err)
	}

	path := filepath.Join(home, ".sealtun", runtimeLockFileName)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove owned lock: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement lock: %v", err)
	}

	release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("replacement lock should remain: %v", err)
	}
	if string(data) != "replacement" {
		t.Fatalf("unexpected replacement lock content: %q", string(data))
	}
}

func TestRuntimeLockDoesNotStartDuplicateWhenLockOwnerIsAliveWithoutState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	path := filepath.Join(root, runtimeLockFileName)
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d:1", os.Getpid())), 0o600); err != nil {
		t.Fatalf("write live owner runtime lock: %v", err)
	}

	release, err := AcquireRuntimeLock()
	if err == nil {
		release()
		t.Fatal("expected runtime lock acquisition to fail while lock owner PID is alive")
	}
}

func TestRuntimeLockDoesNotStartDuplicateWhenStatePIDIsAliveButHeartbeatIsStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SaveState(os.Getpid()); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	path := filepath.Join(home, ".sealtun", runtimeLockFileName)
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write runtime lock: %v", err)
	}
	stale := time.Now().Add(-2 * heartbeatMaxAge)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("mark runtime lock stale: %v", err)
	}

	release, err := AcquireRuntimeLock()
	if err == nil {
		release()
		t.Fatal("expected runtime lock acquisition to fail while recorded PID is alive")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("runtime lock should remain: %v", readErr)
	}
	if string(data) != "existing" {
		t.Fatalf("unexpected runtime lock content: %q", string(data))
	}
}

func TestDeleteStateForPIDDoesNotDeleteReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SaveState(111); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	if err := SaveState(222); err != nil {
		t.Fatalf("replacement SaveState returned error: %v", err)
	}

	if err := DeleteStateForPID(111); err != nil {
		t.Fatalf("DeleteStateForPID returned error: %v", err)
	}

	state, err := LoadState()
	if err != nil {
		t.Fatalf("replacement state should remain: %v", err)
	}
	if state.PID != 222 {
		t.Fatalf("expected replacement PID 222, got %d", state.PID)
	}
}

func TestTouchStateForPIDDoesNotTouchReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SaveState(111); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	if err := SaveState(222); err != nil {
		t.Fatalf("replacement SaveState returned error: %v", err)
	}
	before, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}

	if err := TouchStateForPID(111); err != nil {
		t.Fatalf("TouchStateForPID returned error: %v", err)
	}

	after, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState returned error: %v", err)
	}
	if after.PID != 222 {
		t.Fatalf("expected replacement PID 222, got %d", after.PID)
	}
	if after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("replacement state should not be touched, before=%s after=%s", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestStateAliveRequiresFreshHeartbeatForSameSnapshot(t *testing.T) {
	stale := time.Now().Add(-2 * heartbeatMaxAge).Format(time.RFC3339)
	if stateAlive(&State{PID: os.Getpid(), StartedAt: stale, UpdatedAt: stale}) {
		t.Fatal("expected stale heartbeat to be considered not alive")
	}
}
