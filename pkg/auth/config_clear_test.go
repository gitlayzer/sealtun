package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearAuthDataRemovesPersistedFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}

	for _, name := range []string{"auth.json", "kubeconfig"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := ClearAuthData(); err != nil {
		t.Fatalf("ClearAuthData returned error: %v", err)
	}

	for _, name := range []string{"auth.json", "kubeconfig"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err=%v", name, err)
		}
	}
}
