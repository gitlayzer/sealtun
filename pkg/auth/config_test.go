package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

func TestGetSealosDirDoesNotFollowLegacySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires extra privileges on Windows")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	outside := filepath.Join(home, "outside-auth.json")
	if err := os.WriteFile(outside, []byte(`{"region":"outside"}`), 0o600); err != nil {
		t.Fatalf("write outside auth: %v", err)
	}
	legacyDir := filepath.Join(home, legacyConfigDir)
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(legacyDir, "auth.json")); err != nil {
		t.Fatalf("create legacy auth symlink: %v", err)
	}

	dir, err := GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy auth symlink not to be copied, stat err=%v", err)
	}
}

func TestSaveAuthDataDoesNotCommitAuthWhenKubeconfigWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "kubeconfig"), 0o700); err != nil {
		t.Fatalf("create kubeconfig directory: %v", err)
	}

	err = SaveAuthData(AuthData{
		Region:        "https://gzg.sealos.run",
		AccessToken:   "access",
		RegionalToken: "regional",
	}, "apiVersion: v1")
	if err == nil {
		t.Fatal("expected kubeconfig write failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should not be written when kubeconfig fails, stat err=%v", err)
	}
}

func TestSaveAuthDataRestoresKubeconfigWhenAuthWriteFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SaveAuthData(AuthData{
		Region:        "https://gzg.sealos.run",
		AccessToken:   "old-access",
		RegionalToken: "old-regional",
	}, "old-kubeconfig"); err != nil {
		t.Fatalf("initial SaveAuthData returned error: %v", err)
	}

	dir, err := GetSealosDir()
	if err != nil {
		t.Fatalf("GetSealosDir returned error: %v", err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.Remove(authPath); err != nil {
		t.Fatalf("remove auth.json: %v", err)
	}
	if err := os.Mkdir(authPath, 0o700); err != nil {
		t.Fatalf("replace auth.json with directory: %v", err)
	}

	err = SaveAuthData(AuthData{
		Region:        "https://hzh.sealos.run",
		AccessToken:   "new-access",
		RegionalToken: "new-regional",
	}, "new-kubeconfig")
	if err == nil {
		t.Fatal("expected auth write failure")
	}

	got, err := os.ReadFile(filepath.Join(dir, "kubeconfig"))
	if err != nil {
		t.Fatalf("read kubeconfig: %v", err)
	}
	if string(got) != "old-kubeconfig" {
		t.Fatalf("expected old kubeconfig to be restored, got %q", string(got))
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

func TestPollIntervalRejectsNonPositiveValues(t *testing.T) {
	for _, input := range []int{0, -1, -30} {
		if got := pollIntervalFromSeconds(input); got != 5*time.Second {
			t.Fatalf("expected default poll interval for %d, got %s", input, got)
		}
	}
	if got := pollIntervalFromSeconds(2); got != 2*time.Second {
		t.Fatalf("expected explicit poll interval, got %s", got)
	}
}
