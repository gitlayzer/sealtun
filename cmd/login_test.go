package cmd

import (
	"testing"

	"github.com/labring/sealtun/pkg/auth"
)

func TestVerificationURLFallsBackToVerificationURI(t *testing.T) {
	got, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURI: "https://auth.example.com/device",
	})
	if err != nil {
		t.Fatalf("verificationURL returned error: %v", err)
	}
	if got != "https://auth.example.com/device" {
		t.Fatalf("unexpected verification URL: %s", got)
	}
}

func TestVerificationURLSkipsUnsafeCompleteURL(t *testing.T) {
	got, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURIComplete: "file:///tmp/not-safe",
		VerificationURI:         "https://auth.example.com/device",
	})
	if err != nil {
		t.Fatalf("verificationURL returned error: %v", err)
	}
	if got != "https://auth.example.com/device" {
		t.Fatalf("unexpected verification URL: %s", got)
	}
}

func TestVerificationURLRejectsUnsafeURLs(t *testing.T) {
	if _, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURIComplete: "file:///tmp/not-safe",
		VerificationURI:         "javascript:alert(1)",
	}); err == nil {
		t.Fatal("expected unsafe verification URLs to be rejected")
	}
}

func TestVerificationURLRejectsPlainHTTP(t *testing.T) {
	if _, err := verificationURL(&auth.DeviceAuthResponse{
		VerificationURI: "http://auth.example.com/device",
	}); err == nil {
		t.Fatal("expected plain http verification URL to be rejected")
	}
}

func TestValidateDeviceAuthorizationRejectsIncompleteResponses(t *testing.T) {
	valid := &auth.DeviceAuthResponse{
		DeviceCode:      "device-code",
		UserCode:        "user-code",
		VerificationURI: "https://auth.example.com/device",
		ExpiresIn:       600,
		Interval:        5,
	}

	tests := []struct {
		name string
		res  *auth.DeviceAuthResponse
	}{
		{name: "nil", res: nil},
		{name: "missing device code", res: func() *auth.DeviceAuthResponse {
			cp := *valid
			cp.DeviceCode = ""
			return &cp
		}()},
		{name: "missing user code", res: func() *auth.DeviceAuthResponse {
			cp := *valid
			cp.UserCode = ""
			return &cp
		}()},
		{name: "invalid expiration", res: func() *auth.DeviceAuthResponse {
			cp := *valid
			cp.ExpiresIn = 0
			return &cp
		}()},
		{name: "unsafe url", res: func() *auth.DeviceAuthResponse {
			cp := *valid
			cp.VerificationURI = "file:///tmp/not-safe"
			return &cp
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDeviceAuthorization(tt.res); err == nil {
				t.Fatal("expected invalid device authorization response to be rejected")
			}
		})
	}
}

func TestValidateDeviceAuthorizationAcceptsCompleteResponse(t *testing.T) {
	err := validateDeviceAuthorization(&auth.DeviceAuthResponse{
		DeviceCode:      "device-code",
		UserCode:        "user-code",
		VerificationURI: "https://auth.example.com/device",
		ExpiresIn:       600,
		Interval:        5,
	})
	if err != nil {
		t.Fatalf("expected device authorization response to pass: %v", err)
	}
}

func TestValidateAccessTokenRejectsEmptyToken(t *testing.T) {
	if err := validateAccessToken(&auth.TokenResponse{}); err == nil {
		t.Fatal("expected empty access token to be rejected")
	}
	if err := validateAccessToken(&auth.TokenResponse{AccessToken: "token"}); err != nil {
		t.Fatalf("expected access token to pass: %v", err)
	}
}

func TestValidateRegionLoginDataRejectsIncompleteResponses(t *testing.T) {
	regionData := &auth.RegionTokenResponse{}
	regionData.Data.Token = "regional-token"
	regionData.Data.Kubeconfig = "apiVersion: v1"
	initData := &auth.InitDataResponse{}
	initData.Data.SealosDomain = "sealoshzh.site"

	tests := []struct {
		name       string
		regionData *auth.RegionTokenResponse
		initData   *auth.InitDataResponse
	}{
		{name: "missing regional token", regionData: &auth.RegionTokenResponse{}, initData: initData},
		{name: "missing kubeconfig", regionData: func() *auth.RegionTokenResponse {
			res := &auth.RegionTokenResponse{}
			res.Data.Token = "regional-token"
			return res
		}(), initData: initData},
		{name: "missing init data", regionData: regionData, initData: nil},
		{name: "invalid sealos domain", regionData: regionData, initData: func() *auth.InitDataResponse {
			res := &auth.InitDataResponse{}
			res.Data.SealosDomain = "bad/domain"
			return res
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := validateRegionLoginData(tt.regionData, tt.initData); err == nil {
				t.Fatal("expected incomplete login data to be rejected")
			}
		})
	}
}

func TestValidateRegionLoginDataNormalizesSealosDomain(t *testing.T) {
	regionData := &auth.RegionTokenResponse{}
	regionData.Data.Token = "regional-token"
	regionData.Data.Kubeconfig = "apiVersion: v1"
	initData := &auth.InitDataResponse{}
	initData.Data.SealosDomain = "Sealoshzh.Site."

	got, err := validateRegionLoginData(regionData, initData)
	if err != nil {
		t.Fatalf("expected login data to pass: %v", err)
	}
	if got != "sealoshzh.site" {
		t.Fatalf("expected normalized sealos domain, got %s", got)
	}
}
