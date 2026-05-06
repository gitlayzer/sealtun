//go:build !windows

package session

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func lockSessionFile(file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out acquiring session lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func unlockSessionFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
