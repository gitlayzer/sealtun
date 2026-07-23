package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type policyShowPayload struct {
	TunnelID        string                `json:"tunnelId"`
	BearerTokens    int                   `json:"bearerTokens"`
	IPAllowlist     []string              `json:"ipAllowlist,omitempty"`
	IPDenylist      []string              `json:"ipDenylist,omitempty"`
	TemporaryLinks  []policyTemporaryLink `json:"temporaryLinks,omitempty"`
	RateLimit       string                `json:"rateLimit,omitempty"`
	AuditEnabled    bool                  `json:"auditEnabled"`
	HTTPSOnlyNotice string                `json:"httpsOnlyNotice,omitempty"`
}

type policyTemporaryLink struct {
	Name      string `json:"name,omitempty"`
	ExpiresAt string `json:"expiresAt"`
	Expired   bool   `json:"expired"`
}

type policyAuditPayload struct {
	TunnelID string                    `json:"tunnelId"`
	Events   []accesspolicy.AuditEvent `json:"events"`
	Total    int                       `json:"total"`
}

var policyShowJSON bool
var policySetRateLimit string
var policySetClearRateLimit bool
var policySetAudit bool
var policySetNoAudit bool
var policyAuditSince time.Duration
var policyAuditLimit int
var policyAuditJSON bool

const controlEndpointRetryTimeout = 20 * time.Second
const controlEndpointRetryInterval = time.Second

type remoteEndpointStatusError struct {
	StatusCode int
	Status     string
}

func (e remoteEndpointStatusError) Error() string {
	return fmt.Sprintf("remote endpoint returned %s", e.Status)
}

var policyCmd = &cobra.Command{
	Use:          "policy",
	Short:        "Manage HTTPS access policy and audit data",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var policyShowCmd = &cobra.Command{
	Use:          "show [tunnel-id]",
	Short:        "Show HTTPS access policy without revealing secrets",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := showPolicyWithContext(cmd.Context(), args[0], nowUTC())
		if err != nil {
			return err
		}
		if policyShowJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printPolicyShow(cmd, payload)
		return nil
	},
}

var policySetCmd = &cobra.Command{
	Use:          "set [tunnel-id]",
	Short:        "Update HTTPS access policy settings",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(policySetRateLimit) == "" && !policySetClearRateLimit && !policySetAudit && !policySetNoAudit {
			return fmt.Errorf("set at least one policy option, e.g. --rate-limit 60/m or --audit")
		}
		if strings.TrimSpace(policySetRateLimit) != "" && policySetClearRateLimit {
			return fmt.Errorf("--rate-limit and --clear-rate-limit cannot be combined")
		}
		if policySetAudit && policySetNoAudit {
			return fmt.Errorf("--audit and --no-audit cannot be combined")
		}
		payload, err := setPolicy(cmd.Context(), args[0], policySetRateLimit, policySetClearRateLimit, policySetAudit, policySetNoAudit)
		if err != nil {
			return err
		}
		printPolicyShow(cmd, payload)
		return nil
	},
}

var policyAuditCmd = &cobra.Command{
	Use:          "audit [tunnel-id]",
	Short:        "Show HTTPS access audit events",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := collectPolicyAudit(cmd.Context(), args[0], policyAuditSince, policyAuditLimit)
		if err != nil {
			return err
		}
		if policyAuditJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printPolicyAudit(cmd, payload)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(policyCmd)
	policyCmd.AddCommand(policyShowCmd, policySetCmd, policyAuditCmd)
	policyShowCmd.Flags().BoolVar(&policyShowJSON, "json", false, "Output access policy as JSON")
	policySetCmd.Flags().StringVar(&policySetRateLimit, "rate-limit", "", "Set HTTPS public traffic rate limit, e.g. 60/m or 1000/h")
	policySetCmd.Flags().BoolVar(&policySetClearRateLimit, "clear-rate-limit", false, "Clear the HTTPS public traffic rate limit")
	policySetCmd.Flags().BoolVar(&policySetAudit, "audit", false, "Enable HTTPS access audit")
	policySetCmd.Flags().BoolVar(&policySetNoAudit, "no-audit", false, "Disable HTTPS access audit")
	policyAuditCmd.Flags().DurationVar(&policyAuditSince, "since", 10*time.Minute, "Only return audit events newer than this duration")
	policyAuditCmd.Flags().IntVar(&policyAuditLimit, "limit", 200, "Maximum audit events to return")
	policyAuditCmd.Flags().BoolVar(&policyAuditJSON, "json", false, "Output audit events as JSON")
}

func showPolicy(tunnelID string, now time.Time) (*policyShowPayload, error) {
	return showPolicyWithContext(context.Background(), tunnelID, now)
}

func showPolicyWithContext(ctx context.Context, tunnelID string, now time.Time) (*policyShowPayload, error) {
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	return policyShowPayloadFromSession(*sess, now)
}

func setPolicy(ctx context.Context, tunnelID, rateLimit string, clearRateLimit, auditEnabled, auditDisabled bool) (*policyShowPayload, error) {
	var payload *policyShowPayload
	err := withTunnelOperationLock(tunnelID, func() error {
		sess, err := findSessionRefreshed(ctx, tunnelID)
		if err != nil {
			return err
		}
		if err := validateHTTPSPolicyTarget(*sess); err != nil {
			return err
		}
		next, err := applyPolicySettings(sess.AccessPolicy, rateLimit, clearRateLimit, auditEnabled, auditDisabled)
		if err != nil {
			return err
		}
		if err := updateHTTPSAccessPolicy(ctx, sess, emptyAccessPolicyAsNil(next)); err != nil {
			return err
		}
		payload, err = policyShowPayloadFromSession(*sess, nowUTC())
		return err
	})
	return payload, err
}

func applyPolicySettings(policy *session.AccessPolicy, rateLimit string, clearRateLimit, auditEnabled, auditDisabled bool) (*session.AccessPolicy, error) {
	if strings.TrimSpace(rateLimit) != "" && clearRateLimit {
		return nil, fmt.Errorf("rate limit and clear rate limit cannot be combined")
	}
	next := cloneAccessPolicy(policy)
	if clearRateLimit {
		next.RateLimit = ""
	} else if strings.TrimSpace(rateLimit) != "" {
		spec, err := accesspolicy.ParseRateLimit(rateLimit)
		if err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		next.RateLimit = spec.Raw
	}
	if auditEnabled {
		next.Audit = &session.AuditConfig{Enabled: true}
	}
	if auditDisabled {
		next.Audit = nil
	}
	return next, nil
}

func collectPolicyAudit(ctx context.Context, tunnelID string, since time.Duration, limit int) (*policyAuditPayload, error) {
	if since < 0 {
		return nil, fmt.Errorf("since must be a non-negative duration")
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("limit must be between 1 and 1000")
	}
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	if err := validateHTTPSPolicyTargetForRead(*sess); err != nil {
		return nil, err
	}
	payload, err := fetchServerAudit(ctx, *sess, since, limit)
	if err != nil {
		return nil, err
	}
	return &policyAuditPayload{TunnelID: sess.TunnelID, Events: payload.Events, Total: payload.Total}, nil
}

func fetchServerAudit(ctx context.Context, sess session.TunnelSession, since time.Duration, limit int) (*accesspolicy.AuditPayload, error) {
	host, err := normalizePublicHostname(sessionControlHost(sess))
	if err != nil {
		return nil, fmt.Errorf("invalid session audit host: %w", err)
	}
	if sess.Secret == "" {
		return nil, fmt.Errorf("session secret is unavailable")
	}
	client := newMetricsHTTPClient()
	auditURL := (&url.URL{Scheme: "https", Host: host, Path: "/_sealtun/audit", RawQuery: url.Values{
		"since": []string{since.String()},
		"limit": []string{fmt.Sprintf("%d", limit)},
	}.Encode()}).String()
	return fetchServerAuditURLWithRetry(ctx, client, auditURL, sess.Secret, controlEndpointRetryTimeout, controlEndpointRetryInterval)
}

func fetchServerAuditURLWithRetry(ctx context.Context, client *http.Client, auditURL, secret string, retryFor, retryEvery time.Duration) (*accesspolicy.AuditPayload, error) {
	deadline := time.Now().Add(retryFor)
	var lastErr error
	waitTimer := time.NewTimer(time.Hour)
	if !waitTimer.Stop() {
		<-waitTimer.C
	}
	defer waitTimer.Stop()
	for {
		payload, err := fetchServerAuditURLOnce(ctx, client, auditURL, secret)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if retryFor <= 0 || !retryableControlEndpointError(ctx, err) {
			return nil, err
		}
		wait := retryEvery
		if wait <= 0 {
			wait = controlEndpointRetryInterval
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return nil, fmt.Errorf("remote audit endpoint unavailable after %s: %w", retryFor.Round(time.Second), lastErr)
		} else if remaining < wait {
			wait = remaining
		}
		waitTimer.Reset(wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-waitTimer.C:
		}
	}
}

func fetchServerAuditURLOnce(ctx context.Context, client *http.Client, auditURL, secret string) (*accesspolicy.AuditPayload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, auditURL, nil) // #nosec G107 -- host is validated as a DNS hostname before constructing the URL.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, remoteEndpointStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	var payload accesspolicy.AuditPayload
	if err := decodePolicyAuditJSON(resp.Body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func retryableControlEndpointError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var statusErr remoteEndpointStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == http.StatusBadGateway ||
			statusErr.StatusCode == http.StatusServiceUnavailable ||
			statusErr.StatusCode == http.StatusGatewayTimeout
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func decodePolicyAuditJSON(r io.Reader, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r, metricsResponseMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > metricsResponseMaxBytes {
		return fmt.Errorf("audit response exceeds %d bytes", metricsResponseMaxBytes)
	}
	return json.Unmarshal(body, v)
}

func validateHTTPSPolicyTarget(sess session.TunnelSession) error {
	if err := validateHTTPSPolicyTargetForRead(sess); err != nil {
		return err
	}
	if sess.ConnectionState == session.ConnectionStateStopped {
		return fmt.Errorf("tunnel %s is stopped; run `sealtun start %s` before updating access policy", sess.TunnelID, sess.TunnelID)
	}
	if sessionExpired(sess, nowUTC()) {
		return fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
	}
	return nil
}

func validateHTTPSPolicyTargetForRead(sess session.TunnelSession) error {
	if !sessionUsesHTTP(sess) {
		return fmt.Errorf("access policy is only supported for https tunnels")
	}
	return nil
}

func policyShowPayloadFromSession(sess session.TunnelSession, now time.Time) (*policyShowPayload, error) {
	if err := validateHTTPSPolicyTargetForRead(sess); err != nil {
		return nil, err
	}
	payload := &policyShowPayload{
		TunnelID:        sess.TunnelID,
		HTTPSOnlyNotice: "Access policy applies only to HTTPS public traffic; SSH/TCP NodePort traffic is unchanged.",
	}
	if sess.AccessPolicy == nil {
		return payload, nil
	}
	payload.BearerTokens = len(sess.AccessPolicy.BearerTokenHashes)
	payload.IPAllowlist = append([]string(nil), sess.AccessPolicy.IPAllowlist...)
	payload.IPDenylist = append([]string(nil), sess.AccessPolicy.IPDenylist...)
	payload.RateLimit = sess.AccessPolicy.RateLimit
	payload.AuditEnabled = sess.AccessPolicy.Audit != nil && sess.AccessPolicy.Audit.Enabled
	for _, token := range sess.AccessPolicy.TemporaryTokens {
		item := policyTemporaryLink{Name: token.Name, ExpiresAt: token.ExpiresAt}
		if expiresAt, err := time.Parse(time.RFC3339, token.ExpiresAt); err == nil {
			item.Expired = !now.Before(expiresAt)
		} else {
			item.Expired = true
		}
		payload.TemporaryLinks = append(payload.TemporaryLinks, item)
	}
	return payload, nil
}

func printPolicyShow(cmd *cobra.Command, payload *policyShowPayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Access Policy")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Bearer tokens: %d\n", payload.BearerTokens)
	if len(payload.IPAllowlist) > 0 {
		fmt.Fprintf(out, "  IP allowlist: %s\n", strings.Join(payload.IPAllowlist, ", "))
	}
	if len(payload.IPDenylist) > 0 {
		fmt.Fprintf(out, "  IP denylist: %s\n", strings.Join(payload.IPDenylist, ", "))
	}
	if payload.RateLimit != "" {
		fmt.Fprintf(out, "  Rate limit: %s\n", payload.RateLimit)
	}
	fmt.Fprintf(out, "  Audit: %s\n", yesNo(payload.AuditEnabled))
	if len(payload.TemporaryLinks) > 0 {
		fmt.Fprintln(out, "  Temporary links:")
		for _, link := range payload.TemporaryLinks {
			status := "active"
			if link.Expired {
				status = "expired"
			}
			fmt.Fprintf(out, "    - %s expires=%s status=%s\n", valueOr(link.Name, "-"), link.ExpiresAt, status)
		}
	}
	if payload.HTTPSOnlyNotice != "" {
		fmt.Fprintf(out, "  Note: %s\n", payload.HTTPSOnlyNotice)
	}
}

func printPolicyAudit(cmd *cobra.Command, payload *policyAuditPayload) {
	out := cmd.OutOrStdout()
	if len(payload.Events) == 0 {
		fmt.Fprintf(out, "No access audit events found for tunnel %s.\n", payload.TunnelID)
		return
	}
	fmt.Fprintf(out, "Access audit for tunnel %s (%d event(s), total matched %d)\n", payload.TunnelID, len(payload.Events), payload.Total)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tDECISION\tREASON\tSTATUS\tCLIENT IP\tMETHOD\tPATH")
	for _, event := range payload.Events {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n", event.Time, event.Decision, event.Reason, event.Status, valueOr(event.ClientIP, "-"), valueOr(event.Method, "-"), valueOr(event.Path, "-"))
	}
	_ = w.Flush()
}
