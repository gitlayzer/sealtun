//go:build windows

package daemon

import "os"

func terminateProcess(process *os.Process) error {
	return process.Kill()
}
