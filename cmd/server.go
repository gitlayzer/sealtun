package cmd

import (
	"fmt"
	"os"

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

		resolvedSecret, err := resolveServerSecret(secret, secretEnv, os.Getenv)
		if err != nil {
			return err
		}
		if resolvedSecret == "" {
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

		svr := tunnel.NewServer(resolvedSecret, bindPort, serverProtocol, serverLocalPort)
		return svr.Start()
	},
}

var secret string
var secretEnv string
var bindPort int
var serverProtocol string
var serverLocalPort string

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&secret, "secret", "", "Tunnel authentication secret")
	serverCmd.Flags().StringVar(&secretEnv, "secret-env", "", "Read tunnel authentication secret from an environment variable")
	serverCmd.Flags().IntVar(&bindPort, "port", 8080, "Port to bind the server to")
	serverCmd.Flags().StringVar(&serverProtocol, "protocol", "https", "Tunnel protocol")
	serverCmd.Flags().StringVar(&serverLocalPort, "local-port", "", "Expected local port displayed on fallback pages")
}

func resolveServerSecret(flagSecret, envName string, lookupEnv func(string) string) (string, error) {
	if envName == "" {
		return flagSecret, nil
	}
	value := lookupEnv(envName)
	if value == "" {
		return "", fmt.Errorf("secret environment variable %s is empty or unset", envName)
	}
	return value, nil
}
