package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetSealosDirMigratesLegacyDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyDir := filepath.Join(home, legacyConfigDir)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	legacyFile := filepath.Join(legacyDir, "auth.json")
	if err := os.WriteFile(legacyFile, []byte(`{"region":"test"}`), 0o600); err != nil {
		t.Fatalf("write legacy auth file: %v", err)
	}

	dir, err := GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}

	expectedDir := filepath.Join(home, currentConfigDir)
	if dir != expectedDir {
		t.Fatalf("expected dir %s, got %s", expectedDir, dir)
	}
	if _, err := os.Stat(filepath.Join(expectedDir, "auth.json")); err != nil {
		t.Fatalf("expected migrated auth.json to exist: %v", err)
	}
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("expected legacy dir to be moved away, stat err=%v", err)
	}
}
