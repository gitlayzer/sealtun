package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type domainPayload struct {
	TunnelID     string `json:"tunnelId"`
	PublicHost   string `json:"publicHost"`
	SealosHost   string `json:"sealosHost"`
	CustomDomain string `json:"customDomain,omitempty"`
	CNAME        string `json:"cname,omitempty"`
}

type domainVerifyPayload struct {
	TunnelID          string   `json:"tunnelId"`
	PublicHost        string   `json:"publicHost"`
	SealosHost        string   `json:"sealosHost"`
	CustomDomain      string   `json:"customDomain"`
	DNSCNAME          string   `json:"dnsCname,omitempty"`
	DNSReady          bool     `json:"dnsReady"`
	IngressReady      bool     `json:"ingressReady"`
	CertificateExists bool     `json:"certificateExists"`
	CertificateReady  bool     `json:"certificateReady"`
	CertificateSecret string   `json:"certificateSecret,omitempty"`
	Ready             bool     `json:"ready"`
	Warnings          []string `json:"warnings,omitempty"`
}

var domainJSON bool
var domainVerifyWait bool
var domainVerifyTimeout time.Duration
var lookupCNAME = net.DefaultResolver.LookupCNAME

var domainCmd = &cobra.Command{
	Use:   "domain",
	Short: "Manage custom domains for Sealtun tunnels",
	Long: `Manage custom domains for Sealtun tunnels.

Sealtun always keeps a Sealos-managed host as the tunnel control endpoint and
CNAME target. Point your custom domain to that Sealos host, then attach the
domain to the tunnel.`,
}

var domainSetCmd = &cobra.Command{
	Use:          "set [tunnel-id] [domain]",
	Short:        "Attach a custom domain to an existing tunnel",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		customDomain, err := validateCustomDomain(args[1])
		if err != nil {
			return err
		}
		if customDomain == "" {
			return fmt.Errorf("custom domain is required")
		}
		payload, err := configureSessionCustomDomain(cmd.Context(), args[0], customDomain)
		if payload != nil {
			if printErr := printDomainPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
		if err != nil {
			return err
		}
		return nil
	},
}

var domainClearCmd = &cobra.Command{
	Use:          "clear [tunnel-id]",
	Short:        "Remove a custom domain from an existing tunnel",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := clearSessionCustomDomain(cmd.Context(), args[0])
		if payload != nil {
			if printErr := printDomainPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
		if err != nil {
			return err
		}
		return nil
	},
}

var domainVerifyCmd = &cobra.Command{
	Use:          "verify [tunnel-id]",
	Short:        "Verify DNS, Ingress, and certificate readiness for a custom domain",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		var payload *domainVerifyPayload
		var err error
		if domainVerifyWait {
			if domainVerifyTimeout <= 0 {
				return fmt.Errorf("--timeout must be greater than 0 when --wait is set")
			}
			payload, err = waitForSessionDomain(cmd.Context(), args[0], domainVerifyTimeout)
		} else {
			payload, err = verifySessionDomain(cmd.Context(), args[0])
		}
		if payload != nil {
			if printErr := printDomainVerifyPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
		return domainVerifyResultError(payload, err)
	},
}

func init() {
	rootCmd.AddCommand(domainCmd)
	domainCmd.AddCommand(domainSetCmd)
	domainCmd.AddCommand(domainClearCmd)
	domainCmd.AddCommand(domainVerifyCmd)
	domainCmd.PersistentFlags().BoolVar(&domainJSON, "json", false, "Output domain details as JSON")
	domainVerifyCmd.Flags().BoolVar(&domainVerifyWait, "wait", false, "Wait until DNS, Ingress, and certificate are ready")
	domainVerifyCmd.Flags().DurationVar(&domainVerifyTimeout, "timeout", 5*time.Minute, "Maximum time to wait for domain readiness")
}

func configureSessionCustomDomain(parent context.Context, tunnelID, customDomain string) (*domainPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	original := *sess
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return nil, err
	}

	namespacedClient := client.WithNamespace(sess.Namespace)
	sealosHost := sessionSealosHostForDomain(*sess, namespacedClient.SealosHost(sess.TunnelID))
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := requireDomainCNAME(ctx, customDomain, sealosHost); err != nil {
		return nil, err
	}

	hosts, err := namespacedClient.ConfigureCustomDomain(ctx, sess.TunnelID, sealosHost, customDomain)
	if err != nil {
		return nil, fmt.Errorf("configure custom domain for tunnel %s: %w", sess.TunnelID, err)
	}

	sess.Host = hosts.PublicHost
	sess.SealosHost = hosts.SealosHost
	sess.CustomDomain = hosts.CustomDomain
	if err := session.Update(*sess); err != nil {
		if rollbackErr := restoreRemoteCustomDomain(context.Background(), namespacedClient, original); rollbackErr != nil {
			return nil, fmt.Errorf("update local session %s: %w; remote rollback failed: %v", sess.TunnelID, err, rollbackErr)
		}
		return nil, fmt.Errorf("update local session %s: %w", sess.TunnelID, err)
	}
	return domainPayloadFromSession(*sess), nil
}

func clearSessionCustomDomain(parent context.Context, tunnelID string) (*domainPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	original := *sess
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return nil, err
	}

	namespacedClient := client.WithNamespace(sess.Namespace)
	sealosHost := sessionSealosHostForDomain(*sess, namespacedClient.SealosHost(sess.TunnelID))
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	hosts, err := namespacedClient.ClearCustomDomain(ctx, sess.TunnelID, sealosHost)
	if err != nil && hosts.PublicHost == "" {
		return nil, fmt.Errorf("clear custom domain for tunnel %s: %w", sess.TunnelID, err)
	}

	sess.Host = hosts.PublicHost
	sess.SealosHost = hosts.SealosHost
	sess.CustomDomain = ""
	if err := session.Update(*sess); err != nil {
		if rollbackErr := restoreRemoteCustomDomain(context.Background(), namespacedClient, original); rollbackErr != nil {
			return nil, fmt.Errorf("update local session %s: %w; remote rollback failed: %v", sess.TunnelID, err, rollbackErr)
		}
		return nil, fmt.Errorf("update local session %s: %w", sess.TunnelID, err)
	}
	payload := domainPayloadFromSession(*sess)
	if err != nil {
		return payload, fmt.Errorf("clear custom domain for tunnel %s: %w", sess.TunnelID, err)
	}
	return payload, nil
}

func restoreRemoteCustomDomain(parent context.Context, client *k8s.Client, sess session.TunnelSession) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	sealosHost := sessionSealosHostForDomain(sess, client.SealosHost(sess.TunnelID))
	if sess.CustomDomain != "" {
		_, err := client.ConfigureCustomDomain(ctx, sess.TunnelID, sealosHost, sess.CustomDomain)
		return err
	}
	_, err := client.ClearCustomDomain(ctx, sess.TunnelID, sealosHost)
	return err
}

func domainPayloadFromSession(sess session.TunnelSession) *domainPayload {
	payload := &domainPayload{
		TunnelID:     sess.TunnelID,
		PublicHost:   sess.Host,
		SealosHost:   sessionSealosHostForDomain(sess, ""),
		CustomDomain: sess.CustomDomain,
	}
	if sess.CustomDomain != "" && payload.SealosHost != "" {
		payload.CNAME = fmt.Sprintf("%s -> %s", sess.CustomDomain, payload.SealosHost)
	}
	return payload
}

func printDomainPayload(cmd *cobra.Command, payload *domainPayload) error {
	if domainJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Tunnel %s domain configuration\n", payload.TunnelID)
	fmt.Fprintf(out, "  Public host: %s\n", payload.PublicHost)
	fmt.Fprintf(out, "  Sealos host: %s\n", payload.SealosHost)
	if payload.CustomDomain != "" {
		fmt.Fprintf(out, "  Custom domain: %s\n", payload.CustomDomain)
		fmt.Fprintf(out, "  DNS: create CNAME %s -> %s\n", payload.CustomDomain, payload.SealosHost)
		fmt.Fprintln(out, "  Note: wait for DNS propagation and certificate issuance before relying on HTTPS.")
	} else {
		fmt.Fprintln(out, "  Custom domain: disabled")
	}
	return nil
}

func verifySessionDomain(parent context.Context, tunnelID string) (*domainVerifyPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	if sess.CustomDomain == "" {
		return nil, fmt.Errorf("tunnel %s has no custom domain configured", sess.TunnelID)
	}

	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	return verifyDomainForSession(ctx, *sess), nil
}

func waitForSessionDomain(parent context.Context, tunnelID string, timeout time.Duration) (*domainVerifyPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	if sess.CustomDomain == "" {
		return nil, fmt.Errorf("tunnel %s has no custom domain configured", sess.TunnelID)
	}
	return waitForDomainReady(parent, *sess, timeout)
}

func domainVerifyResultError(payload *domainVerifyPayload, err error) error {
	if err != nil {
		return err
	}
	if payload != nil && !payload.Ready {
		return fmt.Errorf("custom domain %s is not ready", payload.CustomDomain)
	}
	return nil
}

func waitForDomainReady(parent context.Context, sess session.TunnelSession, timeout time.Duration) (*domainVerifyPayload, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("custom domain readiness timeout must be greater than 0")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var last *domainVerifyPayload
	for {
		checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
		payload := verifyDomainForSession(checkCtx, sess)
		checkCancel()
		last = payload
		if payload.Ready {
			return payload, nil
		}

		select {
		case <-ctx.Done():
			return last, fmt.Errorf("custom domain %s is not ready before timeout %s", sess.CustomDomain, timeout)
		case <-ticker.C:
		}
	}
}

func verifyDomainForSession(ctx context.Context, sess session.TunnelSession) *domainVerifyPayload {
	sealosHost := sessionSealosHostForDomain(sess, "")
	payload := &domainVerifyPayload{
		TunnelID:     sess.TunnelID,
		PublicHost:   sess.Host,
		SealosHost:   sealosHost,
		CustomDomain: sess.CustomDomain,
	}

	if payload.SealosHost == "" {
		payload.Warnings = append(payload.Warnings, "Sealos CNAME target is missing from the local session; reconfigure the domain or recreate the tunnel")
	} else {
		cname, dnsReady, dnsWarning := verifyDomainCNAME(ctx, sess.CustomDomain, payload.SealosHost)
		payload.DNSCNAME = cname
		payload.DNSReady = dnsReady
		if dnsWarning != "" {
			payload.Warnings = append(payload.Warnings, dnsWarning)
		}
	}

	remote, err := collectRemoteDiagnosticsWithContext(ctx, sess)
	if err != nil {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote certificate diagnostics unavailable: %v", err))
	} else {
		payload.IngressReady = remote.Ingress.Exists &&
			domainListContains(remote.Ingress.Hosts, sess.CustomDomain) &&
			domainListContains(remote.Ingress.TLSHosts, sess.CustomDomain)
		if !payload.IngressReady {
			payload.Warnings = append(payload.Warnings, "remote ingress is not ready for the custom domain")
		}

		if remote.Certificate != nil {
			payload.CertificateExists = remote.Certificate.Exists
			payload.CertificateReady = remote.Certificate.Ready && domainListContains(remote.Certificate.DNSNames, sess.CustomDomain)
			payload.CertificateSecret = remote.Certificate.SecretName
		}
		if remote.Certificate == nil || !remote.Certificate.Exists {
			payload.Warnings = append(payload.Warnings, "custom domain certificate is missing")
		} else if !domainListContains(remote.Certificate.DNSNames, sess.CustomDomain) {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("custom domain certificate does not include DNS name %s", sess.CustomDomain))
		} else if !remote.Certificate.Ready {
			payload.Warnings = append(payload.Warnings, "custom domain certificate is not ready")
		}
	}

	payload.Ready = payload.DNSReady && payload.IngressReady && payload.CertificateReady
	return payload
}

func verifyDomainCNAME(ctx context.Context, customDomain, sealosHost string) (string, bool, string) {
	cname, err := lookupCNAME(ctx, customDomain)
	if err != nil {
		return "", false, fmt.Sprintf("DNS CNAME lookup failed for %s: %v", customDomain, err)
	}

	got := normalizeDNSName(cname)
	want := normalizeDNSName(sealosHost)
	if got != want {
		return got, false, fmt.Sprintf("DNS CNAME for %s points to %s, expected %s", customDomain, got, want)
	}
	return got, true, ""
}

func requireDomainCNAME(ctx context.Context, customDomain, sealosHost string) error {
	if sealosHost == "" {
		return fmt.Errorf("sealos CNAME target is missing; recreate the tunnel or inspect the session before attaching a custom domain")
	}
	_, ready, warning := verifyDomainCNAME(ctx, customDomain, sealosHost)
	if ready {
		return nil
	}
	if warning == "" {
		warning = fmt.Sprintf("DNS CNAME for %s does not point to %s", customDomain, sealosHost)
	}
	return fmt.Errorf("custom domain DNS is not verified: %s; create CNAME %s -> %s and retry", warning, customDomain, sealosHost)
}

func waitForDomainCNAMEReady(parent context.Context, customDomain, sealosHost string, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("custom domain DNS timeout must be greater than 0")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
		err := requireDomainCNAME(checkCtx, customDomain, sealosHost)
		checkCancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w before timeout %s", lastErr, timeout)
		case <-ticker.C:
		}
	}
}

func normalizeDNSName(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func domainListContains(values []string, want string) bool {
	normalizedWant := normalizeDNSName(want)
	for _, value := range values {
		if normalizeDNSName(value) == normalizedWant {
			return true
		}
	}
	return false
}

func printDomainVerifyPayload(cmd *cobra.Command, payload *domainVerifyPayload) error {
	if domainJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Tunnel %s custom domain verification\n", payload.TunnelID)
	fmt.Fprintf(out, "  Custom domain: %s\n", payload.CustomDomain)
	fmt.Fprintf(out, "  Sealos host: %s\n", payload.SealosHost)
	fmt.Fprintf(out, "  DNS CNAME: %s\n", valueOr(payload.DNSCNAME, "unavailable"))
	fmt.Fprintf(out, "  DNS ready: %s\n", yesNo(payload.DNSReady))
	fmt.Fprintf(out, "  Ingress ready: %s\n", yesNo(payload.IngressReady))
	fmt.Fprintf(out, "  Certificate: exists=%s ready=%s", yesNo(payload.CertificateExists), yesNo(payload.CertificateReady))
	if payload.CertificateSecret != "" {
		fmt.Fprintf(out, " secret=%s", payload.CertificateSecret)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Ready: %s\n", yesNo(payload.Ready))
	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
	return nil
}

func validateCustomDomain(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/:@") {
		return "", fmt.Errorf("invalid custom domain %q: provide a hostname only, not a URL", value)
	}
	if len(value) > 253 {
		return "", fmt.Errorf("invalid custom domain %q: hostname is too long", value)
	}
	if net.ParseIP(value) != nil {
		return "", fmt.Errorf("invalid custom domain %q: custom domain must be a DNS hostname, not an IP address", value)
	}
	if !strings.Contains(value, ".") {
		return "", fmt.Errorf("invalid custom domain %q: custom domain must contain at least two labels", value)
	}
	labelPattern := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, label := range strings.Split(value, ".") {
		if !labelPattern.MatchString(label) {
			return "", fmt.Errorf("invalid custom domain %q: label %q is not DNS compatible", value, label)
		}
	}
	return value, nil
}
