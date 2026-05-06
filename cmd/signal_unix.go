//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

func signalCleanupSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
}
