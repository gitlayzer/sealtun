package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop [tunnel-id]",
	Short: "Stop a Sealtun tunnel while preserving its domain and remote entry resources",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return withTunnelOperationLock(args[0], func() error {
			return runStopTunnel(cmd, args[0])
		})
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStopTunnel(cmd *cobra.Command, tunnelID string) error {
	sess, err := findSession(tunnelID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	ownerAlive, err := stopTunnelSession(ctx, sess)
	if err != nil {
		return err
	}

	if sess.Protocol == "ssh" || sess.Protocol == "tcp" {
		fmt.Fprintf(cmd.OutOrStdout(), "Stopped %s tunnel %s. TCP NodePort, control resources, and local session were preserved.\n", sess.Protocol, sess.TunnelID)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Stopped tunnel %s. Domain, Service, Ingress, and local session were preserved.\n", sess.TunnelID)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Run `sealtun start %s` to reopen it, or `sealtun cleanup` to delete stopped tunnel resources.\n", sess.TunnelID)
	if ownerAlive {
		fmt.Fprintln(cmd.OutOrStdout(), "Note: the local expose process may still be running until it exits on its own.")
	}
	return nil
}

func stopTunnelSession(ctx context.Context, sess *session.TunnelSession) (bool, error) {
	ownerAlive := sessionOwnerAlive(*sess)
	previous := *sess
	sess.PID = 0
	sess.ConnectionState = session.ConnectionStateStopped
	sess.LastError = ""
	if err := session.Update(*sess); err != nil {
		return ownerAlive, fmt.Errorf("update local session %s: %w", sess.TunnelID, err)
	}
	if err := pauseSessionResources(ctx, *sess); err != nil {
		previous.LastError = err.Error()
		if restoreErr := session.Update(previous); restoreErr != nil {
			return ownerAlive, fmt.Errorf("pause tunnel %s: %w; restore local session failed: %v", sess.TunnelID, err, restoreErr)
		}
		*sess = previous
		return ownerAlive, fmt.Errorf("pause tunnel %s: %w", sess.TunnelID, err)
	}
	return ownerAlive, nil
}
