package clusterconnect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRuntimeLockRejectsConcurrentOwner(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())

	release, err := AcquireRuntimeLock()
	if err != nil {
		t.Fatalf("AcquireRuntimeLock returned error: %v", err)
	}
	if secondRelease, err := AcquireRuntimeLock(); err == nil {
		secondRelease()
		t.Fatal("second AcquireRuntimeLock unexpectedly succeeded")
	}

	release()
	reacquired, err := AcquireRuntimeLock()
	if err != nil {
		t.Fatalf("AcquireRuntimeLock after release returned error: %v", err)
	}
	reacquired()
}

func TestAcquireRuntimeLockRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEALTUN_HOME", home)
	connectDir := filepath.Join(home, ".sealtun", connectStateDirName)
	if err := os.MkdirAll(connectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(connectDir, connectRuntimeLockFileName)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if release, err := AcquireRuntimeLock(); err == nil {
		release()
		t.Fatal("AcquireRuntimeLock accepted a symlink")
	}
}
