package auth

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultRegion = "https://gzg.sealos.run"
	// ClientID from seakills for oauth2 device flow
	ClientID = "af993c98-d19d-4bdc-b338-79b80dc4f8bf"
)

type RegionOption struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	SealosDomain string `json:"sealosDomain,omitempty"`
}

var knownRegions = []RegionOption{
	{Name: "gzg", URL: "https://gzg.sealos.run", SealosDomain: "sealosgzg.site"},
	{Name: "hzh", URL: "https://hzh.sealos.run", SealosDomain: "sealoshzh.site"},
	{Name: "cloud", URL: "https://cloud.sealos.io", SealosDomain: "cloud.sealos.io"},
}

func KnownRegions() []RegionOption {
	items := make([]RegionOption, len(knownRegions))
	copy(items, knownRegions)
	return items
}

func ResolveRegion(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return DefaultRegion, nil
	}
	normalizedValue := strings.TrimRight(value, "/")

	for _, region := range knownRegions {
		if value == region.Name || normalizedValue == region.URL {
			return region.URL, nil
		}
	}

	return "", fmt.Errorf("unknown region %q; run `sealtun region list` to see supported regions", input)
}

var insecureSkipTLSVerify bool

func SetInsecureSkipTLSVerify(enabled bool) {
	insecureSkipTLSVerify = enabled
}

func httpClient() *http.Client {
	client := &http.Client{Timeout: 10 * time.Second}
	if insecureSkipTLSVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return client
}

type DeviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

type ErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type Namespace struct {
	UID      string      `json:"uid"`
	ID       string      `json:"id"`
	TeamName string      `json:"teamName"`
	Role     string      `json:"role"`
	NSType   interface{} `json:"nstype"`
}

type NamespaceListResponse struct {
	Data struct {
		Namespaces []Namespace `json:"namespaces"`
	} `json:"data"`
}

type InitDataResponse struct {
	Data struct {
		SealosDomain string `json:"SEALOS_DOMAIN"`
	} `json:"data"`
}

// RequestDeviceAuthorization requests the device code from the region
func RequestDeviceAuthorization(region string) (*DeviceAuthResponse, error) {
	apiURL := fmt.Sprintf("%s/api/auth/oauth2/device", strings.TrimRight(region, "/"))
	data := url.Values{}
	data.Set("client_id", ClientID)
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

	req, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	client := httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("device authorization failed (%d): %s", resp.StatusCode, string(body))
	}

	var res DeviceAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// PollForToken repeatedly polls the token endpoint until the user authenticates or it expires
func PollForToken(region, deviceCode string, interval, expiresIn int) (*TokenResponse, error) {
	apiURL := fmt.Sprintf("%s/api/auth/oauth2/token", strings.TrimRight(region, "/"))

	maxWait := time.Duration(expiresIn) * time.Second
	if maxWait > 10*time.Minute {
		maxWait = 10 * time.Minute
	}
	deadline := time.Now().Add(maxWait)
	pollInterval := time.Duration(interval) * time.Second
	if pollInterval == 0 {
		pollInterval = 5 * time.Second
	}

	client := httpClient()

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		data := url.Values{}
		data.Set("client_id", ClientID)
		data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		data.Set("device_code", deviceCode)

		req, err := http.NewRequest("POST", apiURL, strings.NewReader(data.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		resp, err := client.Do(req)
		if err != nil {
			continue // Network error, keep trying
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var tokenRes TokenResponse
			if err := json.Unmarshal(body, &tokenRes); err != nil {
				return nil, err
			}
			return &tokenRes, nil
		}

		var errRes ErrorResponse
		_ = json.Unmarshal(body, &errRes)

		switch errRes.Error {
		case "authorization_pending":
			// Keep polling
		case "slow_down":
			pollInterval += 5 * time.Second
		case "access_denied":
			return nil, fmt.Errorf("authorization denied by user")
		case "expired_token":
			return nil, fmt.Errorf("device code expired")
		default:
			return nil, fmt.Errorf("token request failed: %s %s", errRes.Error, errRes.ErrorDescription)
		}
	}

	return nil, fmt.Errorf("authorization timed out")
}

// RegionTokenResponse represents the response containing the region token and kubeconfig
type RegionTokenResponse struct {
	Data struct {
		Token      string `json:"token"`
		Kubeconfig string `json:"kubeconfig"`
	} `json:"data"`
}

// GetRegionToken exchanges the global access token for a regional token and kubeconfig
func GetRegionToken(region, accessToken string) (*RegionTokenResponse, error) {
	apiURL := fmt.Sprintf("%s/api/auth/regionToken", strings.TrimRight(region, "/"))
	req, err := http.NewRequest("POST", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", accessToken)
	req.Header.Add("Content-Type", "application/json")

	client := httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("region token exchange failed: %s", string(body))
	}

	var res RegionTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ListWorkspaces returns the list of namespaces/workspaces for the user
func ListWorkspaces(region, regionalToken string) (*NamespaceListResponse, error) {
	apiURL := fmt.Sprintf("%s/api/auth/namespace/list", strings.TrimRight(region, "/"))
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", regionalToken)

	client := httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list workspaces failed: %s", string(body))
	}

	var res NamespaceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func GetInitData(region string) (*InitDataResponse, error) {
	normalized, err := ResolveRegion(region)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("invalid region %q", region)
	}
	apiURL := fmt.Sprintf("https://applaunchpad.%s/api/platform/getInitData", u.Host)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	client := httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get init data failed (%d): %s", resp.StatusCode, string(body))
	}

	var res InitDataResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}
