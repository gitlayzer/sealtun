//go:build windows

package session

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func lockSessionFile(file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	handle := windows.Handle(file.Fd())
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	var overlapped windows.Overlapped

	for {
		err := windows.LockFileEx(handle, flags, 0, 1, 0, &overlapped)
		if err == nil {
			return nil
		}
		if err != windows.ERROR_LOCK_VIOLATION && err != windows.ERROR_SHARING_VIOLATION {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out acquiring session lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func unlockSessionFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
