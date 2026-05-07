package k8s

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/auth"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestDomainInferenceForSealosRegions(t *testing.T) {
	tests := []struct {
		name   string
		region string
		want   string
	}{
		{name: "gzg run", region: "https://gzg.sealos.run", want: "sealosgzg.site"},
		{name: "hzh run", region: "https://hzh.sealos.run", want: "sealoshzh.site"},
		{name: "cloud io", region: "https://cloud.sealos.io", want: "cloud.sealos.app"},
		{name: "custom", region: "https://apps.example.com", want: "apps.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := newClientFromRawConfig(&rest.Config{Host: "https://kubernetes.example.com"}, rawConfigForTest(), &auth.AuthData{Region: tt.region})
			if err != nil {
				t.Fatalf("newClientFromRawConfig returned error: %v", err)
			}
			if client.domain != tt.want {
				t.Fatalf("expected domain %s, got %s", tt.want, client.domain)
			}
		})
	}
}

func TestDomainUsesSealosDomainFromAuthDataWhenPresent(t *testing.T) {
	client, err := newClientFromRawConfig(&rest.Config{Host: "https://kubernetes.example.com"}, rawConfigForTest(), &auth.AuthData{
		Region:       "https://hzh.sealos.run",
		SealosDomain: "custom.sealos.example",
	})
	if err != nil {
		t.Fatalf("newClientFromRawConfig returned error: %v", err)
	}
	if client.domain != "custom.sealos.example" {
		t.Fatalf("expected custom sealos domain to win, got %s", client.domain)
	}
}

func TestEnsureTunnelCleansPartialResourcesOnFailure(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("ingress create failed")
	})
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnel(context.Background(), "abc123", "secret", "https", "3000"); err == nil {
		t.Fatal("expected EnsureTunnel to fail")
	}

	deployments, err := clientset.AppsV1().Deployments("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(deployments.Items) != 0 {
		t.Fatalf("expected partial deployment to be cleaned up, got %d", len(deployments.Items))
	}
	services, err := clientset.CoreV1().Services("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	if len(services.Items) != 0 {
		t.Fatalf("expected partial service to be cleaned up, got %d", len(services.Items))
	}
}

func TestEnsureTunnelRollbackDoesNotDeletePreexistingResources(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	})
	clientset.PrependReactor("create", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("service create failed")
	})
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnel(context.Background(), "abc123", "secret", "https", "3000"); err == nil {
		t.Fatal("expected EnsureTunnel to fail")
	}

	if _, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected preexisting deployment to be preserved: %v", err)
	}
}

func TestEnsureTunnelUsesCompactHostAndSingleIngressWithBothPaths(t *testing.T) {
	longNamespace := "namespace-with-a-very-long-name-that-would-overflow-the-public-host-label"
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: longNamespace,
		domain:    "example.com",
	}

	host, err := client.EnsureTunnel(context.Background(), "abc123", "secret", "https", "3000")
	if err != nil {
		t.Fatalf("EnsureTunnel returned error: %v", err)
	}
	firstLabel := strings.Split(host, ".")[0]
	if len(firstLabel) > 63 {
		t.Fatalf("expected first host label to fit DNS limit, got %d: %s", len(firstLabel), firstLabel)
	}

	ingresses, err := clientset.NetworkingV1().Ingresses(longNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list ingresses: %v", err)
	}
	if len(ingresses.Items) != 1 {
		t.Fatalf("expected one ingress, got %d", len(ingresses.Items))
	}

	paths := ingresses.Items[0].Spec.Rules[0].HTTP.Paths
	if len(paths) != 3 {
		t.Fatalf("expected tunnel and app paths in one ingress, got %d", len(paths))
	}
	if paths[0].Path != "/_sealtun/ws" || paths[1].Path != "/_sealtun/healthz" || paths[2].Path != "/" {
		t.Fatalf("unexpected ingress paths: %#v", paths)
	}
}

func TestDiagnoseTunnelReportsRemoteResources(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.0.0.1",
				Ports:     []corev1.ServicePort{{Protocol: corev1.ProtocolTCP, Port: 80, TargetPort: intstr.FromInt(8080)}},
			},
		},
		&netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: netv1.IngressSpec{
				IngressClassName: stringPtr("nginx"),
				Rules: []netv1.IngressRule{{
					Host:             "abc.example.com",
					IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{Paths: []netv1.HTTPIngressPath{{Path: "/"}}}},
				}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: map[string]string{"app": name}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				}},
				ContainerStatuses: []corev1.ContainerStatus{{Name: name, Ready: true, Image: "sealtun:test"}},
			},
		},
	)
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	diag, err := client.DiagnoseTunnel(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("DiagnoseTunnel returned error: %v", err)
	}
	if !diag.Deployment.Exists || diag.Deployment.ReadyReplicas != 1 {
		t.Fatalf("unexpected deployment diagnostics: %#v", diag.Deployment)
	}
	if !diag.Service.Exists || len(diag.Service.Ports) != 1 {
		t.Fatalf("unexpected service diagnostics: %#v", diag.Service)
	}
	if !diag.Ingress.Exists {
		t.Fatalf("unexpected ingress diagnostics: %#v", diag.Ingress)
	}
	if len(diag.Pods) != 1 || !diag.Pods[0].Ready {
		t.Fatalf("unexpected pod diagnostics: %#v", diag.Pods)
	}
	if len(diag.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", diag.Warnings)
	}
}

func TestDiagnoseTunnelTreatsEventListFailureAsWarning(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Protocol: corev1.ProtocolTCP, Port: 80}}},
		},
		&netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: netv1.IngressSpec{Rules: []netv1.IngressRule{{
				Host:             "abc.example.com",
				IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{Paths: []netv1.HTTPIngressPath{{Path: "/"}}}},
			}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: map[string]string{"app": name}},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}},
		},
	)
	clientset.PrependReactor("list", "events", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("events forbidden")
	})
	client := &Client{clientset: clientset, namespace: "default", domain: "example.com"}

	diag, err := client.DiagnoseTunnel(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("DiagnoseTunnel should not fail when events are unavailable: %v", err)
	}
	if !diag.Deployment.Exists || !diag.Service.Exists || !diag.Ingress.Exists {
		t.Fatalf("expected resource diagnostics to be preserved: %#v", diag)
	}
	if len(diag.Warnings) == 0 || !strings.Contains(diag.Warnings[len(diag.Warnings)-1], "remote events unavailable") {
		t.Fatalf("expected events warning, got %#v", diag.Warnings)
	}
}

func TestFilterEventDiagnosticsMatchesExactObjectName(t *testing.T) {
	events := []corev1.Event{
		{InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "sealtun-abc123"}, Reason: "Exact"},
		{InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "prefix-sealtun-abc123-suffix"}, Reason: "Substring"},
	}

	result := filterEventDiagnostics(events, "sealtun-abc123", 10)
	if len(result) != 1 {
		t.Fatalf("expected only exact event match, got %#v", result)
	}
	if result[0].Reason != "Exact" {
		t.Fatalf("unexpected matched event: %#v", result[0])
	}
}

func TestCleanupManagedOnlyDeletesTrackedTunnelNames(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name:      "sealtun-abc123",
			Namespace: "default",
			Labels:    map[string]string{"cloud.sealos.io/app-deploy-manager": "sealtun-abc123"},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      "sealtun-abc123",
			Namespace: "default",
			Labels:    map[string]string{"cloud.sealos.io/app-deploy-manager": "sealtun-abc123"},
		}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{
			Name:      "sealtun-abc123",
			Namespace: "default",
			Labels:    map[string]string{"cloud.sealos.io/app-deploy-manager": "sealtun-abc123"},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name:      "unrelated-app",
			Namespace: "default",
			Labels:    map[string]string{"cloud.sealos.io/app-deploy-manager": "unrelated-app"},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      "unrelated-app",
			Namespace: "default",
			Labels:    map[string]string{"cloud.sealos.io/app-deploy-manager": "unrelated-app"},
		}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{
			Name:      "unrelated-app",
			Namespace: "default",
			Labels:    map[string]string{"cloud.sealos.io/app-deploy-manager": "unrelated-app"},
		}},
	)
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	summary, err := client.CleanupManaged(context.Background(), []string{"abc123"})
	if err != nil {
		t.Fatalf("CleanupManaged returned error: %v", err)
	}
	if summary.Deployments != 1 || summary.Services != 1 || summary.Ingresses != 1 {
		t.Fatalf("unexpected cleanup summary: %#v", summary)
	}

	if _, err := clientset.AppsV1().Deployments("default").Get(context.Background(), "unrelated-app", metav1.GetOptions{}); err != nil {
		t.Fatalf("unrelated deployment should remain: %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), "unrelated-app", metav1.GetOptions{}); err != nil {
		t.Fatalf("unrelated service should remain: %v", err)
	}
	if _, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), "unrelated-app", metav1.GetOptions{}); err != nil {
		t.Fatalf("unrelated ingress should remain: %v", err)
	}
}

func rawConfigForTest() clientcmdapi.Config {
	return clientcmdapi.Config{
		CurrentContext: "ctx",
		Contexts: map[string]*clientcmdapi.Context{
			"ctx": {
				Cluster:   "cluster",
				AuthInfo:  "user",
				Namespace: "ns-demo",
			},
		},
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {Server: "https://kubernetes.example.com"},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {Token: "token"},
		},
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}

func stringPtr(value string) *string {
	return &value
}
