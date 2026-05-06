//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListSkipsEntriesRemovedBetweenReadDirAndReadFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("SessionsDir returned error: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing.json"), filepath.Join(dir, "gone.json")); err != nil {
		t.Fatalf("create broken session symlink: %v", err)
	}

	sessions, err := List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected no sessions, got %d", len(sessions))
	}
}
