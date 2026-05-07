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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestDomainRejectsInvalidSealosDomainFromAuthData(t *testing.T) {
	_, err := newClientFromRawConfig(&rest.Config{Host: "https://kubernetes.example.com"}, rawConfigForTest(), &auth.AuthData{
		Region:       "https://hzh.sealos.run",
		SealosDomain: "bad/domain",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid Sealos ingress domain") {
		t.Fatalf("expected invalid Sealos domain error, got %v", err)
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
	if len(paths) != 3 {
		t.Fatalf("expected tunnel and app paths in one ingress, got %d", len(paths))
	}
	if paths[0].Path != "/_sealtun/ws" || paths[1].Path != "/_sealtun/healthz" || paths[2].Path != "/" {
		t.Fatalf("unexpected ingress paths: %#v", paths)
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
	oldIssuer := customDomainIssuer(name)
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
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name:      authSecretName(name),
			Namespace: "default",
			Labels:    map[string]string{managedLabelKey: name},
		}},
	)
	issuer := customDomainIssuer(name)
	issuer.SetNamespace("default")
	certificate := customDomainCertificate(name, "dev.example.com")
	certificate.SetNamespace("default")
	client := &Client{
		clientset:     clientset,
		dynamicClient: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), issuer, certificate),
		namespace:     "default",
		domain:        "sealosgzg.site",
	}

	if err := client.CleanupTunnel(context.Background(), "abc123"); err != nil {
		t.Fatalf("CleanupTunnel returned error: %v", err)
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

func TestCleanupTunnelContinuesAfterCustomResourceDeleteFailure(t *testing.T) {
	name := "sealtun-abc123"
	clientset := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: "default",
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
	}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      authSecretName(name),
		Namespace: "default",
		Labels:    map[string]string{managedLabelKey: name},
	}})
	issuer := customDomainIssuer(name)
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
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: map[string]string{"app": name}},
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
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: map[string]string{"app": name}},
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
			ObjectMeta: metav1.ObjectMeta{Name: name + "-pod", Namespace: "default", Labels: map[string]string{"app": name}},
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

func warningsContain(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}
