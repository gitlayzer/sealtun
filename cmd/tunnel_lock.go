package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/session"
)

const tunnelOperationLockWait = 30 * time.Second

func withTunnelOperationLock(tunnelID string, fn func() error) error {
	return withTunnelOperationLockContext(context.Background(), tunnelID, fn)
}

func withTunnelOperationLockContext(ctx context.Context, tunnelID string, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := session.ValidateTunnelIDForExternalUse(tunnelID); err != nil {
		return err
	}
	root, err := auth.GetSealosDir()
	if err != nil {
		return err
	}
	lockPath := filepath.Join(root, fmt.Sprintf("tunnel-%s.lock", tunnelID))
	if err := validateTunnelOperationLockPath(lockPath); err != nil {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- tunnelID is validated before joining a private Sealtun lock path.
	if err != nil {
		return err
	}
	wait := tunnelOperationLockWait
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			_ = file.Close()
			return context.DeadlineExceeded
		}
		if remaining < wait {
			wait = remaining
		}
	}
	release, err := session.LockFileForExternalUse(file, wait)
	if err != nil {
		_ = file.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	defer func() {
		release()
		_ = file.Close()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func withTunnelOperationLocks(tunnelIDs []string, fn func() error) error {
	ids := append([]string(nil), tunnelIDs...)
	for _, tunnelID := range ids {
		if err := session.ValidateTunnelIDForExternalUse(tunnelID); err != nil {
			return err
		}
	}
	sort.Strings(ids)
	ids = compactSortedStrings(ids)

	var acquire func(int) error
	acquire = func(index int) error {
		if index == len(ids) {
			return fn()
		}
		return withTunnelOperationLock(ids[index], func() error {
			return acquire(index + 1)
		})
	}
	return acquire(0)
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] == values[write-1] {
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write]
}

func validateTunnelOperationLockPath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("tunnel operation lock %s is not a regular file", path)
	}
	return nil
}
