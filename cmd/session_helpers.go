package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/auth"
	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/k8s"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/session"
	"github.com/labring/sealtun/pkg/tunnel"
)

var errMissingSessionKubeconfig = errors.New("session has no embedded kubeconfig")

type sessionSnapshot struct {
	Status             string
	ProcessAlive       bool
	LocalPortReachable bool
}

func findSession(tunnelID string) (*session.TunnelSession, error) {
	sess, err := session.Get(tunnelID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("tunnel session %q not found", tunnelID)
		}
		return nil, fmt.Errorf("load tunnel session %q: %w", tunnelID, err)
	}
	return sess, nil
}

func targetReachable(targetURL string) bool {
	target, err := tunnel.ParseTarget(targetURL)
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("tcp", target.Address, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func defaultLocalTargetURL(port string) string {
	if port == "" || port == "-" {
		return ""
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", port)}).String()
}

func sessionTargetURL(sess session.TunnelSession) string {
	if strings.TrimSpace(sess.TargetURL) != "" {
		return sess.TargetURL
	}
	return defaultLocalTargetURL(sess.LocalPort)
}

func sessionTargetLabel(sess session.TunnelSession) string {
	if strings.TrimSpace(sess.TargetURL) != "" {
		return sess.TargetURL
	}
	if strings.TrimSpace(sess.LocalPort) != "" {
		return "localhost:" + sess.LocalPort
	}
	return "unknown"
}

func sessionTargetTLSConfig(insecureSkipVerify bool) *session.TargetTLSConfig {
	if !insecureSkipVerify {
		return nil
	}
	return &session.TargetTLSConfig{InsecureSkipVerify: true}
}

func targetOptionsForSession(sess session.TunnelSession) tunnel.TargetOptions {
	return tunnel.TargetOptions{TLSInsecureSkipVerify: targetTLSInsecureSkipVerifyEnabled(sess.TargetTLS)}
}

func targetTLSInsecureSkipVerifyEnabled(config *session.TargetTLSConfig) bool {
	return config != nil && config.InsecureSkipVerify
}

func resourcesToK8s(config *session.ResourceConfig) *k8s.ResourceConfig {
	if config == nil {
		return nil
	}
	out := &k8s.ResourceConfig{}
	if config.Requests != nil {
		out.Requests = k8s.ResourceValues{
			CPU:    config.Requests.CPU,
			Memory: config.Requests.Memory,
		}
	}
	if config.Limits != nil {
		out.Limits = k8s.ResourceValues{
			CPU:    config.Limits.CPU,
			Memory: config.Limits.Memory,
		}
	}
	return out
}

func resourcesFromK8s(config *k8s.ResourceConfig) *session.ResourceConfig {
	if config == nil {
		return nil
	}
	return &session.ResourceConfig{
		Requests: &session.ResourceValues{
			CPU:    config.Requests.CPU,
			Memory: config.Requests.Memory,
		},
		Limits: &session.ResourceValues{
			CPU:    config.Limits.CPU,
			Memory: config.Limits.Memory,
		},
	}
}

func validateTargetTLSOptions(targetURL string, insecureSkipVerify bool) error {
	if !insecureSkipVerify {
		return nil
	}
	if strings.TrimSpace(targetURL) == "" {
		return fmt.Errorf("target TLS insecure skip verify requires a target URL")
	}
	target, err := tunnel.ParseTargetWithOptions(targetURL, tunnel.TargetOptions{TLSInsecureSkipVerify: true})
	if err != nil {
		return err
	}
	if !strings.HasPrefix(target.URL, "https://") {
		return fmt.Errorf("target TLS insecure skip verify is only supported for https targets")
	}
	return nil
}

func k8sClientForSession(sess session.TunnelSession) (*k8s.Client, error) {
	if sess.Namespace == "" {
		return nil, fmt.Errorf("session namespace is missing for tunnel %q", sess.TunnelID)
	}

	if sess.Kubeconfig != "" {
		return k8s.NewClientFromKubeconfig(sess.Kubeconfig, &auth.AuthData{Region: sess.Region})
	}

	authData, err := auth.LoadAuthData()
	if err != nil {
		return nil, fmt.Errorf("%w for tunnel %q and current login is unavailable: %w", errMissingSessionKubeconfig, sess.TunnelID, err)
	}
	if sess.Region == "" {
		return nil, fmt.Errorf("%w for tunnel %q and the legacy session does not record its region", errMissingSessionKubeconfig, sess.TunnelID)
	}
	if authData.Region == "" || sess.Region != authData.Region {
		return nil, fmt.Errorf("%w for tunnel %q; session region is %s but current login region is %s", errMissingSessionKubeconfig, sess.TunnelID, sess.Region, authData.Region)
	}
	if sess.Namespace == "" {
		return nil, fmt.Errorf("%w for tunnel %q and the legacy session does not record its namespace", errMissingSessionKubeconfig, sess.TunnelID)
	}

	root, err := auth.GetSealosDir()
	if err != nil {
		return nil, err
	}
	kubeconfigPath := filepath.Join(root, "kubeconfig")
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return nil, fmt.Errorf("%w for tunnel %q and current kubeconfig is unavailable: %w", errMissingSessionKubeconfig, sess.TunnelID, err)
	}

	client, err := k8s.NewClient(kubeconfigPath, authData)
	if err != nil {
		return nil, err
	}
	if client.Namespace() != sess.Namespace {
		return nil, fmt.Errorf("%w for tunnel %q; session namespace is %s but current kubeconfig namespace is %s", errMissingSessionKubeconfig, sess.TunnelID, sess.Namespace, client.Namespace())
	}
	return client, nil
}

var cleanupSessionResources = func(ctx context.Context, sess session.TunnelSession) error {
	client, err := k8sClientForSession(sess)
	if err != nil {
		return err
	}

	return client.WithNamespace(sess.Namespace).CleanupTunnel(ctx, sess.TunnelID)
}

var pauseSessionResources = func(ctx context.Context, sess session.TunnelSession) error {
	client, err := k8sClientForSession(sess)
	if err != nil {
		return err
	}

	return client.WithNamespace(sess.Namespace).PauseTunnel(ctx, sess.TunnelID)
}

var resumeSessionResources = func(ctx context.Context, sess session.TunnelSession) error {
	client, err := k8sClientForSession(sess)
	if err != nil {
		return err
	}

	return client.WithNamespace(sess.Namespace).ResumeTunnel(ctx, sess.TunnelID)
}

var collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
	client, err := k8sClientForSession(sess)
	if err != nil {
		return nil, err
	}
	return client.WithNamespace(sess.Namespace).TunnelRemoteState(ctx, sess.TunnelID)
}

func sessionControlHost(sess session.TunnelSession) string {
	if sess.SealosHost != "" {
		return sess.SealosHost
	}
	return sess.Host
}

func sessionProtocol(sess session.TunnelSession) string {
	protocol := tunnelprotocol.Normalize(sess.Protocol)
	if protocol == "" {
		return tunnelprotocol.HTTPS
	}
	return protocol
}

func sessionUsesHTTP(sess session.TunnelSession) bool {
	return sessionProtocol(sess) == tunnelprotocol.HTTPS
}

func normalizePublicHostname(value string) (string, error) {
	host, err := validateCustomDomain(value)
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", fmt.Errorf("public host is missing")
	}
	return host, nil
}

func sessionSealosHostForDomain(sess session.TunnelSession, computed string) string {
	if sess.SealosHost != "" {
		return sess.SealosHost
	}
	if sess.CustomDomain == "" && sess.Host != "" {
		return sess.Host
	}
	return computed
}

func sessionOwnerAlive(sess session.TunnelSession) bool {
	if sess.PID <= 0 {
		return false
	}
	if sess.Mode == "daemon" {
		return daemonstate.Alive()
	}
	return session.OwnerAlive(sess)
}

func classifySession(sess session.TunnelSession, checkLocalPort bool) sessionSnapshot {
	processAlive := sessionOwnerAlive(sess)
	status := session.RuntimeStatusWithOwner(sess, processAlive)
	localReachable := false
	if checkLocalPort {
		localReachable = targetReachable(sessionTargetURL(sess))
		if status == "active" && processAlive && !localReachable {
			status = "degraded"
		}
	} else if status == "active" && processAlive && sess.Mode != "daemon" {
		status = "running"
	}

	return sessionSnapshot{
		Status:             status,
		ProcessAlive:       processAlive,
		LocalPortReachable: localReachable,
	}
}

func sessionIsStale(sess session.TunnelSession, gracePeriod time.Duration) bool {
	if sessionExpired(sess, time.Now()) {
		return true
	}
	return session.IsStaleWithOwner(sess, gracePeriod, sessionOwnerAlive(sess))
}

func sessionCleanupEligible(sess session.TunnelSession, gracePeriod time.Duration) bool {
	if sessionIsStale(sess, gracePeriod) {
		return true
	}
	return sess.ConnectionState == session.ConnectionStateError
}

func sessionNeedsAutomaticRecovery(sess session.TunnelSession, gracePeriod time.Duration) bool {
	if sessionExpired(sess, time.Now()) {
		return true
	}
	if sess.ConnectionState == session.ConnectionStateStopped {
		return false
	}
	return session.IsStaleWithOwner(sess, gracePeriod, sessionOwnerAlive(sess))
}

func shouldPreserveStoppedSession(sess *session.TunnelSession) bool {
	return sess != nil && sess.ConnectionState == session.ConnectionStateStopped
}

func sessionExpired(sess session.TunnelSession, now time.Time) bool {
	if strings.TrimSpace(sess.ExpiresAt) == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(sess.ExpiresAt))
	if err != nil {
		return true
	}
	return !now.Before(expiresAt)
}

func refreshSessionFromRemote(ctx context.Context, sess *session.TunnelSession) error {
	if sess == nil || sess.TunnelID == "" || sess.Namespace == "" {
		return nil
	}
	tunnelID := sess.TunnelID
	return withTunnelOperationLockContext(ctx, tunnelID, func() error {
		current, err := findSession(tunnelID)
		if err != nil {
			return err
		}
		if err := refreshSessionFromRemoteLocked(ctx, current); err != nil {
			return err
		}
		*sess = *current
		return nil
	})
}

// refreshSessionFromRemoteLocked requires the tunnel operation lock.
func refreshSessionFromRemoteLocked(ctx context.Context, sess *session.TunnelSession) error {
	if sess == nil || sess.TunnelID == "" || sess.Namespace == "" {
		return nil
	}
	state, err := collectSessionRemoteState(ctx, *sess)
	if err != nil {
		return fmt.Errorf("refresh tunnel %s from remote state: %w", sess.TunnelID, err)
	}
	if state == nil {
		return nil
	}
	updated, err := session.UpdateAtomic(sess.TunnelID, func(current *session.TunnelSession) (bool, error) {
		return mergeSessionRemoteState(current, state), nil
	})
	if err != nil {
		return fmt.Errorf("persist refreshed tunnel %s: %w", sess.TunnelID, err)
	}
	*sess = *updated
	return nil
}

func mergeSessionRemoteState(sess *session.TunnelSession, state *k8s.TunnelRemoteState) bool {
	changed := false
	if state.SealosHost != "" && state.SealosHost != sess.SealosHost {
		sess.SealosHost = state.SealosHost
		changed = true
	}
	if state.CustomDomain != sess.CustomDomain {
		sess.CustomDomain = state.CustomDomain
		changed = true
	}
	publicPortAuthoritative := state.PublicPort != 0 || (state.DeploymentOK && state.Protocol != "")
	if publicPortAuthoritative && state.PublicPort != sess.PublicPort {
		sess.PublicPort = state.PublicPort
		changed = true
	}
	if state.Protocol != "" && state.Protocol != sess.Protocol {
		sess.Protocol = state.Protocol
		changed = true
	}
	if state.LocalPort != "" && state.LocalPort != sess.LocalPort {
		sess.LocalPort = state.LocalPort
		changed = true
	}
	if state.TargetURL != sess.TargetURL {
		sess.TargetURL = state.TargetURL
		changed = true
	}
	if state.AuthSecretOK && !sess.CredentialsScrubbed {
		if state.Secret != "" && state.Secret != sess.Secret {
			sess.Secret = state.Secret
			changed = true
		}
		if !basicAuthConfigEqual(sess.BasicAuth, state.BasicAuth) {
			sess.BasicAuth = basicAuthFromK8s(state.BasicAuth)
			changed = true
		}
		if !accessPolicyEqual(sess.AccessPolicy, state.AccessPolicy) {
			sess.AccessPolicy = accessPolicyFromK8s(state.AccessPolicy)
			changed = true
		}
	}
	if state.DeploymentOK {
		if !resourceConfigEqual(sess.ResourceConfig, state.Resources) {
			sess.ResourceConfig = resourcesFromK8s(state.Resources)
			changed = true
		}
	}
	wantHost := state.PublicHost
	if wantHost == "" {
		wantHost = state.SealosHost
	}
	if wantHost != "" && wantHost != sess.Host {
		sess.Host = wantHost
		changed = true
	}
	return changed
}

func findSessionRefreshed(ctx context.Context, tunnelID string) (*session.TunnelSession, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	_ = refreshSessionFromRemote(ctx, sess)
	return sess, nil
}

// findSessionSyncedLocked requires the tunnel operation lock.
func findSessionSyncedLocked(ctx context.Context, tunnelID string) (*session.TunnelSession, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	if err := refreshSessionFromRemoteLocked(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func basicAuthFromK8s(config *k8s.BasicAuthOptions) *session.BasicAuthConfig {
	if config == nil {
		return nil
	}
	return &session.BasicAuthConfig{
		Enabled:      true,
		Username:     config.Username,
		PasswordHash: config.PasswordHash,
	}
}

func accessPolicyFromK8s(policy *accesspolicy.Policy) *session.AccessPolicy {
	if policy == nil {
		return nil
	}
	return &session.AccessPolicy{
		BearerTokenHashes: append([]string(nil), policy.BearerTokenHashes...),
		IPAllowlist:       append([]string(nil), policy.IPAllowlist...),
		IPDenylist:        append([]string(nil), policy.IPDenylist...),
		TemporaryTokens:   temporaryTokensFromRuntime(policy.TemporaryTokens),
		RateLimit:         policy.RateLimit,
		Audit:             auditConfigFromRuntime(policy.Audit),
	}
}

func temporaryTokensFromRuntime(tokens []accesspolicy.TemporaryToken) []session.TemporaryToken {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]session.TemporaryToken, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, session.TemporaryToken{
			Name:      token.Name,
			TokenHash: token.TokenHash,
			TTL:       token.TTL,
			ExpiresAt: token.ExpiresAt,
		})
	}
	return out
}

func auditConfigFromRuntime(config *accesspolicy.AuditConfig) *session.AuditConfig {
	if config == nil {
		return nil
	}
	return &session.AuditConfig{Enabled: config.Enabled}
}

func basicAuthConfigEqual(current *session.BasicAuthConfig, next *k8s.BasicAuthOptions) bool {
	if current == nil && next == nil {
		return true
	}
	if current == nil || next == nil {
		return false
	}
	return current.Enabled && current.Username == next.Username && basicAuthPasswordHash(current) == next.PasswordHash
}

func accessPolicyEqual(current *session.AccessPolicy, next *accesspolicy.Policy) bool {
	currentJSON, _ := json.Marshal(accessPolicyToRuntime(current))
	nextJSON, _ := json.Marshal(next)
	return string(currentJSON) == string(nextJSON)
}

func resourceConfigEqual(current *session.ResourceConfig, next *k8s.ResourceConfig) bool {
	currentJSON, _ := json.Marshal(resourcesToK8s(current))
	nextJSON, _ := json.Marshal(next)
	return string(currentJSON) == string(nextJSON)
}
