package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// userCodePattern restricts the server-supplied device user code to a safe
// printable character set (alphanumerics and hyphens) before it is printed to
// the terminal, preventing escape-sequence injection. This covers the RFC 8628
// recommended format while tolerating lowercase codes.
var userCodePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

var loginCmd = &cobra.Command{
	Use:          "login [region]",
	Short:        "Log in to Sealos Cloud",
	SilenceUsage: true,
	Long: `Log in to Sealos Cloud using the OAuth2 Device Grant Flow.
If region is not provided in an interactive terminal, choose a built-in region
from the keyboard selector. In scripts or CI, pass the region explicitly.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		region, err := resolveLoginRegionInput(args, func() (string, error) {
			return selectLoginRegionFn(cmd)
		})
		if err != nil {
			return err
		}
		return runLoginFlowWithProfile(region, insecure, loginProfile)
	},
}

var insecure bool
var loginProfile string
var selectLoginRegionFn = selectLoginRegionFromCommand

var errLoginRegionSelectionCanceled = errors.New("region selection canceled")

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification")
	loginCmd.Flags().StringVar(&loginProfile, "profile", "", "Save and activate this login as a named profile")
}

func resolveLoginRegionInput(args []string, selectRegion func() (string, error)) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if selectRegion == nil {
		return "", fmt.Errorf("region is required when no interactive selector is available; run `sealtun region list` and then `sealtun login <region>`")
	}
	region, err := selectRegion()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(region) == "" {
		return "", fmt.Errorf("region selection returned an empty region")
	}
	return region, nil
}

func selectLoginRegionFromCommand(cmd *cobra.Command) (string, error) {
	in := cmd.InOrStdin()
	out := cmd.OutOrStdout()
	file, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", fmt.Errorf("region is required in non-interactive mode; run `sealtun region list` and then `sealtun login <region>`")
	}
	return selectLoginRegion(file, out, auth.KnownRegions(), auth.DefaultRegion)
}

func selectLoginRegion(input *os.File, out io.Writer, regions []auth.RegionOption, defaultRegion string) (string, error) {
	if len(regions) == 0 {
		return "", fmt.Errorf("no built-in regions are available")
	}
	selected := 0
	for i, region := range regions {
		if region.URL == defaultRegion {
			selected = i
			break
		}
	}

	oldState, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return selectLoginRegionLine(input, out, regions, selected)
	}
	defer func() {
		_ = term.Restore(int(input.Fd()), oldState)
	}()

	reader := bufio.NewReader(input)
	renderLoginRegionSelector(out, regions, selected, true)
	for {
		key, err := readLoginSelectorKey(reader)
		if err != nil {
			return "", err
		}
		switch key {
		case loginSelectorKeyUp:
			if selected > 0 {
				selected--
			} else {
				selected = len(regions) - 1
			}
			renderLoginRegionSelector(out, regions, selected, true)
		case loginSelectorKeyDown:
			if selected < len(regions)-1 {
				selected++
			} else {
				selected = 0
			}
			renderLoginRegionSelector(out, regions, selected, true)
		case loginSelectorKeyEnter:
			fmt.Fprint(out, "\n")
			return regions[selected].Name, nil
		case loginSelectorKeyCancel:
			fmt.Fprint(out, "\n")
			return "", errLoginRegionSelectionCanceled
		}
	}
}

func selectLoginRegionLine(input io.Reader, out io.Writer, regions []auth.RegionOption, selected int) (string, error) {
	renderLoginRegionSelector(out, regions, selected, false)
	fmt.Fprint(out, "Select region number: ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return regions[selected].Name, nil
	}
	for i, region := range regions {
		if value == region.Name || value == region.URL || value == fmt.Sprintf("%d", i+1) {
			return region.Name, nil
		}
	}
	return "", fmt.Errorf("unknown region selection %q; run `sealtun region list` to see supported regions", value)
}

type loginSelectorKey int

const (
	loginSelectorKeyUnknown loginSelectorKey = iota
	loginSelectorKeyUp
	loginSelectorKeyDown
	loginSelectorKeyEnter
	loginSelectorKeyCancel
)

func readLoginSelectorKey(reader *bufio.Reader) (loginSelectorKey, error) {
	b, err := reader.ReadByte()
	if err != nil {
		return loginSelectorKeyUnknown, err
	}
	switch b {
	case '\r', '\n':
		return loginSelectorKeyEnter, nil
	case 3, 27:
		if b == 3 {
			return loginSelectorKeyCancel, nil
		}
		next, err := reader.ReadByte()
		if err != nil {
			return loginSelectorKeyCancel, nil
		}
		if next != '[' {
			return loginSelectorKeyCancel, nil
		}
		code, err := reader.ReadByte()
		if err != nil {
			return loginSelectorKeyUnknown, err
		}
		switch code {
		case 'A':
			return loginSelectorKeyUp, nil
		case 'B':
			return loginSelectorKeyDown, nil
		default:
			return loginSelectorKeyUnknown, nil
		}
	case 'k', 'K':
		return loginSelectorKeyUp, nil
	case 'j', 'J':
		return loginSelectorKeyDown, nil
	case 'q', 'Q':
		return loginSelectorKeyCancel, nil
	default:
		return loginSelectorKeyUnknown, nil
	}
}

func renderLoginRegionSelector(out io.Writer, regions []auth.RegionOption, selected int, redraw bool) {
	if redraw {
		fmt.Fprint(out, "\r\033[2J\033[H")
	}
	fmt.Fprint(out, "Choose a Sealos region with Up/Down, Enter to continue:\r\n")
	for i, region := range regions {
		writeLoginRegionLine(out, region, i == selected, i+1)
		fmt.Fprint(out, "\r\n")
	}
	fmt.Fprint(out, "Press q or Ctrl-C to cancel.\r\n")
}

func writeLoginRegionLine(out io.Writer, region auth.RegionOption, selected bool, index int) {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	defaultMark := ""
	if region.URL == auth.DefaultRegion {
		defaultMark = " default"
	}
	fmt.Fprintf(out, "%s%d. %s%s\n     %s · %s", prefix, index, region.Name, defaultMark, region.URL, valueOr(region.SealosDomain, "domain unknown"))
}

func runLoginFlowWithProfile(regionInput string, insecure bool, profileName string) error {
	region, err := auth.ResolveRegion(regionInput)
	if err != nil {
		return err
	}
	normalizedProfile := ""
	if profileName != "" {
		normalizedProfile, err = auth.ValidateProfileName(profileName)
		if err != nil {
			return err
		}
	}

	auth.SetInsecureSkipTLSVerify(insecure)
	defer auth.SetInsecureSkipTLSVerify(false)

	deviceAuth, err := auth.RequestDeviceAuthorization(region)
	if err != nil {
		return fmt.Errorf("failed to request device authorization: %w", err)
	}
	if err := validateDeviceAuthorization(deviceAuth); err != nil {
		return err
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
	if err := validateAccessToken(tokenRes); err != nil {
		return err
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
	sealosDomain, err := validateRegionLoginData(regionData, initData)
	if err != nil {
		return err
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
		SealosDomain:     sealosDomain,
		AccessToken:      tokenRes.AccessToken,
		RegionalToken:    regionData.Data.Token,
		AuthenticatedAt:  time.Now().Format(time.RFC3339),
		AuthMethod:       "oauth2_device_grant",
		CurrentWorkspace: currentWorkspace,
	}
	if normalizedProfile != "" {
		if _, err := auth.SaveProfile(normalizedProfile, authData, regionData.Data.Kubeconfig); err != nil {
			return fmt.Errorf("failed to save profile %s: %w", normalizedProfile, err)
		}
		if err := auth.ActivateProfile(normalizedProfile); err != nil {
			return fmt.Errorf("failed to activate profile %s: %w", normalizedProfile, err)
		}
	} else {
		if err := auth.SaveAuthData(authData, regionData.Data.Kubeconfig); err != nil {
			return fmt.Errorf("failed to save auth data: %w", err)
		}
		if err := auth.ClearCurrentProfileName(); err != nil {
			return fmt.Errorf("failed to clear active profile marker: %w", err)
		}
	}

	fmt.Println("Authentication successful!")
	if normalizedProfile != "" {
		fmt.Printf("Active profile: %s\n", normalizedProfile)
	}
	if currentWorkspace != nil {
		fmt.Printf("Logged in to workspace: %s (%s)\n", currentWorkspace.ID, currentWorkspace.TeamName)
	}
	return nil
}

func validateDeviceAuthorization(deviceAuth *auth.DeviceAuthResponse) error {
	if deviceAuth == nil {
		return fmt.Errorf("device authorization response is empty")
	}
	if strings.TrimSpace(deviceAuth.DeviceCode) == "" {
		return fmt.Errorf("device authorization response did not include a device code")
	}
	if strings.TrimSpace(deviceAuth.UserCode) == "" {
		return fmt.Errorf("device authorization response did not include a user code")
	}
	// The user code is server-controlled and printed verbatim to the terminal.
	// Restrict it to the RFC 8628 user-code character set (uppercase
	// alphanumerics and hyphens) so a malicious/MITM server cannot inject
	// terminal escape sequences (fake prompts, OSC clipboard writes, etc.).
	if !userCodePattern.MatchString(strings.TrimSpace(deviceAuth.UserCode)) {
		return fmt.Errorf("device authorization response included a malformed user code")
	}
	if deviceAuth.ExpiresIn <= 0 {
		return fmt.Errorf("device authorization response included invalid expiration %d", deviceAuth.ExpiresIn)
	}
	if _, err := verificationURL(deviceAuth); err != nil {
		return err
	}
	return nil
}

func validateAccessToken(tokenRes *auth.TokenResponse) error {
	if tokenRes == nil || strings.TrimSpace(tokenRes.AccessToken) == "" {
		return fmt.Errorf("authorization response did not include an access token")
	}
	return nil
}

func validateRegionLoginData(regionData *auth.RegionTokenResponse, initData *auth.InitDataResponse) (string, error) {
	if regionData == nil || strings.TrimSpace(regionData.Data.Token) == "" {
		return "", fmt.Errorf("region token response did not include a regional token")
	}
	if regionData == nil || strings.TrimSpace(regionData.Data.Kubeconfig) == "" {
		return "", fmt.Errorf("region token response did not include kubeconfig")
	}
	if initData == nil || strings.TrimSpace(initData.Data.SealosDomain) == "" {
		return "", fmt.Errorf("region init data did not include SEALOS_DOMAIN")
	}
	sealosDomain, err := validateCustomDomain(initData.Data.SealosDomain)
	if err != nil {
		return "", fmt.Errorf("region init data included invalid SEALOS_DOMAIN: %w", err)
	}
	if sealosDomain == "" {
		return "", fmt.Errorf("region init data did not include SEALOS_DOMAIN")
	}
	return sealosDomain, nil
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
	return "", fmt.Errorf("device authorization response did not include a safe https verification URL")
}

func isSafeBrowserURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host != ""
}

func openBrowser(url string) {
	if !isSafeBrowserURL(url) && !isSafeLocalBrowserURL(url) {
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

	// #nosec G204 -- command is selected from a fixed OS allowlist and the URL is HTTPS-validated.
	if err := exec.Command(cmd, args...).Start(); err != nil {
		fmt.Printf("Browser opened failed: %v. Please open the URL manually.\n", err)
	} else {
		fmt.Println("Browser opened automatically.")
	}
}

func isSafeLocalBrowserURL(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
