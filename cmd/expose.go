package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/k8s"
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
		if err := validateProtocol(protocol); err != nil {
			return err
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
		host, err := k8sClient.EnsureTunnel(ctx, tunnelID, secret, protocol)
		if err != nil {
			return fmt.Errorf("failed to provision tunnel on Sealos: %w", err)
		}

		fmt.Printf("[+] Public URL will be: https://%s\n", host)
		fmt.Printf("[+] Waiting for tunnel server pod to be ready...\n")

		readyCtx, cancelReady := context.WithTimeout(ctx, readyTimeout)
		defer cancelReady()
		if err := k8sClient.WaitForReady(readyCtx, tunnelID); err != nil {
			return fmt.Errorf("timed out waiting for tunnel server: %w", err)
		}

		// Prepare graceful cleanup on normal exit or interrupt
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()

		defer func() {
			fmt.Printf("\r[+] Disconnected. Cleaning up tunnel resources remotely...\n")
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = k8sClient.Cleanup(cleanupCtx, tunnelID)
		}()

		// 4 & 5. Connect via WebSocket
		wsURL := fmt.Sprintf("wss://%s/_sealtun/ws", host)
		return tunnel.DialServerAndServe(ctx, wsURL, secret, localPort)
	},
}

var protocol string
var readyTimeout time.Duration

func init() {
	rootCmd.AddCommand(exposeCmd)
	exposeCmd.Flags().StringVar(&protocol, "protocol", "https", "Protocol to tunnel (https, grpcs)")
	exposeCmd.Flags().DurationVar(&readyTimeout, "ready-timeout", 90*time.Second, "Maximum time to wait for the remote tunnel pod to become ready")
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
	switch protocol {
	case "https", "grpcs", "grpc", "tcp", "ws", "wss":
		return nil
	default:
		return fmt.Errorf("unsupported protocol %q: must be one of https, grpcs, grpc, tcp, ws, wss", protocol)
	}
}
