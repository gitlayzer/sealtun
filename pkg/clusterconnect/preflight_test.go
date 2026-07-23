package clusterconnect

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeReviewer struct {
	allowed map[string]bool
	delay   time.Duration
}

type capabilityReviewCall struct {
	verb        string
	group       string
	resource    string
	subresource string
}

type recordingReviewer struct {
	mu    sync.Mutex
	calls []capabilityReviewCall
}

func (r *recordingReviewer) Review(_ context.Context, _ string, verb, group, resource, subresource string) (bool, string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, capabilityReviewCall{verb: verb, group: group, resource: resource, subresource: subresource})
	r.mu.Unlock()
	return true, "", nil
}

func (f fakeReviewer) Review(ctx context.Context, namespace, verb, group, resource, subresource string) (bool, string, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return false, "", ctx.Err()
		}
	}
	name := capabilityNameForReview(verb, group, resource, subresource)
	return f.allowed[name], "", nil
}

func BenchmarkProbeCapabilities(b *testing.B) {
	reviewer := fakeReviewer{allowed: map[string]bool{}, delay: time.Millisecond}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		caps := ProbeCapabilities(context.Background(), "ns-a", reviewer)
		if len(caps) != 10 {
			b.Fatalf("got %d capabilities, want 10", len(caps))
		}
	}
}

func capabilityNameForReview(verb, group, resource, subresource string) string {
	switch {
	case verb == "get" && resource == "services":
		return CapabilityServicesGet
	case verb == "list" && resource == "services":
		return CapabilityServicesList
	case verb == "list" && group == "discovery.k8s.io" && resource == "endpointslices":
		return CapabilityEndpointSlicesList
	case verb == "get" && resource == "pods":
		return CapabilityPodsGet
	case verb == "list" && resource == "pods":
		return CapabilityPodsList
	case verb == "create" && resource == "pods" && subresource == "portforward":
		return CapabilityPodsPortForward
	case verb == "create" && group == "apps" && resource == "deployments":
		return CapabilityDeployments
	case verb == "create" && resource == "secrets":
		return CapabilitySecrets
	case verb == "create" && resource == "configmaps":
		return CapabilityConfigMaps
	default:
		return verb + "." + group + "." + resource + "." + subresource
	}
}

func TestProbeCapabilities(t *testing.T) {
	allowed := map[string]bool{
		CapabilityServicesGet:        true,
		CapabilityServicesList:       true,
		CapabilityEndpointSlicesList: true,
		CapabilityPodsGet:            true,
		CapabilityPodsList:           true,
		CapabilityPodsPortForward:    true,
	}
	caps := ProbeCapabilities(context.Background(), "ns-a", fakeReviewer{allowed: allowed})
	wantOrder := []string{
		CapabilityKubeconfig,
		CapabilityServicesGet,
		CapabilityServicesList,
		CapabilityEndpointSlicesList,
		CapabilityPodsGet,
		CapabilityPodsList,
		CapabilityPodsPortForward,
		CapabilityDeployments,
		CapabilitySecrets,
		CapabilityConfigMaps,
	}
	if len(caps) != len(wantOrder) {
		t.Fatalf("got %d capabilities, want %d", len(caps), len(wantOrder))
	}
	byName := map[string]Capability{}
	for i, cap := range caps {
		if cap.Name != wantOrder[i] {
			t.Fatalf("capability %d = %q, want %q", i, cap.Name, wantOrder[i])
		}
		byName[cap.Name] = cap
		if cap.Namespace != "ns-a" {
			t.Fatalf("expected namespace ns-a, got %q", cap.Namespace)
		}
	}
	if !byName[CapabilityKubeconfig].Allowed {
		t.Fatal("kubeconfig capability should be allowed when environment loaded")
	}
	if !byName[CapabilityPodsPortForward].Allowed {
		t.Fatal("expected port-forward allowed")
	}
	if byName[CapabilityDeployments].Allowed {
		t.Fatal("deployment create should not be allowed by fake reviewer")
	}
}

func TestProbeCapabilitiesChecksEndpointSlicesUsedByResolver(t *testing.T) {
	reviewer := &recordingReviewer{}
	ProbeCapabilities(context.Background(), "ns-a", reviewer)

	reviewer.mu.Lock()
	defer reviewer.mu.Unlock()
	foundEndpointSlices := false
	for _, call := range reviewer.calls {
		if call.group == "" && call.resource == "endpoints" {
			t.Fatalf("preflight still checks unused core/v1 Endpoints: %#v", call)
		}
		if call.verb == "list" && call.group == "discovery.k8s.io" && call.resource == "endpointslices" && call.subresource == "" {
			foundEndpointSlices = true
		}
	}
	if !foundEndpointSlices {
		t.Fatal("preflight does not check discovery.k8s.io EndpointSlice list permission")
	}
}

func TestProbeCapabilitiesPropagatesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caps := ProbeCapabilities(ctx, "ns-a", fakeReviewer{delay: time.Second})
	for _, cap := range caps {
		if cap.Name == CapabilityKubeconfig {
			continue
		}
		if cap.Error != context.Canceled.Error() {
			t.Fatalf("capability %q error = %q, want %q", cap.Name, cap.Error, context.Canceled)
		}
	}
}
