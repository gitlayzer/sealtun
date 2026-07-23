package cmd

import (
	"fmt"

	"github.com/labring/sealtun/pkg/session"
	"github.com/labring/sealtun/pkg/tunnel"
	"github.com/spf13/cobra"
)

var sshCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Connect SSH over an existing Sealtun tunnel",
}

var sshConnectCmd = &cobra.Command{
	Use:          "connect [tunnel-id]",
	Short:        "Proxy raw SSH traffic over a Sealtun tunnel",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		sess, err := findSession(args[0])
		if err != nil {
			return err
		}
		if sess.Secret == "" {
			return fmt.Errorf("tunnel %s cannot be used for SSH because its local secret is unavailable", sess.TunnelID)
		}
		if sess.ConnectionState == session.ConnectionStateStopped {
			return fmt.Errorf("tunnel %s is stopped; run `sealtun start %s` first", sess.TunnelID, sess.TunnelID)
		}

		controlHost, err := normalizePublicHostname(sessionControlHost(*sess))
		if err != nil {
			return fmt.Errorf("invalid tunnel control host: %w", err)
		}
		wsURL := fmt.Sprintf("wss://%s/_sealtun/tcp", controlHost)
		return tunnel.DialRawTCPOverWebSocket(cmd.Context(), wsURL, sess.Secret)
	},
}

func init() {
	rootCmd.AddCommand(sshCmd)
	sshCmd.AddCommand(sshConnectCmd)
	markAlphaTree(sshCmd)
}
