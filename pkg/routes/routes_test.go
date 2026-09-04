package routes

import (
	"strings"
	"testing"
)

func TestParseValidRoutes(t *testing.T) {
	parsed, err := Parse([]string{"/api=8080", "/admin=9090"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parsed) != 2 || parsed[0].Path != "/api" || parsed[0].Port != 8080 || parsed[1].Path != "/admin" || parsed[1].Port != 9090 {
		t.Fatalf("unexpected parse result: %#v", parsed)
	}
	if parsed, err := Parse(nil); err != nil || parsed != nil {
		t.Fatalf("empty input should yield nil routes, got %#v, %v", parsed, err)
	}
}

func TestParseRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "/api", "=8080", "/api=", "/api=abc", "/api=0", "/api=70000"} {
		if _, err := Parse([]string{value}); err == nil {
			t.Fatalf("expected error for %q", value)
		}
	}
}

func TestValidateRejectsBadRoutes(t *testing.T) {
	cases := map[string][]Route{
		"empty path":                    {{Path: "", Port: 8080}},
		"missing slash":                 {{Path: "api", Port: 8080}},
		"query in path":                 {{Path: "/api?x=1", Port: 8080}},
		"zero port":                     {{Path: "/api", Port: 0}},
		"port too large":                {{Path: "/api", Port: 65536}},
		"duplicate exact":               {{Path: "/api", Port: 8080}, {Path: "/api", Port: 9090}},
		"duplicate after normalization": {{Path: "/api", Port: 8080}, {Path: "/api/", Port: 9090}},
	}
	for name, routes := range cases {
		if err := Validate(routes); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestMatchLongestSegmentAwarePrefix(t *testing.T) {
	table := []Route{{Path: "/", Port: 3000}, {Path: "/api", Port: 8080}, {Path: "/api/admin", Port: 9090}}
	cases := map[string]int{
		"/":            3000,
		"/index.html":  3000,
		"/api":         8080,
		"/api/":        8080,
		"/api/users":   8080,
		"/apiserver":   3000, // segment boundary: /api must not match
		"/api/admin":   9090,
		"/api/admin/x": 9090,
		"/api/admins":  8080,
	}
	for path, want := range cases {
		port, ok := Match(table, path)
		if !ok || port != want {
			t.Fatalf("Match(%q) = %d, %v; want %d", path, port, ok, want)
		}
	}
}

func TestMatchWithoutRootRoute(t *testing.T) {
	table := []Route{{Path: "/api", Port: 8080}}
	if _, ok := Match(table, "/other"); ok {
		t.Fatal("expected no match for /other without a root route")
	}
	if port, ok := Match(table, "/api/x"); !ok || port != 8080 {
		t.Fatalf("expected /api/x to match, got %d, %v", port, ok)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"/":      "/",
		"api":    "/api",
		"/api/":  "/api",
		"/api//": "/api",
		"  /x  ": "/x",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Fatalf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEqualAndLabel(t *testing.T) {
	a := []Route{{Path: "/api", Port: 8080}, {Path: "/", Port: 3000}}
	b := []Route{{Path: "/", Port: 3000}, {Path: "/api/", Port: 8080}}
	if !Equal(a, b) {
		t.Fatal("routes with reordered entries and trailing slashes should be equal")
	}
	if Equal(a, []Route{{Path: "/api", Port: 8081}}) {
		t.Fatal("different ports must not be equal")
	}
	if got := Label(a); !strings.Contains(got, "/api->8080") || !strings.Contains(got, "/->3000") {
		t.Fatalf("unexpected label: %q", got)
	}
}

func TestStripPrefix(t *testing.T) {
	cases := map[string]string{
		"/|/":             "/",
		"/|/x":            "/x",
		"/api|/api":       "/",
		"/api|/api/users": "/users",
		"/api/|/api/x":    "/x",
	}
	for in, want := range cases {
		parts := strings.SplitN(in, "|", 2)
		if got := StripPrefix(parts[0], parts[1]); got != want {
			t.Fatalf("StripPrefix(%q, %q) = %q, want %q", parts[0], parts[1], got, want)
		}
	}
}

func TestHasRootRoute(t *testing.T) {
	if HasRootRoute([]Route{{Path: "/api", Port: 8080}}) {
		t.Fatal("no root route present")
	}
	if !HasRootRoute([]Route{{Path: "/api", Port: 8080}, {Path: "/", Port: 3000}}) {
		t.Fatal("root route should be detected")
	}
	if !HasRootRoute([]Route{{Path: "//", Port: 3000}}) {
		t.Fatal("normalized double-slash root should be detected")
	}
}
