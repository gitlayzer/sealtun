package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

var cleanupAll bool

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Clean up stale or managed Sealtun tunnel resources",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessions, err := session.List()
		if err != nil {
			return fmt.Errorf("load local session records: %w", err)
		}

		if cleanupAll {
			removed := 0
			failed := 0
			for _, sess := range sessions {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				if err := cleanupSessionResources(ctx, sess); err != nil {
					cancel()
					failed++
					fmt.Fprintf(cmd.ErrOrStderr(), "[!] Skipped tunnel %s: %v\n", sess.TunnelID, err)
					continue
				}
				cancel()
				if err := session.Delete(sess.TunnelID); err != nil {
					return fmt.Errorf("delete local session %s: %w", sess.TunnelID, err)
				}
				removed++
			}

			fmt.Printf("Cleanup complete. Removed %d Sealtun tunnel session(s) and their remote resources.\n", removed)
			if failed > 0 {
				return fmt.Errorf("failed to clean up %d tunnel session(s); local records were kept", failed)
			}
			return nil
		}

		cleaned := 0
		skipped := 0
		failed := 0
		for _, sess := range sessions {
			if !sessionIsStale(sess, time.Minute) {
				skipped++
				continue
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			if err := cleanupSessionResources(ctx, sess); err != nil {
				cancel()
				failed++
				if errors.Is(err, errMissingSessionKubeconfig) {
					fmt.Fprintf(cmd.ErrOrStderr(), "[!] Skipped stale tunnel %s: %v\n", sess.TunnelID, err)
					continue
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "[!] Failed to clean up stale tunnel %s: %v\n", sess.TunnelID, err)
				continue
			}
			cancel()
			if err := session.Delete(sess.TunnelID); err != nil {
				return fmt.Errorf("delete local session %s: %w", sess.TunnelID, err)
			}
			cleaned++
		}

		fmt.Printf("Cleanup complete. Removed %d stale tunnels, skipped %d active session records.\n", cleaned, skipped)
		if failed > 0 {
			return fmt.Errorf("failed to clean up %d stale tunnel session(s); local records were kept", failed)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cleanupCmd)
	cleanupCmd.Flags().BoolVar(&cleanupAll, "all", false, "Delete all locally tracked Sealtun tunnel resources and remove matching local session records")
}
