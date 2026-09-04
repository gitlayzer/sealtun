// Package routes defines path-prefix routing rules for multi-service HTTPS
// tunnels. A request whose path matches a route prefix is forwarded to that
// route's local port; everything else falls back to the tunnel's primary
// target (localPort or --target upstream).
package routes

import (
	"fmt"
	"strconv"
	"strings"
)

// Route forwards one path prefix to a local port.
type Route struct {
	Path string `json:"path" yaml:"path"`
	Port int    `json:"port" yaml:"port"`
}

// Parse converts repeated "path=port" flag values into routes.
func Parse(values []string) ([]Route, error) {
	if len(values) == 0 {
		return nil, nil
	}
	parsed := make([]Route, 0, len(values))
	for _, value := range values {
		path, portText, ok := strings.Cut(strings.TrimSpace(value), "=")
		if !ok || strings.TrimSpace(path) == "" || strings.TrimSpace(portText) == "" {
			return nil, fmt.Errorf("invalid route %q; expected the form /path=port, for example /api=8080", value)
		}
		port, err := strconv.Atoi(strings.TrimSpace(portText))
		if err != nil {
			return nil, fmt.Errorf("invalid route %q: port must be a number", value)
		}
		parsed = append(parsed, Route{Path: path, Port: port})
	}
	if err := Validate(parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// Validate checks path and port sanity and rejects duplicate prefixes.
func Validate(routes []Route) error {
	seen := map[string]bool{}
	for _, route := range routes {
		raw := strings.TrimSpace(route.Path)
		if raw == "" {
			return fmt.Errorf("route path is required")
		}
		if !strings.HasPrefix(raw, "/") {
			return fmt.Errorf("route path %q must start with /", route.Path)
		}
		normalized := NormalizePath(raw)
		if strings.ContainsAny(normalized, "?# \t") {
			return fmt.Errorf("route path %q must be a plain path prefix without query, fragment, or whitespace", route.Path)
		}
		if route.Port < 1 || route.Port > 65535 {
			return fmt.Errorf("route %q has invalid port %d; must be between 1 and 65535", route.Path, route.Port)
		}
		if seen[normalized] {
			return fmt.Errorf("duplicate route for path prefix %q", normalized)
		}
		seen[normalized] = true
	}
	return nil
}

// NormalizePath ensures a leading slash and strips trailing slashes so
// equivalent prefixes compare equal; the root "/" is preserved.
func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

// Match returns the port of the longest route prefix matching requestPath.
// Matching is segment-aware: "/api" matches "/api" and "/api/users" but not
// "/apiserver". The root prefix "/" matches every path.
func Match(routes []Route, requestPath string) (int, bool) {
	route, ok := MatchRoute(routes, requestPath)
	if !ok {
		return 0, false
	}
	return route.Port, true
}

// MatchRoute returns the longest matching route, so callers can also use the
// matched prefix (e.g. to strip it before forwarding).
func MatchRoute(routes []Route, requestPath string) (Route, bool) {
	best := -1
	bestLen := -1
	for i, route := range routes {
		prefix := NormalizePath(route.Path)
		if prefix == "" {
			continue
		}
		if !prefixMatches(prefix, requestPath) {
			continue
		}
		if len(prefix) > bestLen {
			best = i
			bestLen = len(prefix)
		}
	}
	if best < 0 {
		return Route{}, false
	}
	matched := routes[best]
	matched.Path = NormalizePath(matched.Path)
	return matched, true
}

// StripPrefix removes the matched route prefix from requestPath so the local
// service sees its own root-relative paths: "/api/users" via route "/api"
// becomes "/users", and "/api" itself becomes "/". The root prefix "/" never
// strips anything.
func StripPrefix(prefix, requestPath string) string {
	prefix = NormalizePath(prefix)
	if prefix == "/" || prefix == "" {
		return requestPath
	}
	rest := strings.TrimPrefix(requestPath, prefix)
	if rest == "" {
		return "/"
	}
	return rest
}

func prefixMatches(prefix, requestPath string) bool {
	if prefix == "/" {
		return strings.HasPrefix(requestPath, "/")
	}
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

// Label renders routes compactly for summaries and diffs, e.g.
// "/api->8080, /admin->9090".
func Label(routes []Route) string {
	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		parts = append(parts, fmt.Sprintf("%s->%d", NormalizePath(route.Path), route.Port))
	}
	return strings.Join(parts, ", ")
}

// Equal reports whether two route tables are semantically identical.
func Equal(a, b []Route) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[string]int{}
	for _, route := range a {
		counts[fmt.Sprintf("%s=%d", NormalizePath(route.Path), route.Port)]++
	}
	for _, route := range b {
		key := fmt.Sprintf("%s=%d", NormalizePath(route.Path), route.Port)
		counts[key]--
		if counts[key] < 0 {
			return false
		}
	}
	return true
}

// HasRootRoute reports whether any route uses the "/" prefix, which matches
// every request and therefore shadows the tunnel's primary target entirely.
func HasRootRoute(routes []Route) bool {
	for _, route := range routes {
		if NormalizePath(route.Path) == "/" {
			return true
		}
	}
	return false
}
