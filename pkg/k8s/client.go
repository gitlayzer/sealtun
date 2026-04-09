package k8s

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	clientset *kubernetes.Clientset
	namespace string
	domain    string // inferred sealos domain
}

// NewClient initializes a Kubernetes client from the sealtun config
func NewClient(kubeconfigPath string, authData *auth.AuthData) (*Client, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}
	config.WarningHandler = rest.NoWarnings{}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	namespace := "default"
	rawConfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err == nil {
		if ctx, ok := rawConfig.Contexts[rawConfig.CurrentContext]; ok {
			if ctx.Namespace != "" {
				namespace = ctx.Namespace
			}
		}
	}

	// Infer domain from region URL (e.g. https://cloud.sealos.io -> cloud.sealos.app)
	domain := "cloud.sealos.app"
	if authData != nil && authData.Region != "" {
		if u, err := url.Parse(authData.Region); err == nil {
			domain = u.Host
			if strings.Contains(domain, ":") {
				domain = strings.Split(domain, ":")[0]
			}
			if strings.Contains(domain, ".sealos.io") || strings.Contains(domain, ".sealos.run") {
				domain = "sealosgzg.site"
			}
		}
	}

	return &Client{
		clientset: clientset,
		namespace: namespace,
		domain:    domain,
	}, nil
}

// EnsureTunnel deploys the server module in kubernetes
func (c *Client) EnsureTunnel(ctx context.Context, tunnelID string, secret string, protocol string) (string, error) {
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	
	// Create or Update Deployment
	if err := c.ensureDeployment(ctx, name, secret); err != nil {
		return "", fmt.Errorf("failed to ensure deployment: %w", err)
	}

	// Create or Update Service
	if err := c.ensureService(ctx, name); err != nil {
		return "", fmt.Errorf("failed to ensure service: %w", err)
	}

	// Create or Update Ingress
	host, err := c.ensureIngress(ctx, name, protocol)
	if err != nil {
		return "", fmt.Errorf("failed to ensure ingress: %w", err)
	}

	return host, nil
}

func (c *Client) ensureDeployment(ctx context.Context, name, secret string) error {
	replicas := int32(1)
	labels := map[string]string{"app": name, "cloud.sealos.io/app-deploy-manager": name}
	
	f := false
	t := true
	u := int64(1001)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: &f,
					Containers: []corev1.Container{
						{
							Name:            name,
							Image:           "ghcr.io/gitlayzer/sealtun:latest", // Assumed image name
							ImagePullPolicy: corev1.PullAlways,
							Args:            []string{"server", "--secret", secret, "--port", "8080"},
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &f,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								RunAsNonRoot: &t,
								RunAsUser:    &u,
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt(8080),
									},
								},
								InitialDelaySeconds: 1,
								PeriodSeconds:       2,
							},
						},
					},
				},
			},
		},
	}

	deployClient := c.clientset.AppsV1().Deployments(c.namespace)
	existing, err := deployClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		_, err = deployClient.Create(ctx, deployment, metav1.CreateOptions{})
	} else {
		deployment.ResourceVersion = existing.ResourceVersion
		_, err = deployClient.Update(ctx, deployment, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) ensureService(ctx context.Context, name string) error {
	labels := map[string]string{"app": name, "cloud.sealos.io/app-deploy-manager": name}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Port: 80, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	svcClient := c.clientset.CoreV1().Services(c.namespace)
	existing, err := svcClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		_, err = svcClient.Create(ctx, service, metav1.CreateOptions{})
	} else {
		service.ResourceVersion = existing.ResourceVersion
		service.Spec.ClusterIP = existing.Spec.ClusterIP // immutable
		_, err = svcClient.Update(ctx, service, metav1.UpdateOptions{})
	}
	return err
}

func (c *Client) ensureIngress(ctx context.Context, name string, protocol string) (string, error) {
	host := fmt.Sprintf("%s-%s.%s", name, c.namespace, c.domain)
	pathType := netv1.PathTypePrefix
	ingressClass := "nginx"

	// 1. Ensure Tunnel Ingress (Always standard HTTP for WebSocket handshake)
	tunnelIngress := c.generateIngress(name, host, "/_sealtun/ws", &pathType, &ingressClass, "")
	if err := c.applyIngress(ctx, tunnelIngress); err != nil {
		return "", fmt.Errorf("failed to apply tunnel ingress: %w", err)
	}

	// 2. Ensure App Ingress (Handles the actual traffic)
	backendProtocol := ""
	isGRPC := false
	switch strings.ToLower(protocol) {
	case "grpc", "grpcs":
		backendProtocol = "GRPC"
		isGRPC = true
	case "tcp", "ws", "wss":
		backendProtocol = "WS"
	}

	appIngressName := name
	if isGRPC {
		// For gRPC, we MUST use a separate Ingress object to apply the GRPC annotation 
		// without breaking the WebSocket handshake on the tunnel path.
		appIngressName = name + "-app"
	}

	appIngress := c.generateIngress(appIngressName, host, "/", &pathType, &ingressClass, backendProtocol)
	if err := c.applyIngress(ctx, appIngress); err != nil {
		return "", fmt.Errorf("failed to apply app ingress: %w", err)
	}

	return host, nil
}

func (c *Client) generateIngress(name, host, path string, pathType *netv1.PathType, ingressClass *string, backendProtocol string) *netv1.Ingress {
	labels := map[string]string{
		"app":                                       name,
		"cloud.sealos.io/app-deploy-manager":        strings.TrimSuffix(name, "-app"),
		"cloud.sealos.io/app-deploy-manager-domain": strings.Split(host, ".")[0],
	}

	annotations := map[string]string{
		"kubernetes.io/ingress.class":                  "nginx",
		"nginx.ingress.kubernetes.io/proxy-body-size":     "32m",
		"nginx.ingress.kubernetes.io/ssl-redirect":        "false",
		"nginx.ingress.kubernetes.io/proxy-read-timeout":  "3600",
		"nginx.ingress.kubernetes.io/proxy-send-timeout":  "3600",
	}

	if backendProtocol != "" {
		annotations["nginx.ingress.kubernetes.io/backend-protocol"] = backendProtocol
	}

	return &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   c.namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: netv1.IngressSpec{
			IngressClassName: ingressClass,
			Rules: []netv1.IngressRule{
				{
					Host: host,
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: pathType,
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: strings.TrimSuffix(name, "-app"),
											Port: netv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
			TLS: []netv1.IngressTLS{
				{
					Hosts:      []string{host},
					SecretName: "wildcard-cert",
				},
			},
		},
	}
}

func (c *Client) applyIngress(ctx context.Context, ingress *netv1.Ingress) error {
	ingClient := c.clientset.NetworkingV1().Ingresses(c.namespace)
	existing, err := ingClient.Get(ctx, ingress.Name, metav1.GetOptions{})
	if err != nil {
		_, err = ingClient.Create(ctx, ingress, metav1.CreateOptions{})
	} else {
		ingress.ResourceVersion = existing.ResourceVersion
		_, err = ingClient.Update(ctx, ingress, metav1.UpdateOptions{})
	}
	return err
}

// Cleanup resources
func (c *Client) Cleanup(ctx context.Context, tunnelID string) error {
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	
	_ = c.clientset.AppsV1().Deployments(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	_ = c.clientset.CoreV1().Services(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	
	// Delete both potential Ingress resources
	_ = c.clientset.NetworkingV1().Ingresses(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	_ = c.clientset.NetworkingV1().Ingresses(c.namespace).Delete(ctx, name+"-app", metav1.DeleteOptions{})

	return nil
}

// WaitForReady waits for the deployment to become fully ready
func (c *Client) WaitForReady(ctx context.Context, tunnelID string) error {
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	deployClient := c.clientset.AppsV1().Deployments(c.namespace)
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			dep, err := deployClient.Get(ctx, name, metav1.GetOptions{})
			if err == nil && dep.Status.ReadyReplicas > 0 {
				return nil
			}
		}
	}
}
