package auth

import (
	"encoding/json"
	"fmt"
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

// GetSealosDir returns the path to ~/.sealos
func GetSealosDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".sealos")
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
