//go:build !windows

package cmd

import "os"

func sessionTestCurrentPID() int {
	return os.Getpid()
}
