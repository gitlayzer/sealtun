package routes

import (
	"strings"
	"testing"
)

// R1: deep matching boundaries not covered by the base suite.
func TestMatchPseudoRegexIsLiteral(t *testing.T) {
	table := []Route{{Path: "/api.*", Port: 8080}}
	if _, ok := Match(table, "/apiXYZ"); ok {
		t.Fatal("route path must be a literal prefix, not a regex")
	}
	if port, ok := Match(table, "/api.*/x"); !ok || port != 8080 {
		t.Fatalf("literal /api.* prefix should match /api.*/x, got %d %v", port, ok)
	}
}

func TestMatchAdjacentPrefixes(t *testing.T) {
	table := []Route{{Path: "/a", Port: 1}, {Path: "/ab", Port: 2}}
	if port, _ := Match(table, "/abc"); port != 0 {
		t.Fatalf("/abc must not match /a or /ab (segment boundary), got %d", port)
	}
	if port, _ := Match(table, "/ab/c"); port != 2 {
		t.Fatalf("/ab/c should match /ab, got %d", port)
	}
	if port, _ := Match(table, "/a/b"); port != 1 {
		t.Fatalf("/a/b should match /a, got %d", port)
	}
}

func TestMatchDoubleSlashRequest(t *testing.T) {
	table := []Route{{Path: "/api", Port: 8080}}
	port, ok := Match(table, "/api//users")
	if !ok || port != 8080 {
		t.Fatalf("/api//users should still match /api, got %d %v", port, ok)
	}
	if got := StripPrefix("/api", "/api//users"); got != "//users" {
		t.Fatalf("strip leaves the second slash intact, got %q", got)
	}
}

func TestMatchRootNeverWinsOverLonger(t *testing.T) {
	// root first in the list: order must not matter
	table := []Route{{Path: "/", Port: 3000}, {Path: "/api", Port: 8080}}
	if port, _ := Match(table, "/api/x"); port != 8080 {
		t.Fatalf("longer prefix must win regardless of order, got %d", port)
	}
}

func TestMatchVeryLongPath(t *testing.T) {
	table := []Route{{Path: "/api", Port: 8080}}
	long := "/api/" + strings.Repeat("x", 8000)
	if port, ok := Match(table, long); !ok || port != 8080 {
		t.Fatal("8KB path should match")
	}
}

func TestStripPrefixAtBoundary(t *testing.T) {
	if got := StripPrefix("/api", "/api"); got != "/" {
		t.Fatalf("exact prefix strips to root, got %q", got)
	}
	if got := StripPrefix("/api", "/api/"); got != "/" {
		t.Fatalf("prefix with trailing slash strips to root, got %q", got)
	}
}

func TestValidateRejectsEncodedAndDotPaths(t *testing.T) {
	for _, p := range []string{"/api%2Fusers", "/api/../admin", "/api x"} {
		// dot segments and spaces in a CONFIGURED prefix are nonsense; encoded
		// slashes would never match the decoded request path predictably.
		_ = p
	}
	// space is rejected outright:
	if err := Validate([]Route{{Path: "/api x", Port: 8080}}); err == nil {
		t.Fatal("path with space must be rejected")
	}
	// encoded and dot paths are technically plain prefixes today; they can
	// never match decoded request paths, so they are dead config worth rejecting.
	if err := Validate([]Route{{Path: "/api%2Fusers", Port: 8080}}); err == nil {
		t.Fatal("encoded path must be rejected as dead config")
	}
	if err := Validate([]Route{{Path: "/api/../admin", Port: 8080}}); err == nil {
		t.Fatal("dot-segment path must be rejected as dead config")
	}
}
