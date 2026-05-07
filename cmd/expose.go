package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
	Short: "Expose a local port to the internet",
	Long: `Expose a local port to the internet via Sealos Cloud.
This command automatically deploys a tunnel server on Sealos, obtains a public URL,
and establishes a secure connection to forward traffic to your local port.`,
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		localPort := args[0]
		if err := validateLocalPort(localPort); err != nil {
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
		kubeconfigBytes, err := os.ReadFile(kcPath) // #nosec G304 -- kubeconfig path is fixed under the user-owned Sealtun config directory.
		if err != nil {
			return fmt.Errorf("failed to read kubeconfig: %w", err)
		}

		// 2. Generate tunnel ID & secret.
		tunnelID := uuid.New().String()[:8]
		secret := uuid.New().String()
		fmt.Printf("[+] Preparing tunnel %s...\n", tunnelID)

		// 3. Create K8s Resources (Deployment, Service, Ingress)
		k8sClient, err := k8s.NewClient(kcPath, authData)
		if err != nil {
			return fmt.Errorf("failed to init k8s client: %w", err)
		}

		ctx := cmd.Context()
		if err := recoverStaleSessions(ctx); err != nil {
			return err
		}

		hosts, err := k8sClient.EnsureTunnelWithOptions(ctx, tunnelID, secret, protocol, localPort, k8s.TunnelOptions{})
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
			Kubeconfig:      string(kubeconfigBytes),
			Protocol:        protocol,
			Host:            hosts.PublicHost,
			SealosHost:      hosts.SealosHost,
			CustomDomain:    hosts.CustomDomain,
			LocalPort:       localPort,
			Secret:          secret,
			Mode:            "foreground",
			PID:             os.Getpid(),
			ConnectionState: session.ConnectionStatePending,
			Resources: []string{
				fmt.Sprintf("sealtun-%s", tunnelID),
			},
		}
		if err := session.Save(sessionRecord); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), tunnelCleanupTimeout)
			defer cancel()
			_ = k8sClient.CleanupTunnel(cleanupCtx, tunnelID)
			return fmt.Errorf("failed to persist tunnel session: %w", err)
		}

		fmt.Printf("[+] Public URL will be: https://%s\n", hosts.PublicHost)
		if normalizedCustomDomain != "" {
			fmt.Printf("[+] Requested custom domain: %s\n", normalizedCustomDomain)
			fmt.Printf("[+] Sealos CNAME target: %s\n", hosts.SealosHost)
			fmt.Printf("[+] Configure DNS: CNAME %s -> %s\n", normalizedCustomDomain, hosts.SealosHost)
			if !waitDomain {
				fmt.Printf("[+] After DNS is ready, attach it with: sealtun domain set %s %s\n", tunnelID, normalizedCustomDomain)
			}
		}
		fmt.Printf("[+] Waiting for tunnel server pod to be ready...\n")

		readyCtx, cancelReady := context.WithTimeout(ctx, readyTimeout)
		defer cancelReady()
		if err := k8sClient.WaitForReady(readyCtx, tunnelID); err != nil {
			return fmt.Errorf("timed out waiting for tunnel server: %w", err)
		}
		if waitDomain && normalizedCustomDomain != "" {
			fmt.Printf("[+] Waiting for custom domain DNS, Ingress, and certificate readiness (timeout %s)...\n", domainWaitTimeout)
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
			if err := ensureDaemonRunning(); err != nil {
				return fmt.Errorf("failed to start local daemon: %w", err)
			}
			if err := waitForDaemonSession(tunnelID, daemonConnectTimeout); err != nil {
				return err
			}

			rollback = false
			fmt.Printf("[+] Tunnel is running in the background via the local daemon.\n")
			fmt.Printf("[+] Use `sealtun list` or `sealtun inspect %s` to view it later.\n", tunnelID)
			return nil
		}

		ctx, stop := signal.NotifyContext(ctx, signalCleanupSignals()...)
		defer stop()
		rollback = false
		defer cleanupTunnel(cleanupTarget, tunnelID)

		// 4 & 5. Connect via WebSocket
		wsURL := fmt.Sprintf("wss://%s/_sealtun/ws", sessionControlHost(sessionRecord))
		return tunnel.DialServerAndServeWithOnConnected(ctx, wsURL, secret, localPort, func() {
			current, err := session.Get(tunnelID)
			if err != nil {
				return
			}
			current.ConnectionState = session.ConnectionStateConnected
			current.LastError = ""
			current.LastConnectedAt = time.Now().Format(time.RFC3339)
			_ = session.Update(*current)
		})
	},
}

var protocol string
var readyTimeout time.Duration
var foreground bool
var customDomain string
var waitDomain bool
var domainWaitTimeout time.Duration

const daemonConnectTimeout = 60 * time.Second
const daemonConnectionStability = 2 * time.Second
const tunnelCleanupTimeout = 30 * time.Second

func init() {
	rootCmd.AddCommand(exposeCmd)
	exposeCmd.Flags().StringVar(&protocol, "protocol", "https", "Protocol to tunnel (currently only https)")
	exposeCmd.Flags().DurationVar(&readyTimeout, "ready-timeout", 90*time.Second, "Maximum time to wait for the remote tunnel pod to become ready")
	exposeCmd.Flags().BoolVar(&foreground, "foreground", false, "Run the tunnel in the current process instead of handing it off to the local daemon")
	exposeCmd.Flags().StringVar(&customDomain, "domain", "", "Custom domain to prepare; create a CNAME to the printed Sealos target before attaching")
	exposeCmd.Flags().BoolVar(&waitDomain, "wait-domain", false, "Wait for verified custom domain DNS, then attach it and wait for Ingress/certificate readiness")
	exposeCmd.Flags().DurationVar(&domainWaitTimeout, "domain-timeout", 5*time.Minute, "Maximum time to wait for custom domain readiness")
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

func recoverStaleSessions(ctx context.Context) error {
	sessions, err := session.List()
	if err != nil {
		return fmt.Errorf("load tunnel sessions: %w", err)
	}

	for _, sess := range sessions {
		if !sessionIsStale(sess, time.Minute) {
			continue
		}

		fmt.Printf("[+] Found stale tunnel session %s in namespace %s. Cleaning up...\n", sess.TunnelID, sess.Namespace)
		cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := cleanupSessionResources(cleanupCtx, sess)
		cancel()
		if err != nil {
			fmt.Printf("[!] Skipped stale tunnel %s cleanup: %v\n", sess.TunnelID, err)
			continue
		}
		if err := session.Delete(sess.TunnelID); err != nil {
			return fmt.Errorf("delete stale tunnel session %s: %w", sess.TunnelID, err)
		}
	}

	return nil
}

func cleanupTunnel(k8sClient *k8s.Client, tunnelID string) {
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
