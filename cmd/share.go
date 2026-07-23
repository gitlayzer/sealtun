package cmd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type shareCreatePayload struct {
	TunnelID  string `json:"tunnelId"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
}

type shareListItem struct {
	Name      string `json:"name"`
	ExpiresAt string `json:"expiresAt"`
	Expired   bool   `json:"expired"`
}

var shareName string
var shareTTL time.Duration
var shareToken string
var shareJSON bool
var shareOpen bool
var shareListJSON bool

var shareCmd = &cobra.Command{
	Use:          "share",
	Short:        "Manage temporary access links for HTTPS tunnels",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var shareCreateCmd = &cobra.Command{
	Use:          "create [tunnel-id]",
	Aliases:      []string{"add"},
	Short:        "Create a temporary access link",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := createShareLink(cmd.Context(), args[0], shareName, shareTTL, shareToken)
		if err != nil && payload == nil {
			return err
		}
		if shareJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if encodeErr := enc.Encode(payload); encodeErr != nil {
				return encodeErr
			}
			return err
		}
		printShareCreate(cmd, payload)
		if shareOpen {
			openBrowser(payload.URL)
		}
		return err
	},
}

var shareListCmd = &cobra.Command{
	Use:          "list [tunnel-id]",
	Short:        "List temporary access links without revealing tokens",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := listShareLinksWithContext(cmd.Context(), args[0], nowUTC())
		if err != nil {
			return err
		}
		if shareListJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}
		printShareList(cmd, args[0], items)
		return nil
	},
}

var shareRevokeCmd = &cobra.Command{
	Use:          "revoke [tunnel-id] [name]",
	Aliases:      []string{"delete", "remove"},
	Short:        "Revoke a temporary access link by name",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := revokeShareLink(cmd.Context(), args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Revoked temporary access link %q for tunnel %s.\n", strings.TrimSpace(args[1]), args[0])
		return nil
	},
}

var shareRotateCmd = &cobra.Command{
	Use:          "rotate [tunnel-id] [name]",
	Short:        "Rotate a temporary access link token",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := rotateShareLink(cmd.Context(), args[0], args[1], shareTTL)
		if err != nil && payload == nil {
			return err
		}
		if shareJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if encodeErr := enc.Encode(payload); encodeErr != nil {
				return encodeErr
			}
			return err
		}
		printShareCreate(cmd, payload)
		if shareOpen {
			openBrowser(payload.URL)
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(shareCmd)
	shareCmd.AddCommand(shareCreateCmd, shareListCmd, shareRevokeCmd, shareRotateCmd)

	shareCreateCmd.Flags().StringVar(&shareName, "name", "share", "Temporary link name")
	shareCreateCmd.Flags().DurationVar(&shareTTL, "ttl", time.Hour, "Temporary link lifetime")
	shareCreateCmd.Flags().StringVar(&shareToken, "token", "", "Use an explicit token instead of generating one")
	shareCreateCmd.Flags().BoolVar(&shareJSON, "json", false, "Output the created link as JSON")
	shareCreateCmd.Flags().BoolVar(&shareOpen, "open", false, "Open the temporary access URL in the browser")
	shareListCmd.Flags().BoolVar(&shareListJSON, "json", false, "Output temporary link metadata as JSON")
	shareRotateCmd.Flags().DurationVar(&shareTTL, "ttl", time.Hour, "Rotated temporary link lifetime")
	shareRotateCmd.Flags().BoolVar(&shareJSON, "json", false, "Output the rotated link as JSON")
	shareRotateCmd.Flags().BoolVar(&shareOpen, "open", false, "Open the rotated temporary access URL in the browser")
}

func createShareLink(ctx context.Context, tunnelID, name string, ttl time.Duration, token string) (*shareCreatePayload, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("share name is required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("share ttl must be greater than 0")
	}
	var payload *shareCreatePayload
	err := withTunnelOperationLock(tunnelID, func() error {
		sess, err := findSessionSyncedLocked(ctx, tunnelID)
		if err != nil {
			return err
		}
		if !sessionUsesHTTP(*sess) {
			return fmt.Errorf("temporary share links are only supported for https tunnels")
		}
		if sess.ConnectionState == session.ConnectionStateStopped {
			return fmt.Errorf("tunnel %s is stopped; run `sealtun start %s` before creating share links", sess.TunnelID, sess.TunnelID)
		}
		if sessionExpired(*sess, nowUTC()) {
			return fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
		}
		resolvedToken := token
		if resolvedToken == "" {
			resolvedToken, err = generateShareToken()
			if err != nil {
				return err
			}
		}
		hash, err := accesspolicy.HashToken(resolvedToken)
		if err != nil {
			return fmt.Errorf("share token: %w", err)
		}
		if temporaryTokenNameExists(sess.AccessPolicy, name) {
			return fmt.Errorf("temporary access link %q already exists for tunnel %s; use `sealtun share rotate %s %s` to replace it", name, sess.TunnelID, sess.TunnelID, name)
		}
		expiresAt := nowUTC().Add(ttl).UTC().Format(time.RFC3339)
		next := cloneAccessPolicy(sess.AccessPolicy)
		next.TemporaryTokens = append(next.TemporaryTokens, session.TemporaryToken{
			Name:      name,
			TokenHash: hash,
			TTL:       ttl.String(),
			ExpiresAt: expiresAt,
		})
		payload = &shareCreatePayload{
			TunnelID:  sess.TunnelID,
			Name:      name,
			URL:       temporaryAccessURL(sharePublicHost(*sess), resolvedToken),
			ExpiresAt: expiresAt,
		}
		if err := updateHTTPSAccessPolicy(ctx, sess, next); err != nil {
			if accessPolicyCommitted(err) {
				return err
			}
			payload = nil
			return err
		}
		return nil
	})
	if err != nil && payload == nil {
		return nil, err
	}
	return payload, err
}

func listShareLinks(tunnelID string, now time.Time) ([]shareListItem, error) {
	return listShareLinksWithContext(context.Background(), tunnelID, now)
}

func listShareLinksWithContext(ctx context.Context, tunnelID string, now time.Time) ([]shareListItem, error) {
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	if !sessionUsesHTTP(*sess) {
		return nil, fmt.Errorf("temporary share links are only supported for https tunnels")
	}
	if sess.AccessPolicy == nil {
		return nil, nil
	}
	items := make([]shareListItem, 0, len(sess.AccessPolicy.TemporaryTokens))
	for _, token := range sess.AccessPolicy.TemporaryTokens {
		item := shareListItem{Name: token.Name, ExpiresAt: token.ExpiresAt}
		if expiresAt, err := time.Parse(time.RFC3339, token.ExpiresAt); err == nil {
			item.Expired = !now.Before(expiresAt)
		} else {
			item.Expired = true
		}
		items = append(items, item)
	}
	return items, nil
}

func revokeShareLink(ctx context.Context, tunnelID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("share name is required")
	}
	return withTunnelOperationLock(tunnelID, func() error {
		sess, err := findSessionSyncedLocked(ctx, tunnelID)
		if err != nil {
			return err
		}
		if !sessionUsesHTTP(*sess) {
			return fmt.Errorf("temporary share links are only supported for https tunnels")
		}
		if sess.ConnectionState == session.ConnectionStateStopped {
			return fmt.Errorf("tunnel %s is stopped; run `sealtun start %s` before revoking share links", sess.TunnelID, sess.TunnelID)
		}
		if sessionExpired(*sess, nowUTC()) {
			return fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
		}
		next := cloneAccessPolicy(sess.AccessPolicy)
		filtered := next.TemporaryTokens[:0]
		removed := false
		for _, token := range next.TemporaryTokens {
			if token.Name == name {
				removed = true
				continue
			}
			filtered = append(filtered, token)
		}
		if !removed {
			return fmt.Errorf("temporary access link %q not found for tunnel %s", name, sess.TunnelID)
		}
		next.TemporaryTokens = filtered
		return updateHTTPSAccessPolicy(ctx, sess, emptyAccessPolicyAsNil(next))
	})
}

func rotateShareLink(ctx context.Context, tunnelID, name string, ttl time.Duration) (*shareCreatePayload, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("share name is required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("share ttl must be greater than 0")
	}
	var payload *shareCreatePayload
	err := withTunnelOperationLock(tunnelID, func() error {
		sess, err := findSessionSyncedLocked(ctx, tunnelID)
		if err != nil {
			return err
		}
		if !sessionUsesHTTP(*sess) {
			return fmt.Errorf("temporary share links are only supported for https tunnels")
		}
		if sess.ConnectionState == session.ConnectionStateStopped {
			return fmt.Errorf("tunnel %s is stopped; run `sealtun start %s` before rotating share links", sess.TunnelID, sess.TunnelID)
		}
		if sessionExpired(*sess, nowUTC()) {
			return fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
		}
		next := cloneAccessPolicy(sess.AccessPolicy)
		found := false
		for _, token := range next.TemporaryTokens {
			if token.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("temporary access link %q not found for tunnel %s", name, sess.TunnelID)
		}
		token, err := generateShareToken()
		if err != nil {
			return err
		}
		hash, err := accesspolicy.HashToken(token)
		if err != nil {
			return fmt.Errorf("share token: %w", err)
		}
		expiresAt := nowUTC().Add(ttl).UTC().Format(time.RFC3339)
		next.TemporaryTokens = replaceTemporaryToken(next.TemporaryTokens, session.TemporaryToken{
			Name:      name,
			TokenHash: hash,
			TTL:       ttl.String(),
			ExpiresAt: expiresAt,
		})
		payload = &shareCreatePayload{
			TunnelID:  sess.TunnelID,
			Name:      name,
			URL:       temporaryAccessURL(sharePublicHost(*sess), token),
			ExpiresAt: expiresAt,
		}
		if err := updateHTTPSAccessPolicy(ctx, sess, next); err != nil {
			if accessPolicyCommitted(err) {
				return err
			}
			payload = nil
			return err
		}
		return nil
	})
	if err != nil && payload == nil {
		return nil, err
	}
	return payload, err
}

type committedAccessPolicyError struct {
	err error
}

func (e committedAccessPolicyError) Error() string {
	return e.err.Error()
}

func (e committedAccessPolicyError) Unwrap() error {
	return e.err
}

func accessPolicyCommitted(err error) bool {
	var committed committedAccessPolicyError
	return errors.As(err, &committed)
}

var updateHTTPSAccessPolicy = func(ctx context.Context, sess *session.TunnelSession, policy *session.AccessPolicy) error {
	if strings.TrimSpace(sess.Secret) == "" {
		return fmt.Errorf("tunnel %s has no local secret; recreate it before updating access policy", sess.TunnelID)
	}
	policyChanged := accessPolicyChanged(sess.AccessPolicy, policy)
	mutatedAt := nowUTC().Add(-time.Second)
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return err
	}
	namespacedClient := client.WithNamespace(sess.Namespace)
	hosts, err := namespacedClient.EnsureTunnelWithOptions(ctx, sess.TunnelID, sess.Secret, sessionProtocol(*sess), sess.LocalPort, k8s.TunnelOptions{
		CustomDomain: sess.CustomDomain,
		SealosHost:   sessionSealosHostForDomain(*sess, namespacedClient.SealosHost(sess.TunnelID)),
		TargetURL:    sess.TargetURL,
		BasicAuth:    basicAuthToK8s(sess.BasicAuth),
		AccessPolicy: accessPolicyToK8s(policy),
		Resources:    resourcesToK8s(sess.ResourceConfig),
	})
	if err != nil {
		return fmt.Errorf("update remote access policy: %w", err)
	}
	sess.AccessPolicy = emptyAccessPolicyAsNil(policy)
	sess.Host = hosts.PublicHost
	sess.SealosHost = hosts.SealosHost
	sess.CustomDomain = hosts.CustomDomain
	sess.PublicPort = hosts.PublicPort
	if err := session.Update(*sess); err != nil {
		return fmt.Errorf("save updated session: %w", err)
	}
	readyCtx, cancelReady := context.WithTimeout(ctx, readyTimeout)
	defer cancelReady()
	if err := namespacedClient.WaitForReady(readyCtx, sess.TunnelID); err != nil {
		return committedAccessPolicyError{err: fmt.Errorf("access policy was updated, but rollout readiness was not confirmed: %w", err)}
	}
	if policyChanged {
		if err := waitForDaemonSessionAfter(sess.TunnelID, daemonConnectTimeout, mutatedAt); err != nil {
			return committedAccessPolicyError{err: fmt.Errorf("access policy was updated, but local daemon reconnection was not confirmed: %w", err)}
		}
	}
	return nil
}

func cloneAccessPolicy(policy *session.AccessPolicy) *session.AccessPolicy {
	if policy == nil {
		return &session.AccessPolicy{}
	}
	return &session.AccessPolicy{
		BearerTokenHashes: append([]string(nil), policy.BearerTokenHashes...),
		IPAllowlist:       append([]string(nil), policy.IPAllowlist...),
		IPDenylist:        append([]string(nil), policy.IPDenylist...),
		TemporaryTokens:   append([]session.TemporaryToken(nil), policy.TemporaryTokens...),
		RateLimit:         policy.RateLimit,
		Audit:             cloneSessionAuditConfig(policy.Audit),
	}
}

func cloneSessionAuditConfig(config *session.AuditConfig) *session.AuditConfig {
	if config == nil {
		return nil
	}
	return &session.AuditConfig{Enabled: config.Enabled}
}

func emptyAccessPolicyAsNil(policy *session.AccessPolicy) *session.AccessPolicy {
	if accesspolicy.Empty(accessPolicyToRuntime(policy)) {
		return nil
	}
	return policy
}

func replaceTemporaryToken(tokens []session.TemporaryToken, next session.TemporaryToken) []session.TemporaryToken {
	for i := range tokens {
		if tokens[i].Name == next.Name {
			tokens[i] = next
			return tokens
		}
	}
	return append(tokens, next)
}

func temporaryTokenNameExists(policy *session.AccessPolicy, name string) bool {
	if policy == nil {
		return false
	}
	for _, token := range policy.TemporaryTokens {
		if token.Name == name {
			return true
		}
	}
	return false
}

func generateShareToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate share token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func sharePublicHost(sess session.TunnelSession) string {
	return valueOr(sess.Host, sess.SealosHost)
}

func printShareCreate(cmd *cobra.Command, payload *shareCreatePayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Temporary Share")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Name: %s\n", payload.Name)
	fmt.Fprintf(out, "  URL: %s\n", payload.URL)
	fmt.Fprintf(out, "  Expires at: %s\n", payload.ExpiresAt)
	fmt.Fprintln(out, "  Note: this URL is shown only once because Sealtun stores only a token hash.")
}

func printShareList(cmd *cobra.Command, tunnelID string, items []shareListItem) {
	out := cmd.OutOrStdout()
	if len(items) == 0 {
		fmt.Fprintf(out, "No temporary access links found for tunnel %s.\n", tunnelID)
		return
	}
	fmt.Fprintf(out, "Temporary access links for tunnel %s\n", tunnelID)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tEXPIRES AT\tSTATUS")
	for _, item := range items {
		status := "active"
		if item.Expired {
			status = "expired"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", item.Name, item.ExpiresAt, status)
	}
	_ = w.Flush()
}
