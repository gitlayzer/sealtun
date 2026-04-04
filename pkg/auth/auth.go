package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultRegion = "https://cloud.sealos.io"
	// ClientID from seakills for oauth2 device flow
	ClientID = "9vjofg2q7c8u1lxp4zrw3hyk5e0bam6" 
)

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

	client := &http.Client{Timeout: 10 * time.Second}
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

	client := &http.Client{Timeout: 10 * time.Second}

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

	client := &http.Client{Timeout: 10 * time.Second}
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
