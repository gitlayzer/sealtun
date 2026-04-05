package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
		
		// 1. Check if logged in.
		authData, err := auth.LoadAuthData()
		if err != nil {
			return fmt.Errorf("not logged in. Please run 'sealtun login' first: %w", err)
		}

		sealtunDir, err := auth.GetSealtunDir()
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
		host, err := k8sClient.EnsureTunnel(ctx, tunnelID, secret)
		if err != nil {
			return fmt.Errorf("failed to provision tunnel on Sealos: %w", err)
		}
		
		fmt.Printf("[+] Public URL will be: https://%s\n", host)
		fmt.Printf("[+] Waiting for tunnel server pod to be ready...\n")
		
		if err := k8sClient.WaitForReady(ctx, tunnelID); err != nil {
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

func init() {
	rootCmd.AddCommand(exposeCmd)
	exposeCmd.Flags().StringVar(&protocol, "protocol", "http", "Protocol to tunnel (http, tcp)")
}
