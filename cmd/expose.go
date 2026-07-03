package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/session"
	"github.com/labring/sealtun/pkg/tunnel"
	"github.com/spf13/cobra"
)

var exposeCmd = &cobra.Command{
	Use:   "expose [port]",
	Short: "Expose a local port or HTTP upstream target to the internet",
	Long: `Expose a local port or HTTP upstream target to the internet via Sealos Cloud.
This command automatically deploys a tunnel server on Sealos, obtains a public URL,
and establishes a secure connection to forward traffic to the configured target.`,
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runExpose(cmd, args)
	},
}

var runExpose = runExposeCommand

func runExposeCommand(cmd *cobra.Command, args []string) error {
	localPort, targetURL, err := resolveExposeTarget(args, exposeTarget)
	if err != nil {
		return err
	}
	normalizedCustomDomain, err := validateCustomDomain(customDomain)
	if err != nil {
		return err
	}
	if waitDomain {
		if normalizedCustomDomain == "" {
			return fmt.Errorf("--wait-domain requires --domain")
		}
		if domainWaitTimeout <= 0 {
			return fmt.Errorf("--domain-timeout must be greater than 0 when --wait-domain is set")
		}
	}
	if err := validateProtocol(protocol); err != nil {
		return err
	}
	protocol = tunnelprotocol.Normalize(protocol)
	if strings.TrimSpace(exposeTarget) != "" && !tunnelprotocol.IsHTTP(protocol) {
		return fmt.Errorf("--target is only supported for https tunnels")
	}
	if targetTLSInsecureSkipVerify && strings.TrimSpace(exposeTarget) == "" {
		return fmt.Errorf("--target-insecure-skip-verify requires --target with an https URL")
	}
	if err := validateTargetTLSOptions(targetURL, targetTLSInsecureSkipVerify); err != nil {
		return err
	}
	if !tunnelprotocol.IsHTTP(protocol) {
		if normalizedCustomDomain != "" || waitDomain {
			return fmt.Errorf("--domain and --wait-domain are only supported for https tunnels")
		}
		if basicAuthCredential != "" || basicAuthUser != "" || basicAuthPassword != "" || basicAuthPasswordEnv != "" {
			return fmt.Errorf("basic auth flags are only supported for https tunnels")
		}
		if bearerToken != "" || bearerTokenEnv != "" || len(ipAllowlist) > 0 || len(ipDenylist) > 0 || temporaryAccessToken != "" || temporaryAccessTokenEnv != "" || accessRateLimit != "" || accessAuditEnabled {
			return fmt.Errorf("access policy flags are only supported for https tunnels")
		}
	}

	// 1. Check if logged in.
	authData, err := auth.LoadAuthData()
	if err != nil {
		return fmt.Errorf("not logged in. Please run 'sealtun login' first: %w", err)
	}

	sealtunDir, err := auth.GetSealosDir()
	if err != nil {
		return err
	}
	kcPath := filepath.Join(sealtunDir, "kubeconfig")
	kubeconfig, err := auth.ActiveKubeconfig()
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig: %w", err)
	}

	// 2. Generate tunnel ID & secret.
	tunnelID := newTunnelID()
	secret := uuid.New().String()
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "[+] Preparing tunnel %s...\n", tunnelID)

	// 3. Create K8s Resources (Deployment, Service, Ingress)
	k8sClient, err := k8s.NewClient(kcPath, authData)
	if err != nil {
		return fmt.Errorf("failed to init k8s client: %w", err)
	}

	ctx := cmd.Context()
	if err := recoverStaleSessions(ctx, out); err != nil {
		return err
	}

	warnPlaintextPasswordFlag(basicAuthCredential, basicAuthPassword)
	basicAuthConfig, err := resolveBasicAuth(basicAuthInput{
		Credential:  basicAuthCredential,
		Username:    basicAuthUser,
		Password:    basicAuthPassword,
		PasswordEnv: basicAuthPasswordEnv,
	}, getenv)
	if err != nil {
		return err
	}
	accessPolicyConfig, err := resolveAccessPolicy(accessPolicyInput{
		BearerToken:       bearerToken,
		BearerTokenEnv:    bearerTokenEnv,
		IPAllowlist:       ipAllowlist,
		IPDenylist:        ipDenylist,
		TemporaryToken:    temporaryAccessToken,
		TemporaryTokenEnv: temporaryAccessTokenEnv,
		TemporaryTTL:      temporaryAccessTTL,
		TemporaryName:     "default",
		RateLimit:         accessRateLimit,
		AuditEnabled:      accessAuditEnabled,
	}, nowUTC(), getenv)
	if err != nil {
		return err
	}

	hosts, err := k8sClient.EnsureTunnelWithOptions(ctx, tunnelID, secret, protocol, localPort, k8s.TunnelOptions{
		BasicAuth:    basicAuthToK8s(basicAuthConfig),
		AccessPolicy: accessPolicyToK8s(accessPolicyConfig),
		TargetURL:    targetURL,
	})
	if err != nil {
		return fmt.Errorf("failed to provision tunnel on Sealos: %w", err)
	}
	cleanupTarget := k8sClient.WithNamespace(k8sClient.Namespace())
	rollback := true
	defer func() {
		if rollback {
			cleanupTunnel(cleanupTarget, tunnelID)
		}
	}()

	sessionRecord := session.TunnelSession{
		TunnelID:        tunnelID,
		Region:          authData.Region,
		Namespace:       k8sClient.Namespace(),
		Kubeconfig:      kubeconfig,
		Protocol:        protocol,
		Host:            hosts.PublicHost,
		SealosHost:      hosts.SealosHost,
		CustomDomain:    hosts.CustomDomain,
		PublicPort:      hosts.PublicPort,
		LocalPort:       localPort,
		TargetURL:       targetURL,
		TargetTLS:       sessionTargetTLSConfig(targetTLSInsecureSkipVerify),
		Secret:          secret,
		BasicAuth:       basicAuthConfig,
		AccessPolicy:    accessPolicyConfig,
		ResourceConfig:  resourcesFromK8s(k8s.DefaultResourceConfig()),
		Mode:            "foreground",
		PID:             os.Getpid(),
		ConnectionState: session.ConnectionStatePending,
		Resources: []string{
			fmt.Sprintf("sealtun-%s", tunnelID),
		},
	}
	if err := session.Save(sessionRecord); err != nil {
		return fmt.Errorf("failed to persist tunnel session: %w", err)
	}

	if protocol == tunnelprotocol.SSH {
		endpoint := endpointDisplay(protocol, hosts.PublicHost, hosts.SealosHost, hosts.PublicPort)
		fmt.Fprintf(out, "[+] Public SSH host: %s\n", endpoint.Host)
		fmt.Fprintf(out, "[+] Public SSH port: %d\n", endpoint.Port)
		fmt.Fprintf(out, "[+] Connect with: %s\n", endpoint.Command)
		fmt.Fprintf(out, "[+] Local target: localhost:%s\n", localPort)
	} else if protocol == tunnelprotocol.TCP {
		endpoint := endpointDisplay(protocol, hosts.PublicHost, hosts.SealosHost, hosts.PublicPort)
		fmt.Fprintf(out, "[+] Public TCP host: %s\n", endpoint.Host)
		fmt.Fprintf(out, "[+] Public TCP port: %d\n", endpoint.Port)
		fmt.Fprintf(out, "[+] Public TCP endpoint: %s\n", endpointLabel(protocol, hosts.PublicHost, hosts.SealosHost, hosts.PublicPort))
		fmt.Fprintf(out, "[+] Local target: localhost:%s\n", localPort)
	} else {
		fmt.Fprintf(out, "[+] Public URL: %s\n", endpointLabel(protocol, hosts.PublicHost, hosts.SealosHost, hosts.PublicPort))
		fmt.Fprintf(out, "[+] Target: %s\n", targetURL)
		if targetTLSInsecureSkipVerify {
			fmt.Fprintf(out, "[!] Target TLS certificate verification is disabled for this upstream.\n")
		}
	}
	if basicAuthConfig != nil && basicAuthConfig.Enabled {
		fmt.Fprintf(out, "[+] Basic Auth enabled for public traffic as user %q.\n", basicAuthConfig.Username)
	}
	if accessPolicyConfig != nil {
		printAccessPolicySummary(out, accessPolicyConfig)
		if temporaryAccessToken != "" || temporaryAccessTokenEnv != "" {
			token, tokenErr := resolveSecretValue(temporaryAccessToken, temporaryAccessTokenEnv, "temporary access token", getenv)
			if tokenErr == nil {
				fmt.Fprintf(out, "[+] Temporary access URL: %s\n", temporaryAccessURL(hosts.PublicHost, token))
			}
		}
	}
	if normalizedCustomDomain != "" {
		fmt.Fprintf(out, "[+] Requested custom domain: %s\n", normalizedCustomDomain)
		fmt.Fprintf(out, "[+] Sealos CNAME target: %s\n", hosts.SealosHost)
		fmt.Fprintf(out, "[+] Configure DNS: CNAME %s -> %s\n", normalizedCustomDomain, hosts.SealosHost)
		if !waitDomain {
			fmt.Fprintf(out, "[+] After DNS is ready, attach it with: sealtun domain set %s %s\n", tunnelID, normalizedCustomDomain)
		}
	}
	fmt.Fprintf(out, "[+] Waiting for tunnel server pod to be ready...\n")

	readyCtx, cancelReady := context.WithTimeout(ctx, readyTimeout)
	defer cancelReady()
	if err := k8sClient.WaitForReady(readyCtx, tunnelID); err != nil {
		return fmt.Errorf("timed out waiting for tunnel server: %w", err)
	}
	if waitDomain && normalizedCustomDomain != "" {
		fmt.Fprintf(out, "[+] Waiting for custom domain DNS, Ingress, and certificate readiness (timeout %s)...\n", domainWaitTimeout)
		if err := waitForDomainCNAMEReady(ctx, normalizedCustomDomain, hosts.SealosHost, domainWaitTimeout); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] Custom domain DNS is not ready yet: %v\n", err)
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] Tunnel will continue to run on the Sealos host. Re-run `sealtun domain set %s %s` after CNAME is ready.\n", tunnelID, normalizedCustomDomain)
		} else if payload, err := configureSessionCustomDomain(ctx, tunnelID, normalizedCustomDomain); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] Custom domain could not be attached: %v\n", err)
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] Tunnel will continue to run on the Sealos host. Recheck DNS and retry `sealtun domain set %s %s`.\n", tunnelID, normalizedCustomDomain)
		} else if current, err := session.Get(tunnelID); err == nil {
			sessionRecord = *current
			if printErr := printDomainPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
	}
	if waitDomain && sessionRecord.CustomDomain != "" {
		payload, err := waitForDomainReady(ctx, sessionRecord, domainWaitTimeout)
		if payload != nil {
			_ = printDomainVerifyPayload(cmd, payload)
		}
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] Custom domain is not ready yet: %v\n", err)
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] Tunnel will continue to run. Recheck later with `sealtun domain verify %s --wait`.\n", tunnelID)
		}
	}

	if !foreground {
		sessionRecord.Mode = "daemon"
		sessionRecord.PID = 0
		sessionRecord.ConnectionState = session.ConnectionStatePending
		if err := session.Update(sessionRecord); err != nil {
			return fmt.Errorf("failed to update tunnel session for daemon mode: %w", err)
		}
		if err := ensureDaemonRunningFn(); err != nil {
			return fmt.Errorf("failed to start local daemon: %w", err)
		}
		if err := waitForDaemonSession(tunnelID, daemonConnectTimeout); err != nil {
			return err
		}

		rollback = false
		fmt.Fprintf(out, "[+] Tunnel is running in the background via the local daemon.\n")
		fmt.Fprintf(out, "[+] Use `sealtun list` or `sealtun inspect %s` to view it later.\n", tunnelID)
		return nil
	}

	ctx, stop := signal.NotifyContext(ctx, signalCleanupSignals()...)
	defer stop()
	rollback = false
	defer cleanupTunnelForCurrentOwner(cleanupTarget, tunnelID)

	// 4 & 5. Connect via WebSocket
	wsURL := fmt.Sprintf("wss://%s/_sealtun/ws", sessionControlHost(sessionRecord))
	return dialForegroundTunnelWithRetry(ctx, foregroundConnectTimeout, foregroundConnectRetryInterval, func(dialCtx context.Context, onConnected func()) error {
		return tunnel.DialServerAndServeTargetWithOptions(dialCtx, wsURL, secret, localPort, targetURL, protocol, targetOptionsForSession(sessionRecord), func() {
			onConnected()
			current, err := session.Get(tunnelID)
			if err != nil {
				return
			}
			if shouldPreserveStoppedSession(current) {
				return
			}
			current.ConnectionState = session.ConnectionStateConnected
			current.LastError = ""
			current.LastConnectedAt = time.Now().Format(time.RFC3339)
			_ = session.Update(*current)
		})
	})
}

func newTunnelID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
}

var protocol string
var readyTimeout time.Duration
var foreground bool
var customDomain string
var waitDomain bool
var domainWaitTimeout time.Duration
var basicAuthCredential string
var basicAuthUser string
var basicAuthPassword string
var basicAuthPasswordEnv string
var bearerToken string
var bearerTokenEnv string
var ipAllowlist []string
var ipDenylist []string
var temporaryAccessToken string
var temporaryAccessTokenEnv string
var temporaryAccessTTL time.Duration
var accessRateLimit string
var accessAuditEnabled bool
var exposeTarget string
var targetTLSInsecureSkipVerify bool

const daemonConnectTimeout = 60 * time.Second
const daemonConnectionStability = 2 * time.Second
const foregroundConnectTimeout = 60 * time.Second
const foregroundConnectRetryInterval = 2 * time.Second
const tunnelCleanupTimeout = 30 * time.Second

type foregroundDialFunc func(context.Context, func()) error

func init() {
	rootCmd.AddCommand(exposeCmd)
	registerExposeFlags(exposeCmd, false)
}

func registerExposeFlags(cmd *cobra.Command, includeInsecureAlias bool) {
	cmd.Flags().StringVar(&protocol, "protocol", "https", "Protocol to tunnel: https, ssh, or tcp")
	cmd.Flags().StringVar(&exposeTarget, "target", "", "HTTP upstream target URL to expose, e.g. http://10.0.0.12:8080")
	cmd.Flags().BoolVar(&targetTLSInsecureSkipVerify, "target-insecure-skip-verify", false, "Skip TLS certificate verification when --target uses https; use only for private/self-signed upstreams")
	if includeInsecureAlias {
		cmd.Flags().BoolVar(&targetTLSInsecureSkipVerify, "insecure", false, "Alias for --target-insecure-skip-verify")
	}
	cmd.Flags().DurationVar(&readyTimeout, "ready-timeout", 90*time.Second, "Maximum time to wait for the remote tunnel pod to become ready")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run the tunnel in the current process instead of handing it off to the local daemon")
	cmd.Flags().StringVar(&customDomain, "domain", "", "Custom domain to prepare; create a CNAME to the printed Sealos target before attaching")
	cmd.Flags().BoolVar(&waitDomain, "wait-domain", false, "Wait for verified custom domain DNS, then attach it and wait for Ingress/certificate readiness")
	cmd.Flags().DurationVar(&domainWaitTimeout, "domain-timeout", 5*time.Minute, "Maximum time to wait for custom domain readiness")
	cmd.Flags().StringVar(&basicAuthCredential, "basic-auth", "", "Enable Basic Auth for public traffic as username:password")
	cmd.Flags().StringVar(&basicAuthUser, "basic-auth-user", "", "Basic Auth username for public traffic")
	cmd.Flags().StringVar(&basicAuthPassword, "basic-auth-password", "", "Basic Auth password for public traffic; prefer --basic-auth-password-env to avoid shell history")
	cmd.Flags().StringVar(&basicAuthPasswordEnv, "basic-auth-password-env", "", "Read Basic Auth password for public traffic from an environment variable")
	cmd.Flags().StringVar(&bearerToken, "bearer-token", "", "Require this bearer token for public traffic; prefer --bearer-token-env")
	cmd.Flags().StringVar(&bearerTokenEnv, "bearer-token-env", "", "Read bearer token for public traffic from an environment variable")
	cmd.Flags().StringSliceVar(&ipAllowlist, "ip-allowlist", nil, "Allow public traffic only from these IP/CIDR entries; repeat or comma-separate")
	cmd.Flags().StringSliceVar(&ipDenylist, "ip-denylist", nil, "Deny public traffic from these IP/CIDR entries; repeat or comma-separate")
	cmd.Flags().StringVar(&temporaryAccessToken, "temporary-access-token", "", "Enable a temporary access URL token; prefer --temporary-access-token-env")
	cmd.Flags().StringVar(&temporaryAccessTokenEnv, "temporary-access-token-env", "", "Read temporary access URL token from an environment variable")
	cmd.Flags().DurationVar(&temporaryAccessTTL, "temporary-access-ttl", time.Hour, "Temporary access URL lifetime")
	cmd.Flags().StringVar(&accessRateLimit, "rate-limit", "", "Rate limit HTTPS public traffic, e.g. 60/m or 1000/h")
	cmd.Flags().BoolVar(&accessAuditEnabled, "audit", false, "Enable HTTPS access audit for allow/deny decisions")
}

func resolveExposeTarget(args []string, explicitTarget string) (string, string, error) {
	if len(args) == 0 && strings.TrimSpace(explicitTarget) == "" {
		return "", "", fmt.Errorf("provide a local port or --target")
	}
	localPort := ""
	if len(args) > 0 {
		localPort = args[0]
		if err := validateLocalPort(localPort); err != nil {
			return "", "", err
		}
	}
	target, err := tunnel.TargetFor(localPort, explicitTarget)
	if err != nil {
		return "", "", err
	}
	if localPort != "" && strings.TrimSpace(explicitTarget) != "" && localPort != target.Port {
		return "", "", fmt.Errorf("positional port %s does not match --target port %s; omit the positional port or use the same port", localPort, target.Port)
	}
	if localPort == "" {
		localPort = target.Port
	}
	return localPort, target.URL, nil
}

func printAccessPolicySummary(w io.Writer, config *session.AccessPolicy) {
	if config == nil {
		return
	}
	if len(config.BearerTokenHashes) > 0 {
		fmt.Fprintf(w, "[+] Bearer token access enabled for public traffic.\n")
	}
	if len(config.IPAllowlist) > 0 {
		fmt.Fprintf(w, "[+] IP allowlist enabled with %d rule(s).\n", len(config.IPAllowlist))
	}
	if len(config.IPDenylist) > 0 {
		fmt.Fprintf(w, "[+] IP denylist enabled with %d rule(s).\n", len(config.IPDenylist))
	}
	if len(config.TemporaryTokens) > 0 {
		fmt.Fprintf(w, "[+] Temporary access link enabled until %s.\n", config.TemporaryTokens[0].ExpiresAt)
	}
	if config.RateLimit != "" {
		fmt.Fprintf(w, "[+] Rate limit enabled: %s.\n", config.RateLimit)
	}
	if config.Audit != nil && config.Audit.Enabled {
		fmt.Fprintf(w, "[+] Access audit enabled for public traffic.\n")
	}
}

func dialForegroundTunnelWithRetry(ctx context.Context, timeout, interval time.Duration, dial foregroundDialFunc) error {
	if timeout <= 0 {
		return dial(ctx, func() {})
	}
	if interval <= 0 {
		interval = time.Second
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		connected := false
		err := dial(dialCtx, func() {
			connected = true
		})
		if err == nil || connected {
			return err
		}
		lastErr = err

		select {
		case <-dialCtx.Done():
			if lastErr != nil {
				return lastErr
			}
			return dialCtx.Err()
		case <-time.After(interval):
		}
	}
}

func validateLocalPort(port string) error {
	value, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port %q: must be a number between 1 and 65535", port)
	}
	if value < 1 || value > 65535 {
		return fmt.Errorf("invalid port %q: must be between 1 and 65535", port)
	}
	return nil
}

func validateProtocol(protocol string) error {
	return tunnelprotocol.ValidateExpose(protocol)
}

func recoverStaleSessions(ctx context.Context, out io.Writer) error {
	sessions, err := session.List()
	if err != nil {
		return fmt.Errorf("load tunnel sessions: %w", err)
	}

	for _, sess := range sessions {
		if !sessionNeedsAutomaticRecovery(sess, time.Minute) {
			continue
		}

		fmt.Fprintf(out, "[+] Found stale tunnel session %s in namespace %s. Cleaning up...\n", sess.TunnelID, sess.Namespace)
		cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := cleanupSessionResources(cleanupCtx, sess)
		cancel()
		if err != nil {
			fmt.Fprintf(out, "[!] Skipped stale tunnel %s cleanup: %v\n", sess.TunnelID, err)
			continue
		}
		if err := session.Delete(sess.TunnelID); err != nil {
			return fmt.Errorf("delete stale tunnel session %s: %w", sess.TunnelID, err)
		}
	}

	return nil
}

func cleanupTunnel(k8sClient *k8s.Client, tunnelID string) {
	if tunnelCleanupShouldPreserve(tunnelID) {
		fmt.Printf("\r[+] Tunnel %s is stopped. Preserving remote entry resources.\n", tunnelID)
		return
	}

	fmt.Printf("\r[+] Disconnected. Cleaning up tunnel resources remotely...\n")
	cleanupCtx, cancel := context.WithTimeout(context.Background(), tunnelCleanupTimeout)
	defer cancel()

	if err := k8sClient.CleanupTunnel(cleanupCtx, tunnelID); err != nil {
		fmt.Printf("[!] Cleanup for tunnel %s did not complete: %v\n", tunnelID, err)
		return
	}
	if err := session.Delete(tunnelID); err != nil {
		fmt.Printf("[!] Failed to remove local session record for tunnel %s: %v\n", tunnelID, err)
	}
}

func tunnelCleanupShouldPreserve(tunnelID string) bool {
	sess, err := session.Get(tunnelID)
	return err == nil && shouldPreserveStoppedSession(sess)
}

func cleanupTunnelForCurrentOwner(k8sClient *k8s.Client, tunnelID string) {
	if !foregroundSessionOwnedByCurrentProcess(tunnelID) {
		fmt.Printf("\r[+] Tunnel %s is no longer owned by this foreground process. Preserving remote resources.\n", tunnelID)
		return
	}
	cleanupTunnel(k8sClient, tunnelID)
}

func foregroundSessionOwnedByCurrentProcess(tunnelID string) bool {
	sess, err := session.Get(tunnelID)
	if err != nil {
		return true
	}
	if shouldPreserveStoppedSession(sess) {
		return false
	}
	if sess.Mode != "foreground" {
		return false
	}
	if sess.PID != os.Getpid() {
		return false
	}
	if sess.PIDStartToken == "" {
		return true
	}
	currentToken := session.ProcessStartToken(os.Getpid())
	return currentToken == "" || currentToken == sess.PIDStartToken
}
