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
	Short: "Stop a Sealtun tunnel and remove its local session record",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := findSession(args[0])
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()

		if err := cleanupSessionResources(ctx, *sess); err != nil {
			return fmt.Errorf("cleanup tunnel %s: %w", sess.TunnelID, err)
		}
		if err := session.Delete(sess.TunnelID); err != nil {
			return fmt.Errorf("delete local session %s: %w", sess.TunnelID, err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Stopped tunnel %s and removed its local session record.\n", sess.TunnelID)
		if sessionOwnerAlive(*sess) {
			fmt.Fprintln(cmd.OutOrStdout(), "Note: the local expose process may still be running until it exits on its own.")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
