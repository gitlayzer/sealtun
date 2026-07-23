package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:     "start [tunnel-id]",
	Aliases: []string{"resume"},
	Short:   "Restart a stopped Sealtun tunnel",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withTunnelOperationLock(args[0], func() error {
			return runStartTunnel(cmd, args[0])
		})
	},
}

var startSessionUpdate = session.Update

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStartTunnel(cmd *cobra.Command, tunnelID string) error {
	sess, err := findSession(tunnelID)
	if err != nil {
		return err
	}
	if sess.Secret == "" {
		return fmt.Errorf("tunnel %s cannot be started because its local secret is unavailable; run cleanup and recreate the tunnel", sess.TunnelID)
	}
	if sessionExpired(*sess, time.Now()) {
		return fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
	}

	if err := startTunnelSession(cmd.Context(), sess); err != nil {
		return err
	}
	_ = refreshSessionFromRemoteLocked(cmd.Context(), sess)

	if sess.PublicPort != 0 && (sess.Protocol == "ssh" || sess.Protocol == "tcp") {
		endpoint := endpointDisplay(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort)
		fmt.Fprintf(cmd.OutOrStdout(), "Started tunnel %s.\n", sess.TunnelID)
		if sess.Protocol == "ssh" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Public SSH host: %s\n", endpoint.Host)
			fmt.Fprintf(cmd.OutOrStdout(), "  Public SSH port: %d\n", endpoint.Port)
			fmt.Fprintf(cmd.OutOrStdout(), "  SSH command: %s\n", endpoint.Command)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  Public TCP host: %s\n", endpoint.Host)
			fmt.Fprintf(cmd.OutOrStdout(), "  Public TCP port: %d\n", endpoint.Port)
			fmt.Fprintf(cmd.OutOrStdout(), "  Public TCP endpoint: %s\n", endpointLabel(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Target: %s\n", sessionTargetLabel(*sess))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Started tunnel %s.\n", sess.TunnelID)
	fmt.Fprintf(cmd.OutOrStdout(), "  Public URL: %s\n", endpointLabel(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort))
	fmt.Fprintf(cmd.OutOrStdout(), "  Target: %s\n", sessionTargetLabel(*sess))
	return nil
}

func startTunnelSession(ctx context.Context, sess *session.TunnelSession) error {
	resumeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := resumeSessionResources(resumeCtx, *sess); err != nil {
		return fmt.Errorf("resume tunnel %s: %w", sess.TunnelID, err)
	}

	sess.Mode = "daemon"
	sess.PID = 0
	sess.ConnectionState = session.ConnectionStatePending
	sess.LastError = ""
	if err := startSessionUpdate(*sess); err != nil {
		return rollbackStartedTunnelSession(*sess, fmt.Errorf("update local session %s: %w", sess.TunnelID, err))
	}

	if err := ensureDaemonRunningFn(); err != nil {
		return rollbackStartedTunnelSession(*sess, fmt.Errorf("failed to start local daemon: %w", err))
	}
	if err := waitForDaemonSession(sess.TunnelID, daemonConnectTimeout); err != nil {
		return rollbackStartedTunnelSession(*sess, err)
	}
	return nil
}

func rollbackStartedTunnelSession(sess session.TunnelSession, cause error) error {
	pauseCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rollbackErr := pauseSessionResources(pauseCtx, sess)
	sess.PID = 0
	sess.LastError = cause.Error()
	if rollbackErr != nil {
		sess.ConnectionState = session.ConnectionStateError
		updateErr := session.Update(sess)
		if updateErr != nil {
			return fmt.Errorf("%w; rollback pause failed: %v; rollback session update failed: %v", cause, rollbackErr, updateErr)
		}
		return fmt.Errorf("%w; rollback pause failed: %v", cause, rollbackErr)
	}
	sess.ConnectionState = session.ConnectionStateStopped
	updateErr := session.Update(sess)
	if updateErr != nil {
		return fmt.Errorf("%w; rollback session update failed: %v", cause, updateErr)
	}
	return cause
}
