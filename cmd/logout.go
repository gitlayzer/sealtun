package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

var logoutForce bool

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the current Sealtun login session",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := auth.LoadAuthData()
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to load auth data: %w", err)
			}
		}

		if !logoutForce {
			if err := cleanupLogoutSessions(cmd); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), "[!] --force selected: local session credentials will be scrubbed without remote cleanup guarantees.")
			if err := warnForegroundSessionsForLogout(cmd); err != nil {
				return err
			}
		}

		if err := stopDaemonForLogout(cmd); err != nil {
			return err
		}

		if err := session.ScrubCredentials(); err != nil {
			return fmt.Errorf("failed to scrub tunnel session credentials: %w", err)
		}

		if err := auth.ClearAuthData(); err != nil {
			return fmt.Errorf("failed to clear auth data: %w", err)
		}

		fmt.Println("Logged out. Removed local Sealtun credentials and scrubbed tunnel session secrets.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
	logoutCmd.Flags().BoolVar(&logoutForce, "force", false, "Remove local credentials even if tracked tunnel resources cannot be cleaned up")
}

func stopDaemonForLogout(cmd *cobra.Command) error {
	if !daemonstate.Alive() {
		return nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "[+] Stopping local Sealtun daemon before logout...")
	if err := daemonstate.Stop(5 * time.Second); err != nil {
		return fmt.Errorf("stop local daemon before logout: %w", err)
	}
	return nil
}

func warnForegroundSessionsForLogout(cmd *cobra.Command) error {
	sessions, err := session.List()
	if err != nil {
		return fmt.Errorf("load local session records: %w", err)
	}
	for _, sess := range sessions {
		if sess.Mode == "daemon" || sess.PID <= 0 || sess.PID == os.Getpid() || !session.OwnerAlive(sess) {
			continue
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "[!] Foreground tunnel process %d may still be running; stop that terminal/process to disconnect it.\n", sess.PID)
	}
	return nil
}

func cleanupLogoutSessions(cmd *cobra.Command) error {
	sessions, err := session.List()
	if err != nil {
		return fmt.Errorf("load local session records: %w", err)
	}
	if len(sessions) == 0 {
		return nil
	}

	for _, sess := range sessions {
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		if err := cleanupSessionResources(ctx, sess); err != nil {
			cancel()
			return fmt.Errorf("cleanup tunnel %s before logout: %w (rerun with --force to only remove local credentials)", sess.TunnelID, err)
		}
		cancel()
		if err := session.Delete(sess.TunnelID); err != nil {
			return fmt.Errorf("delete local session %s before logout: %w", sess.TunnelID, err)
		}
	}
	return nil
}
