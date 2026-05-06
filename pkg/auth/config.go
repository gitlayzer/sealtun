package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Workspace struct {
	UID      string `json:"uid"`
	ID       string `json:"id"`
	TeamName string `json:"teamName"`
}

type AuthData struct {
	Region           string     `json:"region"`
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

	legacySessionsDir := filepath.Join(legacyDir, "sessions")
	entries, err := os.ReadDir(legacySessionsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if err := copyLegacyFile(filepath.Join(legacySessionsDir, entry.Name()), filepath.Join(sessionsDir, entry.Name())); err != nil {
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

	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// SaveAuthData saves auth and kubeconfig
func SaveAuthData(authData AuthData, kubeconfig string) error {
	dir, err := GetSealosDir()
	if err != nil {
		return err
	}

	authPath := filepath.Join(dir, "auth.json")
	b, err := json.MarshalIndent(authData, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(authPath, b, 0600); err != nil {
		return err
	}

	kcPath := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kcPath, []byte(kubeconfig), 0600); err != nil {
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
	b, err := os.ReadFile(authPath)
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
