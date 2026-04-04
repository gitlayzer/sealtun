package cmd

import (
	"fmt"

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

		svr := tunnel.NewServer(secret, bindPort)
		return svr.Start()
	},
}

var secret string
var bindPort int

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&secret, "secret", "", "Tunnel authentication secret")
	serverCmd.Flags().IntVar(&bindPort, "port", 8080, "Port to bind the server to")
}
