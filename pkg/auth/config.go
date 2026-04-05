package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AuthData struct {
	Region          string    `json:"region"`
	AccessToken     string    `json:"access_token"`
	RegionalToken   string    `json:"regional_token"`
	AuthenticatedAt string    `json:"authenticated_at"`
	AuthMethod      string    `json:"auth_method"`
}

// GetSealtunDir returns the path to ~/.sealtun
func GetSealtunDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	// Verify directory is writable
	testFile := filepath.Join(dir, ".write_test")
	if err := os.WriteFile(testFile, []byte("ok"), 0600); err != nil {
		return "", fmt.Errorf("config directory %s is not writable: %w", dir, err)
	}
	_ = os.Remove(testFile)

	return dir, nil
}

// SaveAuthData saves auth and kubeconfig
func SaveAuthData(authData AuthData, kubeconfig string) error {
	dir, err := GetSealtunDir()
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
	dir, err := GetSealtunDir()
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
