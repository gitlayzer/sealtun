package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/labring/sealtun/pkg/version"
)

var rootCmd = &cobra.Command{
	Use:     "sealtun",
	Version: version.Version,
	Short:   "A cloudflared-like tunnel for Sealos Cloud",
	Long: `Sealtun provides a simple way to expose local development ports to the public internet
via Sealos Cloud using Kubernetes native resources (Deployment, Service, Ingress) and WebSocket tunneling.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
}
