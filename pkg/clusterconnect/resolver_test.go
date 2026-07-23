package clusterconnect

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type fakeClusterAPI struct {
	services       map[string]*corev1.Service
	endpointSlices map[string]*discoveryv1.EndpointSliceList
	pods           []corev1.Pod
}

func (f fakeClusterAPI) GetService(ctx context.Context, namespace, name string) (*corev1.Service, error) {
	return f.services[namespace+"/"+name], nil
}

func (f fakeClusterAPI) ListEndpointSlices(ctx context.Context, namespace, serviceName string) (*discoveryv1.EndpointSliceList, error) {
	return f.endpointSlices[namespace+"/"+serviceName], nil
}

func (f fakeClusterAPI) ListServices(ctx context.Context, namespace string) (*corev1.ServiceList, error) {
	var items []corev1.Service
	for _, svc := range f.services {
		if svc.Namespace == namespace {
			items = append(items, *svc)
		}
	}
	return &corev1.ServiceList{Items: items}, nil
}

func (f fakeClusterAPI) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error) {
	var items []corev1.Pod
	for _, pod := range f.pods {
		if pod.Namespace == namespace {
			items = append(items, pod)
		}
	}
	return &corev1.PodList{Items: items}, nil
}

func TestResolverServiceDNS(t *testing.T) {
	resolver := &Resolver{Client: fakeResolverAPI(), DefaultNamespace: "default"}
	target, err := resolver.Resolve(context.Background(), "web.default.svc.cluster.local", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if target.Namespace != "default" || target.PodName != "web-0" || target.PodPort != 3000 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolverServiceShortFQDN(t *testing.T) {
	resolver := &Resolver{Client: fakeResolverAPI(), DefaultNamespace: "default"}
	target, err := resolver.Resolve(context.Background(), "web.default.svc", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if target.Namespace != "default" || target.PodName != "web-0" || target.PodPort != 3000 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolverDoesNotTreatTwoLabelHostAsServiceNamespace(t *testing.T) {
	resolver := &Resolver{Client: fakeResolverAPI(), DefaultNamespace: "default"}
	if _, err := resolver.Resolve(context.Background(), "example.com", 8080); err == nil {
		t.Fatal("expected two-label host to be rejected")
	}
}

func TestResolverServiceClusterIP(t *testing.T) {
	resolver := &Resolver{Client: fakeResolverAPI(), DefaultNamespace: "default"}
	target, err := resolver.Resolve(context.Background(), "10.96.0.12", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if target.PodName != "web-0" || target.PodPort != 3000 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolverPodIP(t *testing.T) {
	resolver := &Resolver{Client: fakeResolverAPI(), DefaultNamespace: "default"}
	target, err := resolver.Resolve(context.Background(), "10.244.0.22", 3000)
	if err != nil {
		t.Fatal(err)
	}
	if target.PodName != "web-0" || target.PodPort != 3000 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolverPodIPRejectsOutOfRangePort(t *testing.T) {
	resolver := &Resolver{Client: fakeResolverAPI(), DefaultNamespace: "default"}
	if _, err := resolver.Resolve(context.Background(), "10.244.0.22", 70000); err == nil {
		t.Fatal("expected out-of-range pod port to fail")
	}
}

func TestResolverSelectsReadyEndpointAcrossSlices(t *testing.T) {
	api := fakeResolverAPI()
	ready := true
	notReady := false
	terminating := true
	portName := "http"
	port := int32(3000)
	api.endpointSlices["default/web"] = &discoveryv1.EndpointSliceList{Items: []discoveryv1.EndpointSlice{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "web-a", Namespace: "default"},
			Ports:      []discoveryv1.EndpointPort{{Name: &portName, Port: &port}},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.244.0.20"}, Conditions: discoveryv1.EndpointConditions{Ready: &notReady}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "not-ready"}},
				{Addresses: []string{"10.244.0.21"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready, Terminating: &terminating}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "terminating"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "web-b", Namespace: "default"},
			Ports:      []discoveryv1.EndpointPort{{Name: &portName, Port: &port}},
			Endpoints:  []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.22"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "default", Name: "web-0"}}},
		},
	}}

	resolver := &Resolver{Client: api, DefaultNamespace: "default"}
	target, err := resolver.Resolve(context.Background(), "web.default.svc.cluster.local", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if target.PodName != "web-0" || target.PodPort != 3000 {
		t.Fatalf("unexpected ready target: %#v", target)
	}
}

func fakeResolverAPI() fakeClusterAPI {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.12",
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       8080,
				TargetPort: intstr.FromInt32(3000),
			}},
		},
	}
	portName := "http"
	port := int32(3000)
	ready := true
	endpointSlices := &discoveryv1.EndpointSliceList{Items: []discoveryv1.EndpointSlice{{
		ObjectMeta:  metav1.ObjectMeta{Name: "web-abc", Namespace: "default", Labels: map[string]string{discoveryv1.LabelServiceName: "web"}},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.22"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: "default", Name: "web-0"}}},
		Ports:       []discoveryv1.EndpointPort{{Name: &portName, Port: &port}},
	}}}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.22"},
	}
	return fakeClusterAPI{
		services:       map[string]*corev1.Service{"default/web": svc},
		endpointSlices: map[string]*discoveryv1.EndpointSliceList{"default/web": endpointSlices},
		pods:           []corev1.Pod{pod},
	}
}
