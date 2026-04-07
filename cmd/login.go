package cmd

import (
	"fmt"
	"os/exec"
	"runtime"
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

		// 2. Auto-open browser
		url := deviceAuth.VerificationURIComplete
		if url == "" {
			url = deviceAuth.VerificationURI
		}
		if url != "" {
			openBrowser(url)
		}

		fmt.Println("Waiting for authorization...")

		// 3. Poll for token
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

		// 4. Determine current workspace from namespace list (optional, matching JS behavior)
		var currentWorkspace *auth.Workspace
		nsData, err := auth.ListWorkspaces(region, regionData.Data.Token)
		if err == nil && nsData != nil && len(nsData.Data.Namespaces) > 0 {
			// Find private workspace or use the first one
			match := nsData.Data.Namespaces[0]
			for _, ns := range nsData.Data.Namespaces {
				// Handle both "private" (string) and 1 (int) for private namespaces
				isPrivate := false
				switch v := ns.NSType.(type) {
				case string:
					isPrivate = (v == "private")
				case float64: // json.Unmarshal uses float64 for numbers by default
					isPrivate = (v == 1)
				}

				if isPrivate {
					match = ns
					break
				}
			}
			currentWorkspace = &auth.Workspace{
				UID:      match.UID,
				ID:       match.ID,
				TeamName: match.TeamName,
			}
		}

		// 5. Save to config
		authData := auth.AuthData{
			Region:           region,
			AccessToken:      tokenRes.AccessToken,
			RegionalToken:    regionData.Data.Token,
			AuthenticatedAt:  time.Now().Format(time.RFC3339),
			AuthMethod:       "oauth2_device_grant",
			CurrentWorkspace: currentWorkspace,
		}
		if err := auth.SaveAuthData(authData, regionData.Data.Kubeconfig); err != nil {
			return fmt.Errorf("failed to save auth data: %w", err)
		}

		fmt.Println("Authentication successful!")
		if currentWorkspace != nil {
			fmt.Printf("Logged in to workspace: %s (%s)\n", currentWorkspace.ID, currentWorkspace.TeamName)
		}
		return nil
	},
}

var insecure bool

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification")
}

func openBrowser(url string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
		args = []string{url}
	}

	if err := exec.Command(cmd, args...).Start(); err != nil {
		fmt.Printf("Browser opened failed: %v. Please open the URL manually.\n", err)
	} else {
		fmt.Println("Browser opened automatically.")
	}
}
