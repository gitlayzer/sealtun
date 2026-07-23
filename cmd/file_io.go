package cmd

import (
	"fmt"
	"os"
	"path/filepath"
)

func validateRegularOutputPath(path, purpose string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s to %q: path is a symlink", purpose, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to write %s to %q: not a regular file", purpose, path)
	}
	return nil
}

func writeRegularFileAtomic(path string, data []byte, perm os.FileMode, purpose string) error {
	if err := validateRegularOutputPath(path, purpose); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readRegularFile(path, purpose string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", purpose, path)
	}
	return os.ReadFile(path) // #nosec G304 -- callers provide validated or fixed application paths.
}
