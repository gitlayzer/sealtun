package clusterconnect

import (
	"runtime"
	"strings"
	"testing"
)

func TestSelectModeAutoUsesPlatformTransparentSupport(t *testing.T) {
	orig := transparentPlatformSupported
	defer func() { transparentPlatformSupported = orig }()
	transparentPlatformSupported = func() (bool, string) {
		return true, "available"
	}
	caps := []Capability{
		{Name: CapabilityKubeconfig, Allowed: true},
		{Name: CapabilityServicesGet, Allowed: true},
		{Name: CapabilityServicesList, Allowed: true},
		{Name: CapabilityEndpointSlicesList, Allowed: true},
		{Name: CapabilityPodsGet, Allowed: true},
		{Name: CapabilityPodsList, Allowed: true},
		{Name: CapabilityPodsPortForward, Allowed: true},
		{Name: CapabilityDeployments, Allowed: true},
		{Name: CapabilitySecrets, Allowed: true},
		{Name: CapabilityConfigMaps, Allowed: true},
	}
	selected, modes, err := SelectMode(ModeAuto, caps)
	if err != nil {
		t.Fatalf("SelectMode returned error: %v", err)
	}
	if selected != ModeTun {
		t.Fatalf("expected tun mode, got %q", selected)
	}
	if len(modes) != 1 || !modes[0].Available {
		t.Fatalf("expected available tun mode, got %#v", modes)
	}
}

func TestSelectModeAutoReportsUnsupportedPlatform(t *testing.T) {
	orig := transparentPlatformSupported
	defer func() { transparentPlatformSupported = orig }()
	transparentPlatformSupported = func() (bool, string) {
		return false, "not here"
	}
	caps := []Capability{
		{Name: CapabilityKubeconfig, Allowed: true},
		{Name: CapabilityServicesGet, Allowed: true},
		{Name: CapabilityServicesList, Allowed: true},
		{Name: CapabilityEndpointSlicesList, Allowed: true},
		{Name: CapabilityPodsGet, Allowed: true},
		{Name: CapabilityPodsList, Allowed: true},
		{Name: CapabilityPodsPortForward, Allowed: true},
		{Name: CapabilityDeployments, Allowed: true},
		{Name: CapabilitySecrets, Allowed: true},
		{Name: CapabilityConfigMaps, Allowed: true},
	}
	selected, modes, err := SelectMode(ModeAuto, caps)
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
	if selected != "" {
		t.Fatalf("expected no selected mode, got %q", selected)
	}
	if len(modes) != 1 || modes[0].Available {
		t.Fatalf("expected unavailable tun mode, got %#v", modes)
	}
	if !strings.Contains(err.Error(), "not here") {
		t.Fatalf("expected platform reason, got %v", err)
	}
}

func TestPlatformTransparentSupportMatchesRuntime(t *testing.T) {
	ok, _ := platformTransparentSupported()
	if runtime.GOOS == "linux" && !ok {
		t.Fatal("linux should support transparent TCP mode")
	}
	if runtime.GOOS != "linux" && ok {
		t.Fatal("non-linux should not report transparent TCP support")
	}
}

func TestSelectModeReportsMissingCapabilities(t *testing.T) {
	caps := []Capability{{Name: CapabilityKubeconfig, Allowed: true}}
	_, _, err := SelectMode(ModeAuto, caps)
	if err == nil {
		t.Fatal("expected missing capability error")
	}
	if !strings.Contains(err.Error(), CapabilityPodsPortForward) {
		t.Fatalf("expected port-forward to be named in error, got %v", err)
	}
}
