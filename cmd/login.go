package cmd

import (
	"fmt"
	"net/url"
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
		region := ""
		if len(args) > 0 {
			region = args[0]
		}
		return runLoginFlow(region, insecure)
	},
}

var insecure bool

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification")
}

func runLoginFlow(regionInput string, insecure bool) error {
	region, err := auth.ResolveRegion(regionInput)
	if err != nil {
		return err
	}

	auth.SetInsecureSkipTLSVerify(insecure)
	fmt.Printf("login called (region: %s)\n", region)

	deviceAuth, err := auth.RequestDeviceAuthorization(region)
	if err != nil {
		return fmt.Errorf("failed to request device authorization: %w", err)
	}

	authURL, err := verificationURL(deviceAuth)
	if err != nil {
		return err
	}

	fmt.Printf("\nPlease open the following URL in your browser to authorize:\n\n  %s\n\nAuthorization code: %s\nExpires in: %d minutes\n\n",
		authURL, deviceAuth.UserCode, deviceAuth.ExpiresIn/60)

	openBrowser(authURL)

	fmt.Println("Waiting for authorization...")

	tokenRes, err := auth.PollForToken(region, deviceAuth.DeviceCode, deviceAuth.Interval, deviceAuth.ExpiresIn)
	if err != nil {
		return fmt.Errorf("failed to poll for token: %w", err)
	}
	fmt.Println("Authorization received. Exchanging for regional token...")

	regionData, err := auth.GetRegionToken(region, tokenRes.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to get region token: %w", err)
	}

	initData, err := auth.GetInitData(region)
	if err != nil {
		return fmt.Errorf("failed to get region init data: %w", err)
	}
	if initData == nil || initData.Data.SealosDomain == "" {
		return fmt.Errorf("region init data did not include SEALOS_DOMAIN")
	}

	var currentWorkspace *auth.Workspace
	nsData, err := auth.ListWorkspaces(region, regionData.Data.Token)
	if err == nil && nsData != nil && len(nsData.Data.Namespaces) > 0 {
		match := nsData.Data.Namespaces[0]
		for _, ns := range nsData.Data.Namespaces {
			isPrivate := false
			switch v := ns.NSType.(type) {
			case string:
				isPrivate = (v == "private")
			case float64:
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

	authData := auth.AuthData{
		Region:           region,
		SealosDomain:     initData.Data.SealosDomain,
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
}

func verificationURL(deviceAuth *auth.DeviceAuthResponse) (string, error) {
	if deviceAuth == nil {
		return "", fmt.Errorf("device authorization response is empty")
	}
	for _, candidate := range []string{deviceAuth.VerificationURIComplete, deviceAuth.VerificationURI} {
		if candidate == "" {
			continue
		}
		if isSafeBrowserURL(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("device authorization response did not include a safe http(s) verification URL")
}

func isSafeBrowserURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func openBrowser(url string) {
	if !isSafeBrowserURL(url) {
		fmt.Printf("Skipped opening unsafe browser URL: %s\n", url)
		return
	}

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
