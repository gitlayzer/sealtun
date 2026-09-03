package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

var cleanupAll bool
var cleanupYes bool

var cleanupCmd = &cobra.Command{
	Use:   "cleanup [tunnel-id]",
	Short: "Clean up stopped, expired, stale, or managed Sealtun tunnel resources",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cleanupAll && len(args) > 0 {
			return fmt.Errorf("--all cannot be used with a specific tunnel id")
		}
		if cleanupAll && !cleanupYes {
			// Deleting every tracked tunnel and its remote resources is the
			// most destructive operation in the CLI; require explicit intent.
			if !defaultUpCommandInteractive(cmd) {
				return fmt.Errorf("cleanup --all deletes every tracked tunnel and its remote resources; pass --yes to confirm")
			}
			sessions, err := session.List()
			if err != nil {
				return fmt.Errorf("load local session records: %w", err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "This will delete %d tunnel(s) and their remote resources. Type 'yes' to continue: ", len(sessions))
			var answer string
			fmt.Fscanln(cmd.InOrStdin(), &answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "yes" {
				return fmt.Errorf("cleanup --all aborted")
			}
		}
		if len(args) > 0 {
			eligible, deleteFailed, err := cleanupTunnelWithLock(cmd, args[0], cleanupAll)
			if err != nil {
				if deleteFailed {
					return err
				}
				return fmt.Errorf("cleanup tunnel %s: %w", args[0], err)
			}
			if !eligible {
				return fmt.Errorf("tunnel %s is not stopped, expired, stale, or error; refusing cleanup without --all", args[0])
			}
			fmt.Printf("Cleanup complete. Removed tunnel %s and its remote resources.\n", args[0])
			return nil
		}
		sessions, err := session.List()
		if err != nil {
			return fmt.Errorf("load local session records: %w", err)
		}

		if cleanupAll {
			removed := 0
			failed := 0
			for _, sess := range sessions {
				_, deleteFailed, err := cleanupTunnelWithLock(cmd, sess.TunnelID, true)
				if err != nil {
					if deleteFailed {
						return err
					}
					failed++
					fmt.Fprintf(cmd.ErrOrStderr(), "[!] Skipped tunnel %s: %v\n", sess.TunnelID, err)
					continue
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
			eligible, deleteFailed, err := cleanupTunnelWithLock(cmd, sess.TunnelID, false)
			if err != nil {
				if deleteFailed {
					return err
				}
				failed++
				if errors.Is(err, errMissingSessionKubeconfig) {
					fmt.Fprintf(cmd.ErrOrStderr(), "[!] Skipped cleanup-eligible tunnel %s: %v\n", sess.TunnelID, err)
					continue
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "[!] Failed to clean up cleanup-eligible tunnel %s: %v\n", sess.TunnelID, err)
				continue
			}
			if !eligible {
				skipped++
				continue
			}
			cleaned++
		}

		fmt.Printf("Cleanup complete. Removed %d stopped, expired, stale, or error tunnels; skipped %d active session records.\n", cleaned, skipped)
		if failed > 0 {
			return fmt.Errorf("failed to clean up %d cleanup-eligible tunnel session(s); local records were kept", failed)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cleanupCmd)
	cleanupCmd.Flags().BoolVar(&cleanupAll, "all", false, "Force delete all locally tracked Sealtun tunnel resources and remove matching local session records")
	cleanupCmd.Flags().BoolVar(&cleanupYes, "yes", false, "Confirm the destructive cleanup --all operation without prompting")
}

func cleanupTunnelWithLock(cmd *cobra.Command, tunnelID string, force bool) (bool, bool, error) {
	eligible := false
	deleteFailed := false
	err := withTunnelOperationLock(tunnelID, func() error {
		sess, err := findSession(tunnelID)
		if err != nil {
			return err
		}
		if !force && !sessionCleanupEligible(*sess, time.Minute) {
			return nil
		}
		eligible = true
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		if err := cleanupSessionResources(ctx, *sess); err != nil {
			return err
		}
		if err := session.Delete(sess.TunnelID); err != nil {
			deleteFailed = true
			return fmt.Errorf("delete local session %s: %w", sess.TunnelID, err)
		}
		return nil
	})
	return eligible, deleteFailed, err
}
