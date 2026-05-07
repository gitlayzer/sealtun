package session

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSaveListDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := Save(TunnelSession{
		TunnelID:  "abc123",
		Namespace: "ns-demo",
		Protocol:  "https",
		Host:      "abc.example.com",
		LocalPort: "3000",
		PID:       42,
		Resources: []string{"sealtun-abc123", "sealtun-abc123-app"},
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	sessions, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].TunnelID != "abc123" {
		t.Fatalf("unexpected session id: %s", sessions[0].TunnelID)
	}

	if err := Delete("abc123"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	path := filepath.Join(home, ".sealtun", sessionsDirName, "abc123.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected session file to be removed, stat err=%v", err)
	}
}

func TestRejectsUnsafeTunnelIDPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir returned error: %v", err)
	}
	authPath := filepath.Join(filepath.Dir(dir), "auth.json")
	if err := os.WriteFile(authPath, []byte("auth"), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	for _, tt := range []struct {
		name string
		fn   func() error
	}{
		{name: "save traversal", fn: func() error { return Save(TunnelSession{TunnelID: "../auth"}) }},
		{name: "update traversal", fn: func() error { return Update(TunnelSession{TunnelID: "../auth"}) }},
		{name: "delete traversal", fn: func() error { return Delete("../auth") }},
		{name: "save too long for resource name", fn: func() error {
			return Save(TunnelSession{TunnelID: "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcd"})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Fatal("expected unsafe tunnel id to be rejected")
			}
		})
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	if string(data) != "auth" {
		t.Fatalf("auth file was modified: %q", string(data))
	}
}

func TestUpdateDoesNotCreateMissingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := Update(TunnelSession{
		TunnelID:        "missing123",
		ConnectionState: ConnectionStateConnected,
	})
	if !os.IsNotExist(err) {
		t.Fatalf("expected missing session update to return not-exist error, got %v", err)
	}

	sessions, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no session to be created, got %d", len(sessions))
	}
}

func TestListSkipsSymlinkedSessionFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires extra privileges on Windows")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	outside := filepath.Join(home, "outside.json")
	if err := os.WriteFile(outside, []byte(`{"tunnelId":"outside123"}`), 0o600); err != nil {
		t.Fatalf("write outside session: %v", err)
	}
	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir returned error: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outside123.json")); err != nil {
		t.Fatalf("create session symlink: %v", err)
	}

	sessions, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected symlinked session to be skipped, got %d session(s)", len(sessions))
	}
}

func TestUpdateDoesNotRecreateDeletedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Save(TunnelSession{TunnelID: "deleted123"}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loaded, err := Get("deleted123")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if err := Delete("deleted123"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}

	loaded.ConnectionState = ConnectionStateError
	err = Update(*loaded)
	if !os.IsNotExist(err) {
		t.Fatalf("expected deleted session update to return not-exist error, got %v", err)
	}

	sessions, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected deleted session to stay deleted, got %d", len(sessions))
	}
}

func TestIsStale(t *testing.T) {
	currentPID := os.Getpid()
	if IsStale(TunnelSession{PID: currentPID, CreatedAt: time.Now().Format(time.RFC3339)}, time.Minute) {
		t.Fatal("expected current process session to be active")
	}

	old := time.Now().Add(-2 * time.Minute).Format(time.RFC3339)
	if !IsStale(TunnelSession{PID: 999999, CreatedAt: old}, time.Minute) {
		t.Fatal("expected nonexistent old process session to be stale")
	}

	recent := time.Now().Format(time.RFC3339)
	if IsStale(TunnelSession{PID: 999999, CreatedAt: recent}, time.Minute) {
		t.Fatal("expected recent nonexistent process session to respect grace period")
	}

	if IsStaleWithOwner(TunnelSession{PID: 999999, CreatedAt: old}, time.Minute, true) {
		t.Fatal("expected externally alive owner to keep session active")
	}

	if !IsStaleWithOwner(TunnelSession{
		PID:             currentPID,
		ConnectionState: ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}, time.Minute, true) {
		t.Fatal("expected stopped session to be stale even while owner process is alive")
	}
}

func TestRuntimeStatus(t *testing.T) {
	currentPID := os.Getpid()

	if got := RuntimeStatus(TunnelSession{PID: currentPID, Mode: "foreground"}); got != "active" {
		t.Fatalf("expected foreground active status, got %s", got)
	}
	if got := RuntimeStatus(TunnelSession{PID: currentPID, Mode: "daemon", ConnectionState: ConnectionStateConnecting}); got != "connecting" {
		t.Fatalf("expected daemon connecting status, got %s", got)
	}
	if got := RuntimeStatus(TunnelSession{PID: currentPID, Mode: "daemon", ConnectionState: ConnectionStateConnected}); got != "active" {
		t.Fatalf("expected daemon connected status, got %s", got)
	}
	if got := RuntimeStatus(TunnelSession{PID: currentPID, Mode: "daemon", ConnectionState: ConnectionStateError}); got != "error" {
		t.Fatalf("expected daemon error status, got %s", got)
	}
	if got := RuntimeStatusWithOwner(TunnelSession{PID: 0, Mode: "daemon", ConnectionState: ConnectionStateConnecting}, true); got != "connecting" {
		t.Fatalf("expected external daemon owner to keep session connecting, got %s", got)
	}
	if got := RuntimeStatusWithOwner(TunnelSession{PID: 0, Mode: "daemon", ConnectionState: ConnectionStateStopped}, false); got != "stopped" {
		t.Fatalf("expected stopped status to be preserved, got %s", got)
	}
}

func TestScrubCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Save(TunnelSession{
		TunnelID:   "secret123",
		Kubeconfig: "apiVersion: v1",
		Secret:     "tunnel-secret",
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if err := ScrubCredentials(); err != nil {
		t.Fatalf("ScrubCredentials returned error: %v", err)
	}

	sess, err := Get("secret123")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if sess.Kubeconfig != "" {
		t.Fatal("expected kubeconfig to be scrubbed")
	}
	if sess.Secret != "" {
		t.Fatal("expected secret to be scrubbed")
	}
	if sess.ConnectionState != ConnectionStateStopped {
		t.Fatalf("expected scrubbed session to be stopped, got %s", sess.ConnectionState)
	}
}

func TestScrubCredentialsMarksAlreadyScrubbedSessionsStopped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Save(TunnelSession{
		TunnelID:        "already-empty",
		PID:             os.Getpid(),
		ConnectionState: ConnectionStateConnected,
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if err := ScrubCredentials(); err != nil {
		t.Fatalf("ScrubCredentials returned error: %v", err)
	}

	sess, err := Get("already-empty")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if sess.PID != 0 {
		t.Fatalf("expected scrubbed session PID to be reset, got %d", sess.PID)
	}
	if sess.ConnectionState != ConnectionStateStopped {
		t.Fatalf("expected scrubbed session to be stopped, got %s", sess.ConnectionState)
	}
}

func TestSaveDoesNotRestoreScrubbedCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Save(TunnelSession{
		TunnelID:   "race123",
		Kubeconfig: "apiVersion: v1",
		Secret:     "tunnel-secret",
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loadedBeforeScrub, err := Get("race123")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if err := ScrubCredentials(); err != nil {
		t.Fatalf("ScrubCredentials returned error: %v", err)
	}

	loadedBeforeScrub.ConnectionState = ConnectionStateConnected
	if err := Save(*loadedBeforeScrub); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	sess, err := Get("race123")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if sess.Kubeconfig != "" || sess.Secret != "" {
		t.Fatalf("expected scrubbed credentials to stay empty, kubeconfig=%q secret=%q", sess.Kubeconfig, sess.Secret)
	}
	if sess.PID != 0 {
		t.Fatalf("expected scrubbed session PID to stay reset, got %d", sess.PID)
	}
	if sess.ConnectionState != ConnectionStateStopped {
		t.Fatalf("expected scrubbed session to stay stopped, got %s", sess.ConnectionState)
	}
}

func TestListSkipsInvalidSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Save(TunnelSession{TunnelID: "valid123"}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write invalid session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unsafe.json"), []byte(`{"tunnelId":"../auth"}`), 0o600); err != nil {
		t.Fatalf("write unsafe session: %v", err)
	}

	sessions, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 valid session, got %d", len(sessions))
	}
	if sessions[0].TunnelID != "valid123" {
		t.Fatalf("unexpected tunnel id: %s", sessions[0].TunnelID)
	}
}

func TestScrubCredentialsRemovesInvalidSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir returned error: %v", err)
	}
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte(`{"secret":"unterminated`), 0o600); err != nil {
		t.Fatalf("write invalid session: %v", err)
	}

	if err := ScrubCredentials(); err != nil {
		t.Fatalf("ScrubCredentials returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected invalid session file to be removed, stat err=%v", err)
	}
}

func TestScrubCredentialsRemovesUnsafeSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir returned error: %v", err)
	}
	path := filepath.Join(dir, "unsafe.json")
	if err := os.WriteFile(path, []byte(`{"tunnelId":"../auth"}`), 0o600); err != nil {
		t.Fatalf("write unsafe session: %v", err)
	}

	if err := ScrubCredentials(); err != nil {
		t.Fatalf("ScrubCredentials returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected unsafe session file to be removed, stat err=%v", err)
	}
}
