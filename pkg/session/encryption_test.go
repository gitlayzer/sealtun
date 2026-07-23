package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// withTempConfigHome points HOME at a temp dir so the session key file is
// created in isolation, and returns a cleanup that restores the original.
func withTempConfigHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig, had := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", dir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("HOME", orig)
		} else {
			_ = os.Unsetenv("HOME")
		}
	})
}

func TestEncryptSessionDataRoundTrip(t *testing.T) {
	withTempConfigHome(t)

	plaintext := []byte(`{"tunnelId":"abc123","secret":"top-secret"}`)
	encrypted, err := encryptSessionData(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if string(encrypted) == string(plaintext) {
		t.Fatal("expected ciphertext to differ from plaintext")
	}
	if string(encrypted[:len(encryptedSessionMagic)]) != encryptedSessionMagic {
		t.Fatalf("expected encrypted blob to carry magic prefix, got %q", encrypted[:len(encryptedSessionMagic)])
	}
	// The raw secret must not appear in the on-disk bytes.
	if containsBytes(encrypted, []byte("top-secret")) {
		t.Fatal("plaintext secret leaked into encrypted blob")
	}

	decrypted, err := decryptSessionData(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("round trip mismatch: got %q want %q", decrypted, plaintext)
	}
}

func TestDecryptSessionDataAcceptsLegacyPlaintext(t *testing.T) {
	withTempConfigHome(t)

	legacy := []byte(`{"tunnelId":"abc123"}`)
	out, err := decryptSessionData(legacy)
	if err != nil {
		t.Fatalf("legacy plaintext should pass through, got error: %v", err)
	}
	if string(out) != string(legacy) {
		t.Fatalf("legacy plaintext altered: got %q want %q", out, legacy)
	}
}

func TestSessionEncryptionKeyIsStablePerHome(t *testing.T) {
	withTempConfigHome(t)

	first, err := sessionEncryptionKey()
	if err != nil {
		t.Fatalf("first key: %v", err)
	}
	second, err := sessionEncryptionKey()
	if err != nil {
		t.Fatalf("second key: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("expected a stable key across calls within the same config home")
	}
	if len(first) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(first))
	}
	// Key file must be private.
	home, _ := os.LookupEnv("HOME")
	info, err := os.Stat(filepath.Join(home, ".sealtun", sessionKeyFileName))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected key file mode 0600, got %o", perm)
	}
}

func TestSaveFailsClosedWhenSessionKeyIsCorrupt(t *testing.T) {
	withTempConfigHome(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	root := filepath.Join(home, ".sealtun")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create config root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, sessionKeyFileName), []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt session key: %v", err)
	}

	err = Save(TunnelSession{TunnelID: "failclosed", Secret: "must-not-reach-disk"})
	if err == nil {
		t.Fatal("expected Save to reject a corrupt session key")
	}
	data, readErr := os.ReadFile(filepath.Join(root, sessionsDirName, "failclosed.json"))
	if readErr == nil && containsBytes(data, []byte("must-not-reach-disk")) {
		t.Fatal("session secret was written as plaintext after encryption failed")
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read session file: %v", readErr)
	}
}

func BenchmarkListFromConfigDirEncrypted(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	for i := 0; i < 64; i++ {
		if err := Save(TunnelSession{
			TunnelID:   fmt.Sprintf("session-%02d", i),
			Kubeconfig: "apiVersion: v1\nkind: Config\n",
			Secret:     "benchmark-secret",
		}); err != nil {
			b.Fatalf("save fixture %d: %v", i, err)
		}
	}
	root := filepath.Join(home, ".sealtun")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sessions, err := ListFromConfigDir(root)
		if err != nil {
			b.Fatalf("list sessions: %v", err)
		}
		if len(sessions) != 64 {
			b.Fatalf("got %d sessions, want 64", len(sessions))
		}
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
