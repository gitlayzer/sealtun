package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Workspace struct {
	UID      string `json:"uid"`
	ID       string `json:"id"`
	TeamName string `json:"teamName"`
}

type AuthData struct {
	Region           string     `json:"region"`
	SealosDomain     string     `json:"sealos_domain,omitempty"`
	AccessToken      string     `json:"access_token"`
	RegionalToken    string     `json:"regional_token"`
	AuthenticatedAt  string     `json:"authenticated_at"`
	AuthMethod       string     `json:"auth_method"`
	CurrentWorkspace *Workspace `json:"current_workspace,omitempty"`
}

const (
	currentConfigDir = ".sealtun"
	legacyConfigDir  = ".sealos"
)

// GetSealosDir returns the config directory, copying only Sealtun-owned legacy files when needed.
func GetSealosDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(home, currentConfigDir)
	legacyDir := filepath.Join(home, legacyConfigDir)

	_, currentErr := os.Stat(dir)
	currentMissing := os.IsNotExist(currentErr)
	if currentErr != nil && !currentMissing {
		return "", currentErr
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if currentMissing {
		if info, legacyErr := os.Stat(legacyDir); legacyErr == nil && info.IsDir() {
			if err := copyLegacyConfigFiles(legacyDir, dir); err != nil {
				return "", fmt.Errorf("copy legacy config files from %s to %s: %w", legacyDir, dir, err)
			}
		}
	}

	// Verify directory is writable
	testFile := filepath.Join(dir, ".write_test")
	if err := os.WriteFile(testFile, []byte("ok"), 0600); err != nil {
		return "", fmt.Errorf("config directory %s is not writable: %w", dir, err)
	}
	_ = os.Remove(testFile)

	return dir, nil
}

func copyLegacyConfigFiles(legacyDir, dir string) error {
	for _, name := range []string{"auth.json", "kubeconfig"} {
		if err := copyLegacyFile(filepath.Join(legacyDir, name), filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func copyLegacyFile(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}

	in, err := os.Open(src) // #nosec G304 -- legacy source is a validated regular file under the user-owned legacy config directory.
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- destination is a fixed file under the user-owned Sealtun config directory.
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// SaveAuthData saves auth and kubeconfig
func SaveAuthData(authData AuthData, kubeconfig string) error {
	dir, err := GetSealosDir()
	if err != nil {
		return err
	}

	b, err := json.MarshalIndent(authData, "", "  ") // #nosec G117 -- auth data is intentionally persisted with 0600 permissions for CLI reuse.
	if err != nil {
		return err
	}

	kcPath := filepath.Join(dir, "kubeconfig")
	previousKubeconfig, hadPreviousKubeconfig, err := readExistingFile(kcPath)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(kcPath, []byte(kubeconfig), 0600); err != nil {
		return err
	}

	authPath := filepath.Join(dir, "auth.json")
	if err := writeFileAtomic(authPath, b, 0600); err != nil {
		if rollbackErr := restoreFile(kcPath, previousKubeconfig, hadPreviousKubeconfig, 0600); rollbackErr != nil {
			return fmt.Errorf("%w; failed to restore previous kubeconfig: %v", err, rollbackErr)
		}
		return err
	}

	return nil
}

func readExistingFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is one of the fixed Sealtun auth files under the user-owned config directory.
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func restoreFile(path string, data []byte, exists bool, perm os.FileMode) error {
	if !exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, data, perm)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(path), os.Getpid(), time.Now().UnixNano()))
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm) // #nosec G304 -- temp file is created next to a fixed Sealtun config file.
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// LoadAuthData reads the saved auth data
func LoadAuthData() (*AuthData, error) {
	dir, err := GetSealosDir()
	if err != nil {
		return nil, err
	}
	authPath := filepath.Join(dir, "auth.json")
	b, err := os.ReadFile(authPath) // #nosec G304 -- auth path is fixed under the user-owned Sealtun config directory.
	if err != nil {
		return nil, err
	}
	var data AuthData
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// ClearAuthData removes the persisted auth session and kubeconfig files.
func ClearAuthData() error {
	dir, err := GetSealosDir()
	if err != nil {
		return err
	}

	for _, name := range []string{"auth.json", "kubeconfig"} {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
