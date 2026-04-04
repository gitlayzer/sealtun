package cmd

import (
	"fmt"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login [region]",
	Short: "Log in to Sealos Cloud",
	Long: `Log in to Sealos Cloud using the OAuth2 Device Grant Flow.
If region is not provided, it defaults to the configured default region.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		region := auth.DefaultRegion
		if len(args) > 0 {
			region = args[0]
		}
		fmt.Printf("login called (region: %s)\n", region)

		// 1. Request device authorization
		deviceAuth, err := auth.RequestDeviceAuthorization(region)
		if err != nil {
			return fmt.Errorf("failed to request device authorization: %w", err)
		}

		fmt.Printf("\nPlease open the following URL in your browser to authorize:\n\n  %s\n\nAuthorization code: %s\nExpires in: %d minutes\n\n",
			deviceAuth.VerificationURIComplete, deviceAuth.UserCode, deviceAuth.ExpiresIn/60)
		fmt.Println("Waiting for authorization...")

		// 2. Poll for token
		tokenRes, err := auth.PollForToken(region, deviceAuth.DeviceCode, deviceAuth.Interval, deviceAuth.ExpiresIn)
		if err != nil {
			return fmt.Errorf("failed to poll for token: %w", err)
		}
		fmt.Println("Authorization received. Exchanging for regional token...")

		// 3. Exchange for regional token
		regionData, err := auth.GetRegionToken(region, tokenRes.AccessToken)
		if err != nil {
			return fmt.Errorf("failed to get region token: %w", err)
		}

		// 4. Save to config
		authData := auth.AuthData{
			Region:          region,
			AccessToken:     tokenRes.AccessToken,
			RegionalToken:   regionData.Data.Token,
			AuthenticatedAt: time.Now().Format(time.RFC3339),
			AuthMethod:      "oauth2_device_grant",
		}
		if err := auth.SaveAuthData(authData, regionData.Data.Kubeconfig); err != nil {
			return fmt.Errorf("failed to save auth data: %w", err)
		}

		fmt.Println("Authentication successful!")
		return nil
	},
}

var insecure bool

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification")
}
