package k8s

import (
	"context"
	"strings"
	"testing"

	"github.com/labring/sealtun/pkg/mesh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureMeshGatewayCreatesManagedResources(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "ns-test",
		domain:    "sealos.example",
	}

	status, err := client.EnsureMeshGateway(context.Background(), MeshGatewaySpec{
		MeshName: "global",
		Token:    "secret-token",
		Routes: []mesh.GatewayRoute{
			{
				Name:             "api",
				Protocol:         mesh.ProtocolHTTP,
				ListenPort:       mesh.ImportPort("api"),
				TargetRegion:     "hzh",
				TargetNamespace:  "default",
				TargetService:    "api",
				TargetPort:       8080,
				RemoteGatewayURL: "https://mesh-hzh.example.com",
			},
		},
	})
	if err != nil {
		t.Fatalf("EnsureMeshGateway: %v", err)
	}
	if status.Host != "sealtun-mesh-global-ns-test.sealos.example" {
		t.Fatalf("unexpected gateway host: %s", status.Host)
	}
	deployment, err := clientset.AppsV1().Deployments("ns-test").Get(context.Background(), "sealtun-mesh-global", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Args[0] != "mesh" {
		t.Fatalf("gateway deployment does not run mesh command: %#v", deployment.Spec.Template.Spec.Containers[0].Args)
	}
	digest := deployment.Spec.Template.Annotations[meshConfigDigestKey]
	if digest == "" {
		t.Fatal("gateway deployment is missing mesh config digest annotation")
	}
	if strings.Contains(digest, "secret-token") {
		t.Fatal("mesh config digest annotation must not contain the raw token")
	}
	configMap, err := clientset.CoreV1().ConfigMaps("ns-test").Get(context.Background(), "sealtun-mesh-global", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	if strings.Contains(configMap.Data[meshRoutesKey], "secret-token") {
		t.Fatal("routes config must not contain gateway token")
	}
	secret, err := clientset.CoreV1().Secrets("ns-test").Get(context.Background(), "sealtun-mesh-global", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret.Data[meshTokenKey]) != "secret-token" {
		t.Fatalf("unexpected secret token")
	}
	if _, err := clientset.CoreV1().Services("ns-test").Get(context.Background(), "sealtun-mesh-global", metav1.GetOptions{}); err != nil {
		t.Fatalf("get gateway service: %v", err)
	}
	if _, err := clientset.NetworkingV1().Ingresses("ns-test").Get(context.Background(), "sealtun-mesh-global", metav1.GetOptions{}); err != nil {
		t.Fatalf("get gateway ingress: %v", err)
	}
}

func TestEnsureMeshImportCreatesClusterIPServiceToGateway(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "ns-test",
		domain:    "sealos.example",
	}

	dns, err := client.EnsureMeshImport(context.Background(), MeshImportSpec{
		Name:       "api",
		MeshName:   "global",
		Protocol:   mesh.ProtocolHTTP,
		Port:       8080,
		TargetPort: mesh.ImportPort("api"),
	})
	if err != nil {
		t.Fatalf("EnsureMeshImport: %v", err)
	}
	if dns != "mesh-api.ns-test.svc.cluster.local:8080" {
		t.Fatalf("unexpected import dns: %s", dns)
	}
	service, err := clientset.CoreV1().Services("ns-test").Get(context.Background(), "mesh-api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Fatalf("unexpected service type: %s", service.Spec.Type)
	}
	if service.Spec.Selector["app"] != mesh.DefaultGatewayName {
		t.Fatalf("unexpected selector: %#v", service.Spec.Selector)
	}
	if service.Spec.Selector[managedLabelKey] != "sealtun-mesh-global" {
		t.Fatalf("unexpected selector owner: %#v", service.Spec.Selector)
	}
	if service.Spec.Ports[0].TargetPort.IntVal != mesh.ImportPort("api") {
		t.Fatalf("unexpected target port: %#v", service.Spec.Ports[0].TargetPort)
	}
}

func TestCleanupMeshRemovesGatewayResources(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewSimpleClientset()
	client := &Client{
		clientset: clientset,
		namespace: "ns-test",
		domain:    "sealos.example",
	}

	if _, err := client.EnsureMeshGateway(ctx, MeshGatewaySpec{
		MeshName: "global",
		Token:    "secret-token",
		Routes:   []mesh.GatewayRoute{},
	}); err != nil {
		t.Fatalf("EnsureMeshGateway: %v", err)
	}
	if err := client.CleanupMesh(ctx, "global"); err != nil {
		t.Fatalf("CleanupMesh: %v", err)
	}

	name := "sealtun-mesh-global"
	if _, err := clientset.AppsV1().Deployments("ns-test").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("deployment should be deleted, got err=%v", err)
	}
	if _, err := clientset.CoreV1().Services("ns-test").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("service should be deleted, got err=%v", err)
	}
	if _, err := clientset.NetworkingV1().Ingresses("ns-test").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("ingress should be deleted, got err=%v", err)
	}
	if _, err := clientset.CoreV1().ConfigMaps("ns-test").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("configmap should be deleted, got err=%v", err)
	}
	if _, err := clientset.CoreV1().Secrets("ns-test").Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("secret should be deleted, got err=%v", err)
	}
}
