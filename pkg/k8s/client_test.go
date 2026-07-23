package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/publicauth"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynamicfake "k8s.io/client-go/dynamic/fake"
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
		{name: "bja run", region: "https://bja.sealos.run", want: "sealosbja.site"},
		{name: "cloud io", region: "https://cloud.sealos.io", want: "cloud.sealos.io"},
		{name: "usw io", region: "https://usw-1.sealos.io", want: "usw-1.sealos.app"},
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

func TestDomainRejectsInvalidSealosDomainFromAuthData(t *testing.T) {
	_, err := newClientFromRawConfig(&rest.Config{Host: "https://kubernetes.example.com"}, rawConfigForTest(), &auth.AuthData{
		Region:       "https://hzh.sealos.run",
		SealosDomain: "bad/domain",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid Sealos ingress domain") {
		t.Fatalf("expected invalid Sealos domain error, got %v", err)
	}
}

func TestNewClientRejectsSymlinkedKubeconfig(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside-kubeconfig")
	if err := os.WriteFile(outside, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "kubeconfig")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	_, err := NewClient(linked, &auth.AuthData{Region: "https://gzg.sealos.run"})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlinked kubeconfig to be rejected, got %v", err)
	}
}

func TestTunnelRemoteStateSkipsServicesForHTTPTunnel(t *testing.T) {
	name := tunnelResourceName("abc123")
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Args: []string{"server", "--protocol", "https", "--local-port", "3000"},
			}}}}},
		},
		&netv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       netv1.IngressSpec{Rules: []netv1.IngressRule{{Host: "sealtun-abc123-default.example.com"}}},
		},
	)
	client := &Client{clientset: clientset, namespace: "default", domain: "example.com"}

	state, err := client.TunnelRemoteState(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("TunnelRemoteState returned error: %v", err)
	}
	if state.Protocol != "https" || state.SealosHost == "" {
		t.Fatalf("unexpected HTTP remote state: %#v", state)
	}
	for _, action := range clientset.Actions() {
		if action.GetResource().Resource == "services" {
			t.Fatalf("HTTP remote state issued unnecessary service request: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
	}
}

func TestTunnelRemoteStateSkipsIngressForTCPTunnel(t *testing.T) {
	name := tunnelResourceName("abc123")
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Args: []string{"server", "--protocol", "tcp", "--local-port", "5432"},
			}}}}},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: tcpServiceName(name), Namespace: "default"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{NodePort: 35432}}},
		},
	)
	client := &Client{clientset: clientset, namespace: "default", domain: "example.com"}

	state, err := client.TunnelRemoteState(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("TunnelRemoteState returned error: %v", err)
	}
	if state.Protocol != "tcp" || state.PublicPort != 35432 {
		t.Fatalf("unexpected TCP remote state: %#v", state)
	}
	for _, action := range clientset.Actions() {
		if action.GetResource().Resource == "ingresses" {
			t.Fatalf("TCP remote state issued unnecessary ingress request: %s %s", action.GetVerb(), action.GetResource().Resource)
		}
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
	secrets, err := clientset.CoreV1().Secrets("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Fatalf("expected partial auth secret to be cleaned up, got %d", len(secrets.Items))
	}
}

func TestEnsureTunnelSkipsUpdatesWhenManagedResourcesAreUnchanged(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	for i := 0; i < 2; i++ {
		if _, err := client.EnsureTunnel(context.Background(), "abc123", "secret", "https", "3000"); err != nil {
			t.Fatalf("EnsureTunnel call %d returned error: %v", i+1, err)
		}
		if i == 0 {
			clientset.ClearActions()
		}
	}

	var updates []string
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "update" {
			updates = append(updates, action.GetResource().Resource)
		}
	}
	if len(updates) != 0 {
		t.Fatalf("unchanged tunnel reconciliation issued update calls: %v", updates)
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

func TestEnsureTunnelRejectsUnmanagedSameNameResource(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	})
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnel(context.Background(), "abc123", "secret", "https", "3000"); err == nil || !strings.Contains(err.Error(), "not managed by Sealtun") {
		t.Fatalf("expected unmanaged resource rejection, got %v", err)
	}
	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected unmanaged deployment to remain: %v", err)
	}
	if managedLabelMatches(deployment.Labels, name) {
		t.Fatalf("unmanaged deployment labels were modified: %#v", deployment.Labels)
	}
}

func TestEnsureTunnelRejectsUnsafeInputsBeforeCreatingResources(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	tests := []struct {
		name      string
		tunnelID  string
		secret    string
		protocol  string
		localPort string
		opts      TunnelOptions
	}{
		{name: "path traversal tunnel id", tunnelID: "../auth", secret: "secret", protocol: "https", localPort: "3000"},
		{name: "empty secret", tunnelID: "abc123", secret: "", protocol: "https", localPort: "3000"},
		{name: "unsupported protocol", tunnelID: "abc123", secret: "secret", protocol: "grpc", localPort: "3000"},
		{name: "invalid local port", tunnelID: "abc123", secret: "secret", protocol: "https", localPort: "70000"},
		{name: "invalid custom domain", tunnelID: "abc123", secret: "secret", protocol: "https", localPort: "3000", opts: TunnelOptions{CustomDomain: "https://app.example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.EnsureTunnelWithOptions(context.Background(), tt.tunnelID, tt.secret, tt.protocol, tt.localPort, tt.opts); err == nil {
				t.Fatal("expected invalid input to be rejected")
			}
		})
	}

	secrets, err := clientset.CoreV1().Secrets("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Fatalf("expected no resources to be created for invalid inputs, got %d secrets", len(secrets.Items))
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
	if len(paths) != 5 {
		t.Fatalf("expected tunnel and app paths in one ingress, got %d", len(paths))
	}
	if paths[0].Path != "/_sealtun/ws" || paths[1].Path != "/_sealtun/healthz" || paths[2].Path != "/_sealtun/metrics" || paths[3].Path != "/_sealtun/audit" || paths[4].Path != "/" {
		t.Fatalf("unexpected ingress paths: %#v", paths)
	}
}

func TestEnsureTunnelSSHCreatesNodePortService(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		service := create.GetObject().(*corev1.Service)
		for i := range service.Spec.Ports {
			if service.Spec.Ports[i].Name == "tcp" {
				service.Spec.Ports[i].NodePort = 32022
			}
		}
		return false, nil, nil
	})
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	hosts, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "ssh", "22", TunnelOptions{})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	if hosts.PublicPort != 32022 {
		t.Fatalf("expected public nodePort 32022, got %d", hosts.PublicPort)
	}
	if hosts.PublicHost != "sealtun-abc123-default.example.com" {
		t.Fatalf("unexpected public host: %s", hosts.PublicHost)
	}

	service, err := clientset.CoreV1().Services("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("expected HTTP service to remain ClusterIP, got %s", service.Spec.Type)
	}
	if port := servicePortByName(service, "http"); port == nil || port.Port != 80 || port.TargetPort.IntVal != 8080 {
		t.Fatalf("expected http service port 80 -> 8080, got %#v", port)
	}
	if port := servicePortByName(service, "tcp"); port != nil {
		t.Fatalf("expected HTTP service not to expose tcp NodePort, got %#v", port)
	}

	tcpService, err := clientset.CoreV1().Services("default").Get(context.Background(), "sealtun-abc123-tcp", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tcpService.Spec.Type != corev1.ServiceTypeNodePort {
		t.Fatalf("expected TCP service to be NodePort, got %s", tcpService.Spec.Type)
	}
	if port := servicePortByName(tcpService, "tcp"); port == nil || port.Port != 2222 || port.TargetPort.IntVal != 2222 {
		t.Fatalf("expected tcp service port 2222 -> 2222, got %#v", port)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if containerPortByName(deployment, "tcp") == nil {
		t.Fatalf("expected ssh deployment to expose tcp container port, got %#v", deployment.Spec.Template.Spec.Containers[0].Ports)
	}

	ingress, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	paths := ingress.Spec.Rules[0].HTTP.Paths
	if len(paths) != 5 {
		t.Fatalf("expected ssh ingress to expose control paths only, got %#v", paths)
	}
	if paths[0].Path != "/_sealtun/ws" || paths[1].Path != "/_sealtun/healthz" || paths[2].Path != "/_sealtun/metrics" || paths[3].Path != "/_sealtun/audit" || paths[4].Path != "/_sealtun/tcp" {
		t.Fatalf("unexpected ssh ingress paths: %#v", paths)
	}
}

func TestEnsureTunnelTCPCreatesNodePortService(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		service := create.GetObject().(*corev1.Service)
		for i := range service.Spec.Ports {
			if service.Spec.Ports[i].Name == "tcp" {
				service.Spec.Ports[i].NodePort = 35432
			}
		}
		return false, nil, nil
	})
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	hosts, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "tcp", "5432", TunnelOptions{})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	if hosts.PublicPort != 35432 {
		t.Fatalf("expected public nodePort 35432, got %d", hosts.PublicPort)
	}
	if hosts.PublicHost != "sealtun-abc123-default.example.com" {
		t.Fatalf("unexpected public host: %s", hosts.PublicHost)
	}

	tcpService, err := clientset.CoreV1().Services("default").Get(context.Background(), "sealtun-abc123-tcp", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tcpService.Spec.Type != corev1.ServiceTypeNodePort {
		t.Fatalf("expected TCP service to be NodePort, got %s", tcpService.Spec.Type)
	}
	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if containerPortByName(deployment, "tcp") == nil {
		t.Fatalf("expected tcp deployment to expose tcp container port, got %#v", deployment.Spec.Template.Spec.Containers[0].Ports)
	}
}

func TestEnsureTunnelSSHRejectsHTTPOnlyOptions(t *testing.T) {
	client := &Client{
		clientset: fake.NewSimpleClientset(),
		namespace: "default",
		domain:    "example.com",
	}

	tests := []struct {
		name string
		opts TunnelOptions
	}{
		{name: "custom domain", opts: TunnelOptions{CustomDomain: "dev.example.com"}},
		{name: "basic auth", opts: TunnelOptions{BasicAuth: &BasicAuthOptions{Username: "admin", PasswordHash: "hash"}}},
		{name: "access policy", opts: TunnelOptions{AccessPolicy: &accesspolicy.Policy{IPAllowlist: []string{"203.0.113.1"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "ssh", "22", tt.opts); err == nil {
				t.Fatal("expected ssh tunnel with HTTP-only option to fail")
			}
		})
	}
}

func TestEnsureTunnelTCPRejectsHTTPOnlyOptions(t *testing.T) {
	client := &Client{
		clientset: fake.NewSimpleClientset(),
		namespace: "default",
		domain:    "example.com",
	}

	tests := []struct {
		name string
		opts TunnelOptions
	}{
		{name: "custom domain", opts: TunnelOptions{CustomDomain: "dev.example.com"}},
		{name: "basic auth", opts: TunnelOptions{BasicAuth: &BasicAuthOptions{Username: "admin", PasswordHash: "hash"}}},
		{name: "access policy", opts: TunnelOptions{AccessPolicy: &accesspolicy.Policy{IPAllowlist: []string{"203.0.113.1"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "tcp", "5432", tt.opts); err == nil {
				t.Fatal("expected tcp tunnel with HTTP-only option to fail")
			}
		})
	}
}

func TestTunnelPodLogOptionsPreservesZeroTail(t *testing.T) {
	t.Parallel()

	opts := tunnelPodLogOptions("sealtun-abc123", TunnelLogOptions{TailLines: 0})
	if opts.TailLines == nil {
		t.Fatal("expected TailLines pointer to be set for --tail 0")
	}
	if *opts.TailLines != 0 {
		t.Fatalf("expected TailLines 0, got %d", *opts.TailLines)
	}
}

func TestCleanupCreatedSkipsUnmanagedRaceResources(t *testing.T) {
	name := "sealtun-abc123"
	authName := authSecretName(name)
	issuer := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Issuer",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "default",
		},
	}}
	certificate := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "default",
		},
	}}
	clientset := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: authName, Namespace: "default"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, certificate),
		namespace:     "default",
		domain:        "example.com",
	}

	err := client.cleanupCreated(context.Background(), []createdResource{
		{kind: resourceSecret, name: authName},
		{kind: resourceDeployment, name: name},
		{kind: resourceService, name: name},
		{kind: resourceIngress, name: name},
		{kind: resourceIssuer, name: name},
		{kind: resourceCertificate, name: name},
	})
	if err != nil {
		t.Fatalf("cleanupCreated returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authName, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged auth secret should remain: %v", err)
	}
	if _, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged deployment should remain: %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged service should remain: %v", err)
	}
	if _, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged ingress should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged issuer should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged certificate should remain: %v", err)
	}
}

func TestRestoreDynamicResourceDoesNotDeleteUnmanagedReplacement(t *testing.T) {
	name := "sealtun-abc123"
	replacement := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Issuer",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "default",
		},
	}}
	client := &Client{
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), replacement),
		namespace:     "default",
		domain:        "example.com",
	}

	if err := client.restoreDynamicResource(context.Background(), issuerGVR, name, nil); err != nil {
		t.Fatalf("restoreDynamicResource returned error: %v", err)
	}
	if _, err := client.dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged replacement should remain: %v", err)
	}
}

func TestSanitizeDynamicResourceForApplyDropsServerManagedFields(t *testing.T) {
	resource := customDomainCertificate("sealtun-abc123", "dev.example.com")
	resource.SetUID("uid-123")
	resource.SetResourceVersion("12345")
	resource.SetGeneration(7)
	resource.SetCreationTimestamp(metav1.Now())
	resource.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "test"}})
	resource.Object["status"] = map[string]interface{}{"ready": true}

	sanitizeDynamicResourceForApply(resource)

	if resource.GetUID() != "" {
		t.Fatalf("expected UID to be cleared, got %s", resource.GetUID())
	}
	if resource.GetResourceVersion() != "" {
		t.Fatalf("expected resourceVersion to be cleared, got %s", resource.GetResourceVersion())
	}
	if resource.GetGeneration() != 0 {
		t.Fatalf("expected generation to be cleared, got %d", resource.GetGeneration())
	}
	if !resource.GetCreationTimestamp().Time.IsZero() {
		t.Fatalf("expected creation timestamp to be cleared, got %s", resource.GetCreationTimestamp())
	}
	if len(resource.GetManagedFields()) != 0 {
		t.Fatalf("expected managed fields to be cleared, got %#v", resource.GetManagedFields())
	}
	if _, ok := resource.Object["status"]; ok {
		t.Fatalf("expected status to be removed, got %#v", resource.Object["status"])
	}
}

func TestEnsureTunnelStoresAuthSecretOutsideDeploymentArgs(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnel(context.Background(), "abc123", "raw-secret", "https", "3000"); err != nil {
		t.Fatalf("EnsureTunnel returned error: %v", err)
	}

	authSecret, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected auth secret to be created: %v", err)
	}
	if got := string(authSecret.Data[tunnelAuthSecretKey]); got != "raw-secret" {
		t.Fatalf("unexpected auth secret value: %q", got)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if strings.Contains(strings.Join(container.Args, " "), "raw-secret") {
		t.Fatalf("deployment args must not contain raw tunnel secret: %#v", container.Args)
	}
	if len(container.Env) != 1 || container.Env[0].ValueFrom == nil || container.Env[0].ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected deployment to reference auth secret via env var: %#v", container.Env)
	}
	if container.Env[0].ValueFrom.SecretKeyRef.Name != authSecretName(name) || container.Env[0].ValueFrom.SecretKeyRef.Key != tunnelAuthSecretKey {
		t.Fatalf("unexpected auth secret ref: %#v", container.Env[0].ValueFrom.SecretKeyRef)
	}
}

func TestEnsureTunnelUsesHardenedPodSecurityContext(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnel(context.Background(), "abc123", "raw-secret", "https", "3000"); err != nil {
		t.Fatalf("EnsureTunnel returned error: %v", err)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	podSpec := deployment.Spec.Template.Spec
	if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
		t.Fatalf("expected service account token automount to be disabled, got %#v", podSpec.AutomountServiceAccountToken)
	}
	if len(podSpec.Volumes) != 1 || podSpec.Volumes[0].Name != "tmp" || podSpec.Volumes[0].EmptyDir == nil {
		t.Fatalf("expected writable /tmp emptyDir volume, got %#v", podSpec.Volumes)
	}

	container := podSpec.Containers[0]
	if got := container.Resources.Requests[corev1.ResourceCPU]; got.String() != DefaultRequestCPU {
		t.Fatalf("expected default cpu request %s, got %s", DefaultRequestCPU, got.String())
	}
	if got := container.Resources.Requests[corev1.ResourceMemory]; got.String() != DefaultRequestMemory {
		t.Fatalf("expected default memory request %s, got %s", DefaultRequestMemory, got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceCPU]; got.String() != DefaultLimitCPU {
		t.Fatalf("expected default cpu limit %s, got %s", DefaultLimitCPU, got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.String() != DefaultLimitMemory {
		t.Fatalf("expected default memory limit %s, got %s", DefaultLimitMemory, got.String())
	}
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].Name != "tmp" || container.VolumeMounts[0].MountPath != "/tmp" {
		t.Fatalf("expected /tmp volume mount, got %#v", container.VolumeMounts)
	}
	securityContext := container.SecurityContext
	if securityContext == nil {
		t.Fatal("expected container security context")
	}
	if securityContext.ReadOnlyRootFilesystem == nil || !*securityContext.ReadOnlyRootFilesystem {
		t.Fatalf("expected read-only root filesystem, got %#v", securityContext.ReadOnlyRootFilesystem)
	}
	if securityContext.RunAsGroup == nil || *securityContext.RunAsGroup != 1001 {
		t.Fatalf("expected runAsGroup 1001, got %#v", securityContext.RunAsGroup)
	}
	if securityContext.RunAsUser == nil || *securityContext.RunAsUser != 1001 {
		t.Fatalf("expected runAsUser 1001, got %#v", securityContext.RunAsUser)
	}
	if securityContext.RunAsNonRoot == nil || !*securityContext.RunAsNonRoot {
		t.Fatalf("expected runAsNonRoot true, got %#v", securityContext.RunAsNonRoot)
	}
	if securityContext.AllowPrivilegeEscalation == nil || *securityContext.AllowPrivilegeEscalation {
		t.Fatalf("expected privilege escalation disabled, got %#v", securityContext.AllowPrivilegeEscalation)
	}
	if securityContext.SeccompProfile == nil || securityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("expected RuntimeDefault seccomp profile, got %#v", securityContext.SeccompProfile)
	}
	if securityContext.Capabilities == nil || len(securityContext.Capabilities.Drop) != 1 || securityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("expected all capabilities to be dropped, got %#v", securityContext.Capabilities)
	}
}

func TestEnsureTunnelUsesCustomPodResources(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		Resources: &ResourceConfig{
			Requests: ResourceValues{CPU: "25m", Memory: "48Mi"},
			Limits:   ResourceValues{CPU: "300m", Memory: "192Mi"},
		},
	}); err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if got := container.Resources.Requests[corev1.ResourceCPU]; got.String() != "25m" {
		t.Fatalf("expected custom cpu request, got %s", got.String())
	}
	if got := container.Resources.Requests[corev1.ResourceMemory]; got.String() != "48Mi" {
		t.Fatalf("expected custom memory request, got %s", got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceCPU]; got.String() != "300m" {
		t.Fatalf("expected custom cpu limit, got %s", got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.String() != "192Mi" {
		t.Fatalf("expected custom memory limit, got %s", got.String())
	}
}

func TestEnsureTunnelInjectsTargetURLAndRollsPodsOnTargetChange(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	_, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "raw-secret", "https", "3000", TunnelOptions{
		TargetURL: "http://10.0.0.12:8080",
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	args := strings.Join(deployment.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(args, "--target-url http://10.0.0.12:8080") {
		t.Fatalf("expected target URL arg, got %#v", deployment.Spec.Template.Spec.Containers[0].Args)
	}
	firstDigest := deployment.Spec.Template.Annotations[serverConfigDigestKey]
	if firstDigest == "" {
		t.Fatalf("expected server config digest annotation")
	}

	_, err = client.EnsureTunnelWithOptions(context.Background(), "abc123", "raw-secret", "https", "3000", TunnelOptions{
		TargetURL: "http://10.0.0.13:8080",
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error after target change: %v", err)
	}
	updated, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated deployment: %v", err)
	}
	updatedArgs := strings.Join(updated.Spec.Template.Spec.Containers[0].Args, " ")
	if !strings.Contains(updatedArgs, "--target-url http://10.0.0.13:8080") {
		t.Fatalf("expected updated target URL arg, got %#v", updated.Spec.Template.Spec.Containers[0].Args)
	}
	if got := updated.Spec.Template.Annotations[serverConfigDigestKey]; got == "" || got == firstDigest {
		t.Fatalf("expected server config digest annotation to change after target change, got %q", got)
	}
}

func TestEnsureTunnelInjectsBasicAuthViaSecret(t *testing.T) {
	name := "sealtun-abc123"
	firstHash, err := publicauth.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := publicauth.HashPassword("new-secret")
	if err != nil {
		t.Fatal(err)
	}
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	_, err = client.EnsureTunnelWithOptions(context.Background(), "abc123", "raw-secret", "https", "3000", TunnelOptions{
		BasicAuth: &BasicAuthOptions{
			Username:     "admin",
			PasswordHash: firstHash,
		},
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}

	authSecret, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected auth secret to be created: %v", err)
	}
	if got := string(authSecret.Data[basicAuthUserKey]); got != "admin" {
		t.Fatalf("unexpected basic auth username: %q", got)
	}
	if got := string(authSecret.Data[basicAuthPasswordKey]); got != firstHash {
		t.Fatalf("unexpected basic auth password hash: %q", got)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	args := strings.Join(container.Args, " ")
	if !strings.Contains(args, "--basic-auth-user-env SEALTUN_BASIC_AUTH_USER") {
		t.Fatalf("expected basic auth username env arg, got %#v", container.Args)
	}
	if !strings.Contains(args, "--basic-auth-password-hash-env SEALTUN_BASIC_AUTH_PASSWORD_HASH") {
		t.Fatalf("expected basic auth password hash env arg, got %#v", container.Args)
	}
	if strings.Contains(args, firstHash) {
		t.Fatalf("deployment args must not contain basic auth password hash: %#v", container.Args)
	}
	if len(container.Env) != 3 {
		t.Fatalf("expected tunnel secret and basic auth env vars, got %#v", container.Env)
	}
	firstDigest := deployment.Spec.Template.Annotations[serverConfigDigestKey]
	if firstDigest == "" {
		t.Fatalf("expected server config digest annotation to trigger pod rollout")
	}
	if strings.Contains(firstDigest, firstHash) {
		t.Fatalf("server config digest annotation must not expose password hash: %q", firstDigest)
	}

	_, err = client.EnsureTunnelWithOptions(context.Background(), "abc123", "raw-secret", "https", "3000", TunnelOptions{
		BasicAuth: &BasicAuthOptions{
			Username:     "admin",
			PasswordHash: secondHash,
		},
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error after password change: %v", err)
	}
	updated, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated deployment: %v", err)
	}
	if got := updated.Spec.Template.Annotations[serverConfigDigestKey]; got == "" || got == firstDigest {
		t.Fatalf("expected server config digest annotation to change after password change, got %q", got)
	}
}

func TestEnsureTunnelInjectsAccessPolicyViaSecret(t *testing.T) {
	name := "sealtun-abc123"
	hash, err := accesspolicy.HashToken("access-token")
	if err != nil {
		t.Fatal(err)
	}
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	_, err = client.EnsureTunnelWithOptions(context.Background(), "abc123", "raw-secret", "https", "3000", TunnelOptions{
		AccessPolicy: &accesspolicy.Policy{
			BearerTokenHashes: []string{hash},
			IPAllowlist:       []string{"10.0.0.0/8"},
			IPDenylist:        []string{"10.0.0.9"},
			RateLimit:         "60/m",
			Audit:             &accesspolicy.AuditConfig{Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}

	authSecret, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected auth secret to be created: %v", err)
	}
	policyJSON := string(authSecret.Data[accessPolicyKey])
	if !strings.Contains(policyJSON, hash) || !strings.Contains(policyJSON, "10.0.0.0/8") || !strings.Contains(policyJSON, `"rateLimit":"60/m"`) || !strings.Contains(policyJSON, `"audit":{"enabled":true}`) {
		t.Fatalf("expected access policy JSON in secret, got %s", policyJSON)
	}
	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	args := strings.Join(container.Args, " ")
	if !strings.Contains(args, "--access-policy-env SEALTUN_ACCESS_POLICY") {
		t.Fatalf("expected access policy env arg, got %#v", container.Args)
	}
	if strings.Contains(args, hash) || strings.Contains(deployment.Spec.Template.Annotations[serverConfigDigestKey], hash) {
		t.Fatal("deployment args and annotations must not expose access policy token hashes")
	}
}

func TestEnsureTunnelRejectsInvalidAccessPolicy(t *testing.T) {
	client := &Client{
		clientset: fake.NewSimpleClientset(),
		namespace: "default",
		domain:    "example.com",
	}

	_, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		AccessPolicy: &accesspolicy.Policy{IPAllowlist: []string{"not-an-ip"}},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid access policy") {
		t.Fatalf("expected invalid access policy error, got %v", err)
	}
}

func TestImageTagForVersion(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{version: "dev", want: "latest"},
		{version: "", want: "latest"},
		{version: "f596979", want: ""},
		{version: "f596979a", want: ""},
		{version: "v0.0.9", want: "0.0.9"},
		{version: "0.0.9", want: "0.0.9"},
		{version: "v0.0.9-rc.1", want: "0.0.9-rc.1"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := imageTagForVersion(tt.version); got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestServerImageForVersionCanBeOverridden(t *testing.T) {
	t.Setenv("SEALTUN_SERVER_IMAGE", "example.com/sealtun:test")

	got, err := serverImageForVersion("v1.2.3")
	if err != nil {
		t.Fatalf("server image override failed: %v", err)
	}
	if got != "example.com/sealtun:test" {
		t.Fatalf("expected overridden image, got %s", got)
	}
}

func TestServerImageForVersionRejectsUnknownVersions(t *testing.T) {
	t.Setenv("SEALTUN_SERVER_IMAGE", "")

	if _, err := serverImageForVersion("f596979"); err == nil {
		t.Fatal("expected unknown version to require an explicit server image")
	}
}

func TestEnsureTunnelUpdatesExistingManagedAuthSecret(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      authSecretName(name),
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}, Data: map[string][]byte{tunnelAuthSecretKey: []byte("old-secret")}})
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	if _, err := client.EnsureTunnel(context.Background(), "abc123", "new-secret", "https", "3000"); err != nil {
		t.Fatalf("EnsureTunnel returned error: %v", err)
	}
	authSecret, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get auth secret: %v", err)
	}
	if got := string(authSecret.Data[tunnelAuthSecretKey]); got != "new-secret" {
		t.Fatalf("expected auth secret to be updated, got %q", got)
	}
}

func TestEnsureTunnelWithCustomDomainKeepsSealosHostAndCreatesCertResources(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	hosts, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "dev.example.com",
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	if hosts.PublicHost != "dev.example.com" {
		t.Fatalf("expected public host to be custom domain, got %s", hosts.PublicHost)
	}
	if hosts.SealosHost != "sealtun-abc123-default.sealosgzg.site" {
		t.Fatalf("unexpected sealos host: %s", hosts.SealosHost)
	}

	ingress, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ingress: %v", err)
	}
	if len(ingress.Spec.Rules) != 2 {
		t.Fatalf("expected official and custom ingress rules, got %d", len(ingress.Spec.Rules))
	}
	if ingress.Spec.Rules[0].Host != hosts.SealosHost || ingress.Spec.Rules[1].Host != "dev.example.com" {
		t.Fatalf("unexpected ingress hosts: %#v", ingress.Spec.Rules)
	}
	if len(ingress.Spec.TLS) != 2 {
		t.Fatalf("expected official and custom TLS entries, got %d", len(ingress.Spec.TLS))
	}
	if ingress.Spec.TLS[0].SecretName != "wildcard-cert" || ingress.Spec.TLS[1].SecretName != "sealtun-abc123" {
		t.Fatalf("unexpected TLS entries: %#v", ingress.Spec.TLS)
	}
	if got := ingress.Labels["cloud.sealos.io/app-deploy-manager-domain"]; got != "sealtun-abc123-default" {
		t.Fatalf("expected official domain label, got %s", got)
	}

	issuer, err := dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected issuer to be created: %v", err)
	}
	if issuer.GetKind() != "Issuer" {
		t.Fatalf("unexpected issuer kind: %s", issuer.GetKind())
	}
	cert, err := dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected certificate to be created: %v", err)
	}
	dnsNames, ok, err := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
	if err != nil || !ok || len(dnsNames) != 1 || dnsNames[0] != "dev.example.com" {
		t.Fatalf("unexpected certificate dnsNames ok=%v err=%v value=%#v", ok, err, dnsNames)
	}
}

func TestEnsureTunnelWithOptionsUsesProvidedSealosHost(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "new-region.example.com",
	}

	hosts, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "dev.example.com",
		SealosHost:   "preserved-old-host.sealosold.site",
	})
	if err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	if hosts.SealosHost != "preserved-old-host.sealosold.site" {
		t.Fatalf("expected provided Sealos host to be preserved, got %s", hosts.SealosHost)
	}

	ingress, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ingress: %v", err)
	}
	if len(ingress.Spec.Rules) != 2 || ingress.Spec.Rules[0].Host != "preserved-old-host.sealosold.site" || ingress.Spec.Rules[1].Host != "dev.example.com" {
		t.Fatalf("unexpected ingress hosts: %#v", ingress.Spec.Rules)
	}
}

func TestEnsureTunnelWithCustomDomainCleansIssuerOnCertificateFailure(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("create", "certificates", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("certificate create failed")
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "dev.example.com",
	}); err == nil {
		t.Fatal("expected EnsureTunnelWithOptions to fail")
	}
	if _, err := dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected issuer to be cleaned up, got %v", err)
	}
	ingresses, err := clientset.NetworkingV1().Ingresses("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list ingresses: %v", err)
	}
	if len(ingresses.Items) != 0 {
		t.Fatalf("expected ingress rollback, got %d", len(ingresses.Items))
	}
}

func TestEnsureTunnelWithCustomDomainRestoresCertificateWhenIngressUpdateFails(t *testing.T) {
	name := "sealtun-abc123"
	oldCert := customDomainCertificate(name, "old.example.com")
	oldCert.SetNamespace("default")
	oldIssuer := (&Client{namespace: "default"}).customDomainIssuer(name)
	oldIssuer.SetNamespace("default")
	clientset := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      authSecretName(name),
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}, Data: map[string][]byte{tunnelAuthSecretKey: []byte("old")}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)}, Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: managedLabels(name)},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)}, Spec: corev1.ServiceSpec{
			ClusterIP: "10.0.0.1",
		}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)}},
	)
	clientset.PrependReactor("update", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("ingress update failed")
	})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), oldIssuer, oldCert)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "new", "https", "3000", TunnelOptions{
		CustomDomain: "new.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "ingress update failed") {
		t.Fatalf("expected ingress update failure, got %v", err)
	}
	cert, err := dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get restored certificate: %v", err)
	}
	dnsNames, ok, err := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
	if err != nil || !ok || len(dnsNames) != 1 || dnsNames[0] != "old.example.com" {
		t.Fatalf("expected old certificate dnsNames to be restored, ok=%v err=%v value=%#v", ok, err, dnsNames)
	}
}

func TestEnsureTunnelRejectsCustomDomainEqualToSealosHost(t *testing.T) {
	client := &Client{
		clientset:     fake.NewSimpleClientset(),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "sealtun-abc123-default.sealosgzg.site",
	})
	if err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("expected custom domain target validation error, got %v", err)
	}
}

func TestEnsureTunnelRejectsCustomDomainUnderSealosManagedDomain(t *testing.T) {
	client := &Client{
		clientset:     fake.NewSimpleClientset(),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	tests := []string{
		"attacker.sealosgzg.site",
		"sealosgzg.site",
	}
	for _, customDomain := range tests {
		_, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
			CustomDomain: customDomain,
		})
		if err == nil || !strings.Contains(err.Error(), "must not be under the Sealos-managed domain") {
			t.Fatalf("expected managed domain rejection for %s, got %v", customDomain, err)
		}
	}
}

func TestEnsureTunnelRejectsCustomDomainUnderReservedSealosDomain(t *testing.T) {
	client := &Client{
		clientset:     fake.NewSimpleClientset(),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
		domain:        "example.com",
	}

	_, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "victim.sealoshzh.site",
	})
	if err == nil || !strings.Contains(err.Error(), "must not be under reserved Sealos domain") {
		t.Fatalf("expected reserved domain rejection, got %v", err)
	}
}

func TestConfigureCustomDomainRequiresCoreTunnelResources(t *testing.T) {
	client := &Client{
		clientset:     fake.NewSimpleClientset(),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.ConfigureCustomDomain(context.Background(), "abc123", "", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "remote deployment sealtun-abc123 is missing") {
		t.Fatalf("expected missing deployment error, got %v", err)
	}
}

func TestConfigureCustomDomainRejectsUnmanagedCoreResources(t *testing.T) {
	name := "sealtun-abc123"
	client := &Client{
		clientset: fake.NewSimpleClientset(
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.ConfigureCustomDomain(context.Background(), "abc123", "", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "remote deployment sealtun-abc123 is not managed by Sealtun") {
		t.Fatalf("expected unmanaged deployment rejection, got %v", err)
	}
}

func TestConfigureCustomDomainRejectsUnmanagedTLSSecret(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.ConfigureCustomDomain(context.Background(), "abc123", "sealtun-abc123-default.sealosgzg.site", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "secret sealtun-abc123 already exists but is not managed by Sealtun") {
		t.Fatalf("expected unmanaged TLS secret rejection, got %v", err)
	}
}

func TestConfigureCustomDomainDoesNotTrustManagedCertificateWithDifferentSecret(t *testing.T) {
	name := "sealtun-abc123"
	cert := customDomainCertificate(name, "old.example.com")
	cert.SetNamespace("default")
	if err := unstructured.SetNestedField(cert.Object, "other-secret", "spec", "secretName"); err != nil {
		t.Fatalf("set certificate secretName: %v", err)
	}
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cert),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.ConfigureCustomDomain(context.Background(), "abc123", "sealtun-abc123-default.sealosgzg.site", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "secret sealtun-abc123 already exists but is not managed by Sealtun") {
		t.Fatalf("expected unmanaged TLS secret rejection, got %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged TLS secret should remain: %v", err)
	}
}

func TestConfigureCustomDomainDoesNotTrustUnmarkedSecretEvenWithManagedCertificate(t *testing.T) {
	name := "sealtun-abc123"
	cert := customDomainCertificate(name, "old.example.com")
	cert.SetNamespace("default")
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{managedLabelKey: name}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cert),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	_, err := client.ConfigureCustomDomain(context.Background(), "abc123", "sealtun-abc123-default.sealosgzg.site", "dev.example.com")
	if err == nil || !strings.Contains(err.Error(), "secret sealtun-abc123 already exists but is not managed by Sealtun") {
		t.Fatalf("expected unmanaged TLS secret rejection, got %v", err)
	}
}

func TestConfigureCustomDomainRestoresCertificateWhenIngressUpdateFails(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}
	if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "old.example.com",
	}); err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	clientset.PrependReactor("update", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("ingress update failed")
	})

	if _, err := client.ConfigureCustomDomain(context.Background(), "abc123", "", "new.example.com"); err == nil {
		t.Fatal("expected ConfigureCustomDomain to fail")
	}
	cert, err := dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get restored certificate: %v", err)
	}
	dnsNames, ok, err := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
	if err != nil || !ok || len(dnsNames) != 1 || dnsNames[0] != "old.example.com" {
		t.Fatalf("expected restored old dnsName, ok=%v err=%v value=%#v", ok, err, dnsNames)
	}
}

func TestClearCustomDomainRestoresOfficialIngressAndRemovesCertResources(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "sealtun-abc123",
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: "sealtun-abc123"},
	}})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}
	if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "dev.example.com",
	}); err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}

	hosts, err := client.ClearCustomDomain(context.Background(), "abc123", "")
	if err != nil {
		t.Fatalf("ClearCustomDomain returned error: %v", err)
	}
	if hosts.PublicHost != "sealtun-abc123-default.sealosgzg.site" {
		t.Fatalf("unexpected public host after clear: %s", hosts.PublicHost)
	}
	ingress, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ingress: %v", err)
	}
	if len(ingress.Spec.Rules) != 1 || ingress.Spec.Rules[0].Host != hosts.SealosHost {
		t.Fatalf("expected only official host after clear, got %#v", ingress.Spec.Rules)
	}
	if _, err := dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected certificate to be deleted, got %v", err)
	}
	if _, err := dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected issuer to be deleted, got %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected custom TLS secret to be deleted, got %v", err)
	}
}

func TestClearCustomDomainKeepsOfficialIngressWhenCertificateCleanupFails(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "sealtun-abc123",
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: "sealtun-abc123"},
	}})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}
	if _, err := client.EnsureTunnelWithOptions(context.Background(), "abc123", "secret", "https", "3000", TunnelOptions{
		CustomDomain: "dev.example.com",
	}); err != nil {
		t.Fatalf("EnsureTunnelWithOptions returned error: %v", err)
	}
	dynamicClient.PrependReactor("delete", "certificates", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("certificate delete forbidden")
	})

	hosts, err := client.ClearCustomDomain(context.Background(), "abc123", "")
	if err == nil || !strings.Contains(err.Error(), "certificate cleanup incomplete") {
		t.Fatalf("expected cleanup warning error, got %v", err)
	}
	if hosts.PublicHost != "sealtun-abc123-default.sealosgzg.site" {
		t.Fatalf("expected official host to be returned, got %#v", hosts)
	}
	ingress, getErr := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), "sealtun-abc123", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("get ingress: %v", getErr)
	}
	if len(ingress.Spec.Rules) != 1 || ingress.Spec.Rules[0].Host != hosts.SealosHost {
		t.Fatalf("expected ingress to stay on official host after cleanup failure, got %#v", ingress.Spec.Rules)
	}
}

func TestCleanupTunnelAlwaysRemovesCustomDomainResources(t *testing.T) {
	name := "sealtun-abc123-tcp"
	clientset := fake.NewSimpleClientset(
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      tcpServiceName(name),
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      authSecretName(name),
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
	)
	issuer := (&Client{namespace: "default"}).customDomainIssuer(name)
	issuer.SetNamespace("default")
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123-tcp"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected service to be deleted, got %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), tcpServiceName(name), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected tcp service to be deleted, got %v", err)
	}
	if _, err := client.dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected certificate to be deleted, got %v", err)
	}
	if _, err := client.dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected issuer to be deleted, got %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected secret to be deleted, got %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected auth secret to be deleted, got %v", err)
	}
}

func TestCleanupCreatedRemovesServicesWhenTunnelNameEndsWithTCP(t *testing.T) {
	name := "sealtun-abc123-tcp"
	clientset := fake.NewSimpleClientset(
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      tcpServiceName(name),
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
	)
	client := &Client{
		clientset: clientset,
		namespace: "default",
	}

	err := client.cleanupCreated(context.Background(), []createdResource{
		{kind: resourceService, name: name},
		{kind: resourceTCPService, name: tcpServiceName(name)},
	})
	if err != nil {
		t.Fatalf("cleanupCreated returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected service to be deleted, got %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), tcpServiceName(name), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected tcp service to be deleted, got %v", err)
	}
}

func TestPauseTunnelScalesManagedDeploymentAndKeepsEntryResources(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(1)
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: authSecretName(name), Namespace: "default", Labels: managedLabels(name)}},
	)
	issuer := (&Client{namespace: "default"}).customDomainIssuer(name)
	issuer.SetNamespace("default")
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.PauseTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("PauseTunnel returned error: %v", err)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment should remain: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 {
		t.Fatalf("expected deployment replicas to be 0, got %v", deployment.Spec.Replicas)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("service should remain: %v", err)
	}
	if _, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("ingress should remain: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("tls secret should remain: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{}); err != nil {
		t.Fatalf("auth secret should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("certificate should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("issuer should remain: %v", err)
	}
}

func TestResumeTunnelScalesManagedDeploymentToOne(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(0)
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}

	if err := client.ResumeTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("ResumeTunnel returned error: %v", err)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment should remain: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 {
		t.Fatalf("expected deployment replicas to be 1, got %v", deployment.Spec.Replicas)
	}
}

func TestResumeTunnelSkipsUpdateWhenAlreadyRunning(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(1)
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}

	if err := client.ResumeTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("ResumeTunnel returned error: %v", err)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "update" {
			t.Fatalf("already-running tunnel issued update for %s", action.GetResource().Resource)
		}
	}
}

func TestUpdateTunnelResourcesPreservesReplicas(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(0)
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: name,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
				},
			}}}},
		},
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}

	if err := client.UpdateTunnelResources(context.Background(), "abc123", &ResourceConfig{
		Requests: ResourceValues{CPU: "50m", Memory: "64Mi"},
		Limits:   ResourceValues{CPU: "400m", Memory: "256Mi"},
	}); err != nil {
		t.Fatalf("UpdateTunnelResources returned error: %v", err)
	}

	deployment, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("deployment should remain: %v", err)
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 {
		t.Fatalf("expected replicas to stay 0, got %v", deployment.Spec.Replicas)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if got := container.Resources.Requests[corev1.ResourceCPU]; got.String() != "50m" {
		t.Fatalf("expected updated cpu request, got %s", got.String())
	}
	if got := container.Resources.Limits[corev1.ResourceMemory]; got.String() != "256Mi" {
		t.Fatalf("expected updated memory limit, got %s", got.String())
	}
}

func TestUpdateTunnelResourcesSkipsUpdateWhenUnchanged(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(1)
	requirements, err := resourceRequirementsForConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:      name,
				Resources: requirements,
			}}}},
		},
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}

	if err := client.UpdateTunnelResources(context.Background(), "abc123", nil); err != nil {
		t.Fatalf("UpdateTunnelResources returned error: %v", err)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "update" {
			t.Fatalf("unchanged resources issued update for %s", action.GetResource().Resource)
		}
	}
}

func TestPauseTunnelRejectsUnmanagedDeployment(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(1)
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}

	err := client.PauseTunnel(context.Background(), "abc123")
	if err == nil || !strings.Contains(err.Error(), "not managed by Sealtun") {
		t.Fatalf("expected unmanaged deployment error, got %v", err)
	}
}

func TestCleanupTunnelSkipsUnmanagedSameNameResources(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: authSecretName(name), Namespace: "default"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
	)
	issuer := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Issuer",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "default",
		},
	}}
	certificate := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "default",
		},
		"spec": map[string]interface{}{
			"secretName": name,
		},
	}}
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged TLS secret should remain: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), authSecretName(name), metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged auth secret should remain: %v", err)
	}
	if _, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged deployment should remain: %v", err)
	}
	if _, err := clientset.CoreV1().Services("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged service should remain: %v", err)
	}
	if _, err := clientset.NetworkingV1().Ingresses("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged ingress should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged certificate should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(issuerGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged issuer should remain: %v", err)
	}
}

func TestCleanupTunnelKeepsUnprovenACMEAccountSecret(t *testing.T) {
	name := "sealtun-abc123"
	keyName := "letsencrypt-prod-" + name
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      keyName,
		Namespace: "default",
	}})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), keyName, metav1.GetOptions{}); err != nil {
		t.Fatalf("unproven ACME account secret should remain: %v", err)
	}
}

func TestCleanupTunnelDeletesACMEAccountSecretOwnedByManagedIssuer(t *testing.T) {
	name := "sealtun-abc123"
	keyName := "letsencrypt-prod-" + name
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      keyName,
		Namespace: "default",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "cert-manager.io/v1",
			Kind:       "Issuer",
			Name:       name,
		}},
	}})
	issuer := (&Client{namespace: "default"}).customDomainIssuer(name)
	issuer.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer),
		namespace:     "default",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), keyName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("owned ACME account secret should be deleted, got %v", err)
	}
}

func TestCleanupTunnelKeepsUnmanagedSecretWhenCertificateUsesDifferentSecret(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
	}})
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	if err := unstructured.SetNestedField(certificate.Object, "other-secret", "spec", "secretName"); err != nil {
		t.Fatalf("set certificate secretName: %v", err)
	}
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmanaged TLS secret should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected managed certificate to be deleted, got %v", err)
	}
}

func TestCleanupTunnelKeepsUnmarkedSecretEvenWhenManagedCertificateReferencesIt(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
	}})
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unmarked TLS secret should remain: %v", err)
	}
	if _, err := client.dynamicClient.Resource(certificateGVR).Namespace("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected managed certificate to be deleted, got %v", err)
	}
}

func TestCleanupTunnelDeletesCertManagerAnnotatedSecret(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:        name,
		Namespace:   "default",
		Annotations: map[string]string{"cert-manager.io/certificate-name": name},
	}})
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected cert-manager annotated secret to be deleted, got %v", err)
	}
}

func TestCleanupTunnelContinuesAfterCustomResourceDeleteFailure(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      authSecretName(name),
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}})
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), certificate)
	dynamicClient.PrependReactor("delete", "certificates", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("certificate delete forbidden")
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	err := client.CleanupTunnel(context.Background(), "abc123")
	if err == nil || !strings.Contains(err.Error(), "certificate delete forbidden") {
		t.Fatalf("expected certificate delete error, got %v", err)
	}
	if _, err := clientset.CoreV1().Secrets("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected secret to be deleted despite certificate error, got %v", err)
	}
}

func TestCleanupManagedRemovesCustomDomainResources(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      authSecretName(name),
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}})
	issuer := (&Client{namespace: "default"}).customDomainIssuer(name)
	issuer.SetNamespace("default")
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, certificate),
		namespace:     "default",
		domain:        "example.com",
	}

	summary, err := client.CleanupManaged(context.Background(), []string{"abc123"})
	if err != nil {
		t.Fatalf("CleanupManaged returned error: %v", err)
	}
	if summary.Certificates != 1 || summary.Issuers != 1 || summary.Secrets != 2 {
		t.Fatalf("unexpected custom resource cleanup summary: %#v", summary)
	}
}

func TestCleanupManagedContinuesAfterCustomResourceDeleteFailure(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}})
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), certificate)
	dynamicClient.PrependReactor("delete", "certificates", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("certificate delete forbidden")
	})
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     "default",
		domain:        "example.com",
	}

	summary, err := client.CleanupManaged(context.Background(), []string{"abc123"})
	if err == nil || !strings.Contains(err.Error(), "certificate delete forbidden") {
		t.Fatalf("expected certificate delete error, got %v", err)
	}
	if summary == nil || summary.Deployments != 1 {
		t.Fatalf("expected deployment cleanup to continue, summary=%#v", summary)
	}
	if _, err := clientset.AppsV1().Deployments("default").Get(context.Background(), name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected deployment to be deleted despite certificate error, got %v", err)
	}
}

func TestWaitForReadyRequiresObservedUpdatedReplicas(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			Generation: 2,
		},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           2,
			ReadyReplicas:      1,
			AvailableReplicas:  1,
			UpdatedReplicas:    0,
		},
	}
	clientset := fake.NewSimpleClientset(deployment)
	client := &Client{clientset: clientset, namespace: "default"}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := client.WaitForReady(ctx, "abc123"); err == nil {
		t.Fatal("expected old ready replicas to be ignored before rollout is observed and updated")
	}

	ready := deployment.DeepCopy()
	ready.Status.ObservedGeneration = ready.Generation
	ready.Status.Replicas = 1
	ready.Status.UpdatedReplicas = 1
	ready.Status.ReadyReplicas = 1
	ready.Status.AvailableReplicas = 1
	if _, err := clientset.AppsV1().Deployments("default").UpdateStatus(context.Background(), ready, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readyCancel()
	if err := client.WaitForReady(readyCtx, "abc123"); err != nil {
		t.Fatalf("expected updated rollout to be ready: %v", err)
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
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
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

func TestDiagnoseTunnelIgnoresPodsWithoutManagedLabel(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1},
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
			ObjectMeta: metav1.ObjectMeta{Name: "unrelated-pod", Namespace: "default", Labels: map[string]string{"app": name}},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}},
		},
	)
	client := &Client{clientset: clientset, namespace: "default", domain: "example.com"}

	diag, err := client.DiagnoseTunnel(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("DiagnoseTunnel returned error: %v", err)
	}
	if len(diag.Pods) != 1 || diag.Pods[0].Name != name+"-pod" {
		t.Fatalf("expected only the managed tunnel pod, got %#v", diag.Pods)
	}
	if len(diag.Warnings) != 0 {
		t.Fatalf("expected no warnings from ignored unrelated pod, got %#v", diag.Warnings)
	}
}

func TestDiagnoseTunnelReportsCustomDomainCertificate(t *testing.T) {
	name := "sealtun-abc123"
	cert := customDomainCertificate(name, "dev.example.com")
	cert.SetNamespace("default")
	if err := unstructured.SetNestedSlice(cert.Object, []interface{}{
		map[string]interface{}{
			"type":    "Ready",
			"status":  "True",
			"reason":  "Issued",
			"message": "Certificate issued",
		},
	}, "status", "conditions"); err != nil {
		t.Fatalf("set certificate condition: %v", err)
	}
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
			Spec: netv1.IngressSpec{
				Rules: []netv1.IngressRule{{
					Host:             "dev.example.com",
					IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{Paths: []netv1.HTTPIngressPath{{Path: "/"}}}},
				}},
				TLS: []netv1.IngressTLS{{Hosts: []string{"dev.example.com"}, SecretName: name}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}},
		},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cert),
		namespace:     "default",
		domain:        "example.com",
	}

	diag, err := client.DiagnoseTunnelWithOptions(context.Background(), "abc123", TunnelOptions{CustomDomain: "dev.example.com"})
	if err != nil {
		t.Fatalf("DiagnoseTunnelWithOptions returned error: %v", err)
	}
	if diag.Certificate == nil || !diag.Certificate.Exists || !diag.Certificate.Ready {
		t.Fatalf("unexpected certificate diagnostics: %#v", diag.Certificate)
	}
	if len(diag.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %#v", diag.Warnings)
	}
}

func TestTunnelResourcesSanitizesSecretsAndReportsCostHints(t *testing.T) {
	name := "sealtun-abc123"
	replicas := int32(1)
	cert := customDomainCertificate(name, "dev.example.com")
	cert.SetNamespace("default")
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Ports: []corev1.ServicePort{{Name: "http", Port: 80}}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name + "-tcp", Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Ports: []corev1.ServicePort{{Name: "tcp", Port: 2222, NodePort: 32222}}}},
		&netv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Spec: netv1.IngressSpec{Rules: []netv1.IngressRule{{Host: "dev.example.com"}}}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Type: corev1.SecretTypeTLS, Data: map[string][]byte{"tls.key": []byte("secret")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: authSecretName(name), Namespace: "default", Labels: managedLabels(name), CreationTimestamp: metav1.Now()}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"token": []byte("secret")}},
	)
	issuer := (&Client{namespace: "default"}).customDomainIssuer(name)
	issuer.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, cert),
		namespace:     "default",
	}

	payload, err := client.TunnelResources(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if payload.Namespace != "default" || payload.TunnelID != "abc123" {
		t.Fatalf("unexpected payload identity: %#v", payload)
	}
	kinds := map[string]TunnelResource{}
	for _, resource := range payload.Resources {
		kinds[resource.Kind+"-"+resource.Name] = resource
		if strings.Contains(fmt.Sprintf("%#v", resource), "tls.key") || strings.Contains(fmt.Sprintf("%#v", resource), "token") {
			t.Fatalf("secret data leaked through resources payload: %#v", resource)
		}
	}
	if got := kinds["Deployment-"+name]; !strings.Contains(strings.Join(got.CostHints, " "), "desired replicas: 1") {
		t.Fatalf("expected deployment replica hint, got %#v", got)
	}
	if got := kinds["TCP NodePort Service-"+name+"-tcp"]; !strings.Contains(strings.Join(got.CostHints, " "), "32222") {
		t.Fatalf("expected nodePort hint, got %#v", got)
	}
	if _, ok := kinds["Secret-"+name]; !ok {
		t.Fatalf("expected TLS secret resource in %#v", kinds)
	}
	if _, ok := kinds["Secret-"+authSecretName(name)]; !ok {
		t.Fatalf("expected auth secret resource in %#v", kinds)
	}
}

func TestTunnelResourcesReportsMissingResources(t *testing.T) {
	client := &Client{
		clientset:     fake.NewSimpleClientset(),
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		namespace:     "default",
	}
	payload, err := client.TunnelResources(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Resources) == 0 || len(payload.Warnings) == 0 {
		t.Fatalf("expected missing resources and warnings, got %#v", payload)
	}
	if payload.Resources[0].Status != "missing" {
		t.Fatalf("expected missing status, got %#v", payload.Resources[0])
	}
	for _, warning := range payload.Warnings {
		if strings.Contains(warning, "Certificate") || strings.Contains(warning, "Issuer") || strings.Contains(warning, "Secret") {
			t.Fatalf("optional certificate/issuer/secret resources should not create warnings: %#v", payload.Warnings)
		}
	}
}

func TestDiagnoseTunnelWarnsWhenCustomDomainIngressHostIsMissing(t *testing.T) {
	name := "sealtun-abc123"
	cert := customDomainCertificate(name, "dev.example.com")
	cert.SetNamespace("default")
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
			Spec: netv1.IngressSpec{
				Rules: []netv1.IngressRule{{
					Host:             "sealtun-abc123-default.sealosgzg.site",
					IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{Paths: []netv1.HTTPIngressPath{{Path: "/"}}}},
				}},
				TLS: []netv1.IngressTLS{{Hosts: []string{"sealtun-abc123-default.sealosgzg.site"}, SecretName: "wildcard-cert"}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}},
		},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cert),
		namespace:     "default",
		domain:        "example.com",
	}

	diag, err := client.DiagnoseTunnelWithOptions(context.Background(), "abc123", TunnelOptions{CustomDomain: "dev.example.com"})
	if err != nil {
		t.Fatalf("DiagnoseTunnelWithOptions returned error: %v", err)
	}
	if !warningsContain(diag.Warnings, "remote ingress is missing custom domain host dev.example.com") {
		t.Fatalf("expected missing custom host warning, got %#v", diag.Warnings)
	}
	if !warningsContain(diag.Warnings, "remote ingress TLS is missing custom domain host dev.example.com") {
		t.Fatalf("expected missing custom TLS warning, got %#v", diag.Warnings)
	}
}

func TestDiagnoseTunnelWarnsWhenSealosControlHostIsMissing(t *testing.T) {
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
			Spec: netv1.IngressSpec{
				Rules: []netv1.IngressRule{{
					Host:             "wrong.example.com",
					IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{Paths: []netv1.HTTPIngressPath{{Path: "/"}}}},
				}},
				TLS: []netv1.IngressTLS{{Hosts: []string{"wrong.example.com"}, SecretName: "wildcard-cert"}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}},
		},
	)
	client := &Client{
		clientset: clientset,
		namespace: "default",
		domain:    "example.com",
	}

	diag, err := client.DiagnoseTunnelWithOptions(context.Background(), "abc123", TunnelOptions{
		SealosHost: "sealtun-abc123-default.sealosgzg.site",
	})
	if err != nil {
		t.Fatalf("DiagnoseTunnelWithOptions returned error: %v", err)
	}
	if !warningsContain(diag.Warnings, "remote ingress is missing Sealos CNAME host sealtun-abc123-default.sealosgzg.site") {
		t.Fatalf("expected missing Sealos host warning, got %#v", diag.Warnings)
	}
	if !warningsContain(diag.Warnings, "remote ingress TLS is missing Sealos CNAME host sealtun-abc123-default.sealosgzg.site") {
		t.Fatalf("expected missing Sealos TLS warning, got %#v", diag.Warnings)
	}
}

func TestDiagnoseTunnelWarnsWhenCertificateDNSNameDoesNotMatchCustomDomain(t *testing.T) {
	name := "sealtun-abc123"
	cert := customDomainCertificate(name, "old.example.com")
	cert.SetNamespace("default")
	if err := unstructured.SetNestedSlice(cert.Object, []interface{}{
		map[string]interface{}{"type": "Ready", "status": "True"},
	}, "status", "conditions"); err != nil {
		t.Fatalf("set certificate condition: %v", err)
	}
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
			Spec: netv1.IngressSpec{
				Rules: []netv1.IngressRule{{
					Host:             "dev.example.com",
					IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{Paths: []netv1.HTTPIngressPath{{Path: "/"}}}},
				}},
				TLS: []netv1.IngressTLS{{Hosts: []string{"dev.example.com"}, SecretName: name}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}}},
		},
	)
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cert),
		namespace:     "default",
		domain:        "example.com",
	}

	diag, err := client.DiagnoseTunnelWithOptions(context.Background(), "abc123", TunnelOptions{CustomDomain: "dev.example.com"})
	if err != nil {
		t.Fatalf("DiagnoseTunnelWithOptions returned error: %v", err)
	}
	if !warningsContain(diag.Warnings, "custom domain certificate does not include DNS name dev.example.com") {
		t.Fatalf("expected certificate DNS name warning, got %#v", diag.Warnings)
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
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: managedLabels(name)},
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

func TestDiagnoseTunnelTreatsPodListFailureAsWarning(t *testing.T) {
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
	)
	clientset.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("pods forbidden")
	})
	client := &Client{clientset: clientset, namespace: "default", domain: "example.com"}

	diag, err := client.DiagnoseTunnel(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("DiagnoseTunnel should not fail when pods are unavailable: %v", err)
	}
	if !diag.Deployment.Exists || !diag.Service.Exists || !diag.Ingress.Exists {
		t.Fatalf("expected resource diagnostics to be preserved: %#v", diag)
	}
	if !warningsContain(diag.Warnings, "remote pods unavailable: pods forbidden") {
		t.Fatalf("expected pods warning, got %#v", diag.Warnings)
	}
}

func TestFilterEventDiagnosticsMatchesExactObjectName(t *testing.T) {
	old := metav1.NewTime(time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC))
	recent := metav1.NewTime(time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC))
	events := []corev1.Event{
		{InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "sealtun-abc123"}, Reason: "Exact", LastTimestamp: old},
		{InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "sealtun-abc123-pod"}, Reason: "PodExact", LastTimestamp: recent, Count: 3},
		{InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "prefix-sealtun-abc123-suffix"}, Reason: "Substring"},
	}

	result := filterEventDiagnostics(events, []string{"sealtun-abc123", "sealtun-abc123-pod"}, 10)
	if len(result) != 2 {
		t.Fatalf("expected only exact event match, got %#v", result)
	}
	if result[0].Reason != "PodExact" || result[1].Reason != "Exact" {
		t.Fatalf("unexpected matched event: %#v", result[0])
	}
	if result[0].Count != 3 || result[0].LastTimestamp != "2026-05-18T10:00:00Z" {
		t.Fatalf("expected event count and last timestamp, got %#v", result[0])
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

func servicePortByName(service *corev1.Service, name string) *corev1.ServicePort {
	if service == nil {
		return nil
	}
	for i := range service.Spec.Ports {
		if service.Spec.Ports[i].Name == name {
			return &service.Spec.Ports[i]
		}
	}
	return nil
}

func containerPortByName(deployment *appsv1.Deployment, name string) *corev1.ContainerPort {
	if deployment == nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
		return nil
	}
	for i := range deployment.Spec.Template.Spec.Containers[0].Ports {
		if deployment.Spec.Template.Spec.Containers[0].Ports[i].Name == name {
			return &deployment.Spec.Template.Spec.Containers[0].Ports[i]
		}
	}
	return nil
}

func warningsContain(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}
