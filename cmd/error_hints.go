package cmd

import (
	"fmt"
	"strings"
)

func commandErrorWithHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if hint := actionableErrorHintText(msg); hint != "" {
		return fmt.Sprintf("%s\nHint: %s", msg, hint)
	}
	return msg
}

func actionableErrorHint(err error) string {
	if err == nil {
		return ""
	}
	return actionableErrorHintText(err.Error())
}

func actionableErrorHintText(msg string) string {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return ""
	}
	if strings.Contains(lower, "quota") ||
		strings.Contains(lower, "insufficient") ||
		strings.Contains(lower, "balance") ||
		strings.Contains(lower, "billing") ||
		strings.Contains(lower, "exceeded") ||
		strings.Contains(lower, "out of cpu") ||
		strings.Contains(lower, "out of memory") {
		return "Sealos/Kubernetes rejected the resource request. Check account balance/quota in Sealos Cloud, lower tunnel resources via the YAML `resources` field and `sealtun apply`, or clean up unused tunnels."
	}
	if strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "rbac") ||
		(strings.Contains(lower, "permission denied") && !isLocalOSPathError(lower)) {
		return "The active login may not have permission in this region/namespace. Run `sealtun status`, confirm the active profile/region, then re-login if needed."
	}
	// Local domain validation errors contain "dns" in their message; only hint
	// at DNS propagation when the failure is an actual resolution/lookup error.
	if !strings.Contains(lower, "invalid custom domain") &&
		(strings.Contains(lower, "cname") ||
			strings.Contains(lower, "no such host") ||
			strings.Contains(lower, "server misbehaving") ||
			strings.Contains(lower, "lookup ") ||
			strings.Contains(lower, "dns resolution")) {
		return "DNS may not have propagated or the resolver may be stale. Run `sealtun domain plan` to confirm the CNAME target, then `sealtun domain verify --wait` after updating DNS."
	}
	if strings.Contains(lower, "x509:") ||
		strings.Contains(lower, "certificate signed by unknown authority") ||
		strings.Contains(lower, "tls: failed to verify certificate") ||
		strings.Contains(lower, "tls: handshake failure") {
		return "If the Sealos cluster API certificate is not trusted, re-login to refresh the kubeconfig (`sealtun login <region>`). For a private HTTPS upstream with a self-signed certificate, recreate the tunnel with `--target-insecure-skip-verify`; never use it for public upstreams."
	}
	if strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "no route to host") {
		return "The target or Kubernetes API was not reachable from this machine. Check the local/target service, network path, active region, and rerun `sealtun doctor <tunnel-id>`."
	}
	return ""
}

// isLocalOSPathError reports whether a "permission denied" message came from
// the local filesystem (unreadable config, YAML, or state files) rather than
// a cluster-side RBAC rejection. Local OS errors are formatted by Go as
// "<verb> <path>: permission denied".
func isLocalOSPathError(lower string) bool {
	if !strings.Contains(lower, ": permission denied") {
		return false
	}
	for _, verb := range []string{"open ", "read ", "lstat ", "stat ", "mkdir ", "write ", "create ", "remove ", "chmod ", "rename "} {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	return false
}
