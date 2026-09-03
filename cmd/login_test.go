package cmd

import (
	"bufio"
	"errors"
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/auth"
)

func TestResolveLoginRegionInputKeepsExplicitRegion(t *testing.T) {
	called := false
	got, err := resolveLoginRegionInput([]string{"gzg"}, func() (string, error) {
		called = true
		return "hzh", nil
	})
	if err != nil {
		t.Fatalf("resolveLoginRegionInput returned error: %v", err)
	}
	if got != "gzg" {
		t.Fatalf("expected explicit region to win, got %q", got)
	}
	if called {
		t.Fatal("selector should not be called when a region argument is provided")
	}
}

func TestResolveLoginRegionInputUsesSelectorForBareLogin(t *testing.T) {
	got, err := resolveLoginRegionInput(nil, func() (string, error) {
		return "hzh", nil
	})
	if err != nil {
		t.Fatalf("resolveLoginRegionInput returned error: %v", err)
	}
	if got != "hzh" {
		t.Fatalf("expected selected region, got %q", got)
	}
}

func TestResolveLoginRegionInputRequiresRegionWithoutSelector(t *testing.T) {
	if _, err := resolveLoginRegionInput(nil, nil); err == nil || !strings.Contains(err.Error(), "region is required") {
		t.Fatalf("expected non-interactive region error, got %v", err)
	}
}

func TestSelectLoginRegionLineDefaultsAndAcceptsNamesNumbersAndURLs(t *testing.T) {
	regions := []auth.RegionOption{
		{Name: "gzg", URL: "https://gzg.sealos.run", SealosDomain: "sealosgzg.site"},
		{Name: "hzh", URL: "https://hzh.sealos.run", SealosDomain: "sealoshzh.site"},
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty default", input: "\n", want: "gzg"},
		{name: "number", input: "2\n", want: "hzh"},
		{name: "name", input: "hzh\n", want: "hzh"},
		{name: "url", input: "https://hzh.sealos.run\n", want: "hzh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			got, err := selectLoginRegionLine(strings.NewReader(tt.input), &out, regions, 0)
			if err != nil {
				t.Fatalf("selectLoginRegionLine returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
			if !strings.Contains(out.String(), "Choose a Sealos region") {
				t.Fatalf("expected prompt output, got %q", out.String())
			}
		})
	}
}

func TestSelectLoginRegionLineRejectsUnknownSelection(t *testing.T) {
	regions := []auth.RegionOption{{Name: "gzg", URL: "https://gzg.sealos.run"}}
	if _, err := selectLoginRegionLine(strings.NewReader("missing\n"), ioDiscard{}, regions, 0); err == nil || !strings.Contains(err.Error(), "unknown region selection") {
		t.Fatalf("expected unknown selection error, got %v", err)
	}
}

func TestReadLoginSelectorKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want loginSelectorKey
	}{
		{name: "enter", in: "\n", want: loginSelectorKeyEnter},
		{name: "up arrow", in: "\x1b[A", want: loginSelectorKeyUp},
		{name: "down arrow", in: "\x1b[B", want: loginSelectorKeyDown},
		{name: "j", in: "j", want: loginSelectorKeyDown},
		{name: "k", in: "k", want: loginSelectorKeyUp},
		{name: "q", in: "q", want: loginSelectorKeyCancel},
		{name: "ctrl c", in: "\x03", want: loginSelectorKeyCancel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readLoginSelectorKey(bufio.NewReader(strings.NewReader(tt.in)))
			if err != nil {
				t.Fatalf("readLoginSelectorKey returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected key %v, got %v", tt.want, got)
			}
		})
	}
}

func TestReadLoginSelectorKeyEscapeCancels(t *testing.T) {
	got, err := readLoginSelectorKey(bufio.NewReader(strings.NewReader("\x1b")))
	if err != nil {
		t.Fatalf("readLoginSelectorKey returned error: %v", err)
	}
	if got != loginSelectorKeyCancel {
		t.Fatalf("expected cancel, got %v", got)
	}
}

func TestLoginRegionSelectionCanceledError(t *testing.T) {
	if !errors.Is(errLoginRegionSelectionCanceled, errLoginRegionSelectionCanceled) {
		t.Fatal("expected cancellation sentinel to compare with errors.Is")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

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
	regionData.Data.Kubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://gzg.sealos.run:6443
    insecure-skip-tls-verify: true
  name: sealos
contexts:
- context:
    cluster: sealos
    user: u
    namespace: ns-x
  name: ctx
current-context: ctx
users:
- name: u
  user:
    token: fake
`
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
	regionData.Data.Kubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://gzg.sealos.run:6443
    insecure-skip-tls-verify: true
  name: sealos
contexts:
- context:
    cluster: sealos
    user: u
    namespace: ns-x
  name: ctx
current-context: ctx
users:
- name: u
  user:
    token: fake
`
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
