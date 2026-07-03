package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/session"
)

const tunnelOperationLockWait = 30 * time.Second

func withTunnelOperationLock(tunnelID string, fn func() error) error {
	if err := session.ValidateTunnelIDForExternalUse(tunnelID); err != nil {
		return err
	}
	root, err := auth.GetSealosDir()
	if err != nil {
		return err
	}
	lockPath := filepath.Join(root, fmt.Sprintf("tunnel-%s.lock", tunnelID))
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- tunnelID is validated before joining a private Sealtun lock path.
	if err != nil {
		return err
	}
	release, err := session.LockFileForExternalUse(file, tunnelOperationLockWait)
	if err != nil {
		_ = file.Close()
		return err
	}
	defer func() {
		release()
		_ = file.Close()
	}()
	return fn()
}
