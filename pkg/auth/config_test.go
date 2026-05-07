package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetSealosDirCopiesLegacySealtunFilesOnly(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(legacyDir, "unrelated.json"), []byte(`{"owner":"other"}`), 0o600); err != nil {
		t.Fatalf("write unrelated legacy file: %v", err)
	}
	legacySessionsDir := filepath.Join(legacyDir, "sessions")
	if err := os.MkdirAll(legacySessionsDir, 0o700); err != nil {
		t.Fatalf("create legacy sessions dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacySessionsDir, "old.json"), []byte(`{"tunnelId":"old"}`), 0o600); err != nil {
		t.Fatalf("write legacy session file: %v", err)
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
		t.Fatalf("expected copied auth.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedDir, "unrelated.json")); !os.IsNotExist(err) {
		t.Fatalf("expected unrelated legacy file not to be copied, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedDir, "sessions", "old.json")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy session files not to be copied, stat err=%v", err)
	}
	if _, err := os.Stat(legacyDir); err != nil {
		t.Fatalf("expected legacy dir to remain untouched, stat err=%v", err)
	}
}

func TestGetSealosDirDoesNotRecopyLegacyAfterCurrentDirExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyDir := filepath.Join(home, legacyConfigDir)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "auth.json"), []byte(`{"region":"test"}`), 0o600); err != nil {
		t.Fatalf("write legacy auth file: %v", err)
	}

	dir, err := GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}
	currentAuth := filepath.Join(dir, "auth.json")
	if err := os.Remove(currentAuth); err != nil {
		t.Fatalf("remove current auth: %v", err)
	}

	if _, err := GetSealosDir(); err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}
	if _, err := os.Stat(currentAuth); !os.IsNotExist(err) {
		t.Fatalf("expected removed current auth not to be recopied, stat err=%v", err)
	}
}

func TestResolveRegionRejectsPlainHTTP(t *testing.T) {
	if _, err := ResolveRegion("http://custom-region.example"); err == nil {
		t.Fatal("expected plain http region to be rejected")
	}
}

func TestResolveRegionRejectsUnsupportedCustomRegions(t *testing.T) {
	tests := []string{
		"https://custom-region.example",
		"https://custom-region.example/path",
		"https://custom-region.example?debug=true",
		"https://custom-region.example#fragment",
		"https://user:pass@custom-region.example",
		"https://127.0.0.1:8443",
		"https://[::1]:8443",
		"https://localhost:8443",
	}

	for _, input := range tests {
		if _, err := ResolveRegion(input); err == nil {
			t.Fatalf("expected %s to be rejected", input)
		}
	}
}

func TestResolveRegionAllowsKnownRegionURLWithTrailingSlash(t *testing.T) {
	got, err := ResolveRegion("https://hzh.sealos.run/")
	if err != nil {
		t.Fatalf("expected known region URL with trailing slash to be accepted: %v", err)
	}
	if got != "https://hzh.sealos.run" {
		t.Fatalf("unexpected normalized region: %s", got)
	}
}
