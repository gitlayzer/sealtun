package cmd

import (
	"fmt"

	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/tunnel"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:    "server",
	Short:  "Run the tunnel server component (internal)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("sealtun server component starting on port %d\n", bindPort)

		if secret == "" {
			return fmt.Errorf("secret is required to run the server component")
		}
		if bindPort < 1 || bindPort > 65535 {
			return fmt.Errorf("invalid --port %d: must be between 1 and 65535", bindPort)
		}
		if err := tunnelprotocol.ValidateServer(serverProtocol); err != nil {
			return err
		}
		serverProtocol = tunnelprotocol.Normalize(serverProtocol)
		if err := validateLocalPort(serverLocalPort); err != nil {
			return fmt.Errorf("invalid --local-port: %w", err)
		}

		svr := tunnel.NewServer(secret, bindPort, serverProtocol, serverLocalPort)
		return svr.Start()
	},
}

var secret string
var bindPort int
var serverProtocol string
var serverLocalPort string

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&secret, "secret", "", "Tunnel authentication secret")
	serverCmd.Flags().IntVar(&bindPort, "port", 8080, "Port to bind the server to")
	serverCmd.Flags().StringVar(&serverProtocol, "protocol", "https", "Tunnel protocol")
	serverCmd.Flags().StringVar(&serverLocalPort, "local-port", "", "Expected local port displayed on fallback pages")
}
