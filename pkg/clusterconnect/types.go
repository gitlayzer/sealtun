package clusterconnect

import (
	"fmt"
	"strings"
)

const (
	ModeAuto = "auto"
	ModeTun  = "tun"
)

var transparentPlatformSupported = platformTransparentSupported

const (
	CapabilityKubeconfig         = "kubeconfig"
	CapabilityServicesGet        = "services.get"
	CapabilityServicesList       = "services.list"
	CapabilityEndpointSlicesList = "endpointslices.list"
	CapabilityPodsGet            = "pods.get"
	CapabilityPodsList           = "pods.list"
	CapabilityPodsPortForward    = "pods.portforward"
	CapabilityDeployments        = "deployments.create"
	CapabilitySecrets            = "secrets.create"
	CapabilityConfigMaps         = "configmaps.create"

	// Deprecated: service resolution uses discovery.k8s.io/v1 EndpointSlice.
	CapabilityEndpointsGet = "endpoints.get"
	// Deprecated: service resolution uses discovery.k8s.io/v1 EndpointSlice.
	CapabilityEndpointsList = "endpoints.list"
)

type Options struct {
	Mode      string
	Namespace string
}

type Capability struct {
	Name      string `json:"name"`
	Allowed   bool   `json:"allowed"`
	Required  bool   `json:"required"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type ModeStatus struct {
	Name      string   `json:"name"`
	Available bool     `json:"available"`
	Selected  bool     `json:"selected,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Requires  []string `json:"requires,omitempty"`
}

type Preflight struct {
	LoggedIn      bool         `json:"loggedIn"`
	ActiveProfile string       `json:"activeProfile,omitempty"`
	Region        string       `json:"region,omitempty"`
	Namespace     string       `json:"namespace,omitempty"`
	Mode          string       `json:"mode"`
	SelectedMode  string       `json:"selectedMode"`
	Capabilities  []Capability `json:"capabilities"`
	Modes         []ModeStatus `json:"modes"`
	Warnings      []string     `json:"warnings,omitempty"`
}

func NormalizeMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeAuto
	}
	switch mode {
	case ModeAuto, ModeTun:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported connect mode %q; use auto or tun", mode)
	}
}

func SelectMode(requested string, caps []Capability) (selected string, modes []ModeStatus, err error) {
	requested, err = NormalizeMode(requested)
	if err != nil {
		return "", nil, err
	}

	tunReqs := []string{
		CapabilityKubeconfig,
		CapabilityServicesGet,
		CapabilityServicesList,
		CapabilityEndpointSlicesList,
		CapabilityPodsGet,
		CapabilityPodsList,
		CapabilityPodsPortForward,
	}

	tunOK, tunReason := requirementsAllowed(caps, tunReqs)
	if tunOK {
		if ok, reason := transparentPlatformSupported(); !ok {
			tunOK = false
			tunReason = reason
		} else {
			tunReason = reason
		}
	}

	modes = []ModeStatus{
		{Name: ModeTun, Available: tunOK, Reason: tunReason, Requires: tunReqs},
	}

	switch requested {
	case ModeTun:
		if !tunOK {
			return "", modes, fmt.Errorf("tun mode is unavailable: %s", tunReason)
		}
		selected = ModeTun
	case ModeAuto:
		if !tunOK {
			return "", modes, fmt.Errorf("no compatible transparent connect mode is available: %s", tunReason)
		}
		selected = ModeTun
	}

	for i := range modes {
		modes[i].Selected = modes[i].Name == selected
	}
	return selected, modes, nil
}

func requirementsAllowed(caps []Capability, requirements []string) (bool, string) {
	byName := make(map[string]Capability, len(caps))
	for _, cap := range caps {
		byName[cap.Name] = cap
	}
	var missing []string
	for _, name := range requirements {
		cap, ok := byName[name]
		if !ok || !cap.Allowed {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return false, "missing " + strings.Join(missing, ", ")
	}
	return true, "all required capabilities are available"
}
