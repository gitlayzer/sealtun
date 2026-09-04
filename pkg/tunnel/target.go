package tunnel

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/labring/sealtun/pkg/routes"
)

type Target struct {
	URL                   string
	Address               string
	Port                  string
	TLSInsecureSkipVerify bool
	// Routes holds optional path-prefix rules for multi-service HTTPS
	// tunnels. Matched prefixes are forwarded to the route's local port;
	// everything else falls back to this target.
	Routes []routes.Route
}

func LocalhostTarget(localPort string) (Target, error) {
	if strings.TrimSpace(localPort) == "" {
		return Target{}, fmt.Errorf("local port is required")
	}
	targetURL := (&url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", localPort)}).String()
	return ParseTarget(targetURL)
}

type TargetOptions struct {
	TLSInsecureSkipVerify bool
	Routes                []routes.Route
}

func ParseTarget(raw string) (Target, error) {
	return ParseTargetWithOptions(raw, TargetOptions{})
}

func ParseTargetWithOptions(raw string, opts TargetOptions) (Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, fmt.Errorf("target is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("invalid target %q: %w", raw, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return Target{}, fmt.Errorf("target scheme must be http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return Target{}, fmt.Errorf("target host is required")
	}
	if parsed.User != nil {
		return Target{}, fmt.Errorf("target must not include userinfo")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return Target{}, fmt.Errorf("target must not include query or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return Target{}, fmt.Errorf("target path is not supported")
	}
	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return Target{}, fmt.Errorf("target port must be between 1 and 65535")
	}
	if opts.TLSInsecureSkipVerify && scheme != "https" {
		return Target{}, fmt.Errorf("target TLS insecure skip verify is only supported for https targets")
	}
	parsed.Scheme = scheme
	parsed.Host = net.JoinHostPort(parsed.Hostname(), port)
	parsed.Path = ""
	return Target{
		URL:                   parsed.String(),
		Address:               parsed.Host,
		Port:                  port,
		TLSInsecureSkipVerify: opts.TLSInsecureSkipVerify,
	}, nil
}

func TargetFor(localPort, targetURL string) (Target, error) {
	return TargetForWithOptions(localPort, targetURL, TargetOptions{})
}

func TargetForWithOptions(localPort, targetURL string, opts TargetOptions) (Target, error) {
	var target Target
	if strings.TrimSpace(targetURL) != "" {
		parsed, err := ParseTargetWithOptions(targetURL, opts)
		if err != nil {
			return Target{}, err
		}
		target = parsed
	} else {
		if opts.TLSInsecureSkipVerify {
			return Target{}, fmt.Errorf("target TLS insecure skip verify requires --target with an https URL")
		}
		local, err := LocalhostTarget(localPort)
		if err != nil {
			return Target{}, err
		}
		target = local
	}
	// Routes are attached verbatim. Restricting them to local-port tunnels is
	// the CLI/config layer's job: sessions always carry a targetURL (local
	// tunnels default it to http://localhost:<port>), so this function cannot
	// distinguish a user-supplied upstream from the default local one.
	target.Routes = opts.Routes
	return target, nil
}
