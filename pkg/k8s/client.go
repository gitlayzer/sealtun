package k8s

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/labring/sealtun/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Client struct {
	clientset kubernetes.Interface
	namespace string
	domain    string // inferred sealos domain
}

type CleanupSummary struct {
	Deployments int
	Services    int
	Ingresses   int
}

type TunnelDiagnostics struct {
	Namespace  string                `json:"namespace"`
	Name       string                `json:"name"`
	Deployment DeploymentDiagnostics `json:"deployment"`
	Service    ServiceDiagnostics    `json:"service"`
	Ingress    IngressDiagnostics    `json:"ingress"`
	Pods       []PodDiagnostics      `json:"pods,omitempty"`
	Events     []EventDiagnostic     `json:"events,omitempty"`
	Warnings   []string              `json:"warnings,omitempty"`
}

type DeploymentDiagnostics struct {
	Exists            bool                  `json:"exists"`
	ReadyReplicas     int32                 `json:"readyReplicas"`
	AvailableReplicas int32                 `json:"availableReplicas"`
	DesiredReplicas   int32                 `json:"desiredReplicas"`
	UpdatedReplicas   int32                 `json:"updatedReplicas"`
	Conditions        []ConditionDiagnostic `json:"conditions,omitempty"`
}

type ServiceDiagnostics struct {
	Exists    bool     `json:"exists"`
	Type      string   `json:"type,omitempty"`
	ClusterIP string   `json:"clusterIp,omitempty"`
	Ports     []string `json:"ports,omitempty"`
}

type IngressDiagnostics struct {
	Exists    bool     `json:"exists"`
	ClassName string   `json:"className,omitempty"`
	Hosts     []string `json:"hosts,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	TLSHosts  []string `json:"tlsHosts,omitempty"`
}

type PodDiagnostics struct {
	Name          string                `json:"name"`
	Phase         string                `json:"phase"`
	Ready         bool                  `json:"ready"`
	RestartCount  int32                 `json:"restartCount"`
	Reason        string                `json:"reason,omitempty"`
	Message       string                `json:"message,omitempty"`
	ContainerInfo []ContainerDiagnostic `json:"containers,omitempty"`
	Conditions    []ConditionDiagnostic `json:"conditions,omitempty"`
}

type ContainerDiagnostic struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restartCount"`
	State        string `json:"state,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
	Image        string `json:"image,omitempty"`
}

type ConditionDiagnostic struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type EventDiagnostic struct {
	Type    string `json:"type,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	Object  string `json:"object,omitempty"`
}

type resourceKind string

const (
	resourceDeployment resourceKind = "deployment"
	resourceService    resourceKind = "service"
	resourceIngress    resourceKind = "ingress"
)

type createdResource struct {
	kind resourceKind
	name string
}

// NewClient initializes a Kubernetes client from the sealtun config
func NewClient(kubeconfigPath string, authData *auth.AuthData) (*Client, error) {
	rawConfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	config, err := clientcmd.NewDefaultClientConfig(*rawConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	config.WarningHandler = rest.NoWarnings{}

	return newClientFromRawConfig(config, *rawConfig, authData)
}

// NewClientFromKubeconfig initializes a Kubernetes client from a raw kubeconfig string.
func NewClientFromKubeconfig(kubeconfig string, authData *auth.AuthData) (*Client, error) {
	rawConfig, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return nil, err
	}

	config, err := clientcmd.NewDefaultClientConfig(*rawConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	config.WarningHandler = rest.NoWarnings{}

	return newClientFromRawConfig(config, *rawConfig, authData)
}

func newClientFromRawConfig(config *rest.Config, rawConfig clientcmdapi.Config, authData *auth.AuthData) (*Client, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	namespace := "default"
	if ctx, ok := rawConfig.Contexts[rawConfig.CurrentContext]; ok {
		if ctx.Namespace != "" {
			namespace = ctx.Namespace
		}
	}

	// Infer the public app domain from the selected Sealos region.
	domain := "cloud.sealos.app"
	if authData != nil && authData.SealosDomain != "" {
		domain = authData.SealosDomain
	} else if authData != nil && authData.Region != "" {
		if u, err := url.Parse(authData.Region); err == nil {
			host := u.Host
			if strings.Contains(host, ":") {
				host = strings.Split(host, ":")[0]
			}
			switch {
			case host == "cloud.sealos.io":
				domain = "cloud.sealos.app"
			case strings.HasSuffix(host, ".sealos.run"):
				region := strings.TrimSuffix(host, ".sealos.run")
				if region != "" {
					domain = fmt.Sprintf("sealos%s.site", strings.Split(region, ".")[0])
				}
			case strings.HasSuffix(host, ".sealos.io"):
				region := strings.TrimSuffix(host, ".sealos.io")
				if region != "" && region != "cloud" {
					domain = fmt.Sprintf("sealos%s.site", strings.Split(region, ".")[0])
				} else {
					domain = host
				}
			case host != "":
				domain = host
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
func (c *Client) EnsureTunnel(ctx context.Context, tunnelID string, secret string, protocol string, localPort string) (string, error) {
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	created := []createdResource{}
	rollback := true
	defer func() {
		if rollback {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = c.cleanupCreated(cleanupCtx, created)
		}
	}()

	// Create or Update Deployment
	deploymentCreated, err := c.ensureDeployment(ctx, name, secret, protocol, localPort)
	if err != nil {
		return "", fmt.Errorf("failed to ensure deployment: %w", err)
	}
	if deploymentCreated {
		created = append(created, createdResource{kind: resourceDeployment, name: name})
	}

	// Create or Update Service
	serviceCreated, err := c.ensureService(ctx, name)
	if err != nil {
		return "", fmt.Errorf("failed to ensure service: %w", err)
	}
	if serviceCreated {
		created = append(created, createdResource{kind: resourceService, name: name})
	}

	// Create or Update Ingress
	host, ingressCreated, err := c.ensureIngress(ctx, name)
	created = append(created, ingressCreated...)
	if err != nil {
		return "", fmt.Errorf("failed to ensure ingress: %w", err)
	}

	rollback = false
	return host, nil
}

func (c *Client) ensureDeployment(ctx context.Context, name, secret, protocol, localPort string) (bool, error) {
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
							Name: name,
							Image: fmt.Sprintf("ghcr.io/gitlayzer/sealtun:%s", func() string {
								v := version.Version
								if v == "dev" {
									return "latest"
								}
								// Detect if it is a 7-character git hash
								if match, _ := regexp.MatchString("^[0-9a-f]{7}$", v); match {
									return "sha-" + v
								}
								// Otherwise, assume it's a version tag (e.g., 0.1.0)
								return strings.TrimPrefix(v, "v")
							}()),
							ImagePullPolicy: corev1.PullAlways,
							Args:            []string{"server", "--secret", secret, "--port", "8080", "--protocol", protocol, "--local-port", localPort},
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
	if apierrors.IsNotFound(err) {
		_, err = deployClient.Create(ctx, deployment, metav1.CreateOptions{})
		return err == nil, err
	} else if err == nil {
		deployment.ResourceVersion = existing.ResourceVersion
		_, err = deployClient.Update(ctx, deployment, metav1.UpdateOptions{})
	}
	return false, err
}

func (c *Client) ensureService(ctx context.Context, name string) (bool, error) {
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
	if apierrors.IsNotFound(err) {
		_, err = svcClient.Create(ctx, service, metav1.CreateOptions{})
		return err == nil, err
	} else if err == nil {
		service.ResourceVersion = existing.ResourceVersion
		service.Spec.ClusterIP = existing.Spec.ClusterIP // immutable
		service.Spec.ClusterIPs = existing.Spec.ClusterIPs
		service.Spec.IPFamilies = existing.Spec.IPFamilies
		service.Spec.IPFamilyPolicy = existing.Spec.IPFamilyPolicy
		service.Spec.HealthCheckNodePort = existing.Spec.HealthCheckNodePort
		service.Spec.InternalTrafficPolicy = existing.Spec.InternalTrafficPolicy
		service.Spec.TrafficDistribution = existing.Spec.TrafficDistribution
		_, err = svcClient.Update(ctx, service, metav1.UpdateOptions{})
	}
	return false, err
}

func (c *Client) ensureIngress(ctx context.Context, name string) (string, []createdResource, error) {
	host := fmt.Sprintf("%s.%s", compactDNSLabel(fmt.Sprintf("%s-%s", name, c.namespace), 63), c.domain)
	pathType := netv1.PathTypePrefix
	ingressClass := "nginx"
	ingress := c.generateIngress(name, host, []string{"/_sealtun/ws", "/_sealtun/healthz", "/"}, &pathType, &ingressClass)
	ingressCreated, err := c.applyIngress(ctx, ingress)
	if err != nil {
		return "", nil, fmt.Errorf("failed to apply ingress: %w", err)
	}
	if ingressCreated {
		return host, []createdResource{{kind: resourceIngress, name: name}}, nil
	}

	return host, nil, nil
}

func (c *Client) generateIngress(name, host string, paths []string, pathType *netv1.PathType, ingressClass *string) *netv1.Ingress {
	labels := map[string]string{
		"app":                                name,
		"cloud.sealos.io/app-deploy-manager": strings.TrimSuffix(name, "-app"),
		"cloud.sealos.io/app-deploy-manager-domain": strings.Split(host, ".")[0],
	}

	annotations := map[string]string{
		"kubernetes.io/ingress.class":                    "nginx",
		"nginx.ingress.kubernetes.io/proxy-body-size":    "32m",
		"nginx.ingress.kubernetes.io/ssl-redirect":       "false",
		"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
		"nginx.ingress.kubernetes.io/proxy-send-timeout": "3600",
	}

	httpPaths := make([]netv1.HTTPIngressPath, 0, len(paths))
	for _, path := range paths {
		httpPaths = append(httpPaths, netv1.HTTPIngressPath{
			Path:     path,
			PathType: pathType,
			Backend: netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: strings.TrimSuffix(name, "-app"),
					Port: netv1.ServiceBackendPort{Number: 80},
				},
			},
		})
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
							Paths: httpPaths,
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

func (c *Client) applyIngress(ctx context.Context, ingress *netv1.Ingress) (bool, error) {
	ingClient := c.clientset.NetworkingV1().Ingresses(c.namespace)
	existing, err := ingClient.Get(ctx, ingress.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = ingClient.Create(ctx, ingress, metav1.CreateOptions{})
		return err == nil, err
	} else if err == nil {
		ingress.ResourceVersion = existing.ResourceVersion
		_, err = ingClient.Update(ctx, ingress, metav1.UpdateOptions{})
	}
	return false, err
}

// Cleanup resources
func (c *Client) Cleanup(ctx context.Context, tunnelID string) error {
	name := fmt.Sprintf("sealtun-%s", tunnelID)

	for _, deleteFn := range []func() error{
		func() error {
			return c.clientset.AppsV1().Deployments(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
		},
		func() error {
			return c.clientset.CoreV1().Services(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
		},
		func() error {
			return c.clientset.NetworkingV1().Ingresses(c.namespace).Delete(ctx, name, metav1.DeleteOptions{})
		},
		func() error {
			return c.clientset.NetworkingV1().Ingresses(c.namespace).Delete(ctx, name+"-app", metav1.DeleteOptions{})
		},
	} {
		if err := deleteFn(); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (c *Client) cleanupCreated(ctx context.Context, resources []createdResource) error {
	for i := len(resources) - 1; i >= 0; i-- {
		resource := resources[i]
		var err error
		switch resource.kind {
		case resourceDeployment:
			err = c.clientset.AppsV1().Deployments(c.namespace).Delete(ctx, resource.name, metav1.DeleteOptions{})
		case resourceService:
			err = c.clientset.CoreV1().Services(c.namespace).Delete(ctx, resource.name, metav1.DeleteOptions{})
		case resourceIngress:
			err = c.clientset.NetworkingV1().Ingresses(c.namespace).Delete(ctx, resource.name, metav1.DeleteOptions{})
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *Client) CleanupManaged(ctx context.Context, tunnelIDs []string) (*CleanupSummary, error) {
	summary := &CleanupSummary{}

	seen := map[string]struct{}{}
	for _, tunnelID := range tunnelIDs {
		if tunnelID == "" {
			continue
		}
		if _, ok := seen[tunnelID]; ok {
			continue
		}
		seen[tunnelID] = struct{}{}

		name := fmt.Sprintf("sealtun-%s", tunnelID)
		if err := c.clientset.AppsV1().Deployments(c.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err == nil {
			summary.Deployments++
		} else if !apierrors.IsNotFound(err) {
			return nil, err
		}

		if err := c.clientset.CoreV1().Services(c.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err == nil {
			summary.Services++
		} else if !apierrors.IsNotFound(err) {
			return nil, err
		}

		for _, ingressName := range []string{name, name + "-app"} {
			if err := c.clientset.NetworkingV1().Ingresses(c.namespace).Delete(ctx, ingressName, metav1.DeleteOptions{}); err == nil {
				summary.Ingresses++
			} else if !apierrors.IsNotFound(err) {
				return nil, err
			}
		}
	}

	return summary, nil
}

func (c *Client) DiagnoseTunnel(ctx context.Context, tunnelID string) (*TunnelDiagnostics, error) {
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	diag := &TunnelDiagnostics{
		Namespace: c.namespace,
		Name:      name,
	}

	deployment, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		diag.Warnings = append(diag.Warnings, "remote deployment is missing")
	} else if err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", name, err)
	} else {
		diag.Deployment = deploymentDiagnostics(deployment)
		if diag.Deployment.ReadyReplicas == 0 {
			diag.Warnings = append(diag.Warnings, "remote deployment has no ready replicas")
		}
	}

	service, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		diag.Warnings = append(diag.Warnings, "remote service is missing")
	} else if err != nil {
		return nil, fmt.Errorf("get service %s: %w", name, err)
	} else {
		diag.Service = serviceDiagnostics(service)
		if len(diag.Service.Ports) == 0 {
			diag.Warnings = append(diag.Warnings, "remote service has no ports")
		}
	}

	ingress, err := c.clientset.NetworkingV1().Ingresses(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		diag.Warnings = append(diag.Warnings, "remote ingress is missing")
	} else if err != nil {
		return nil, fmt.Errorf("get ingress %s: %w", name, err)
	} else {
		diag.Ingress = ingressDiagnostics(ingress)
		if len(diag.Ingress.Hosts) == 0 {
			diag.Warnings = append(diag.Warnings, "remote ingress has no hosts")
		}
	}
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", name),
	})
	if err != nil {
		return nil, fmt.Errorf("list pods for %s: %w", name, err)
	}
	if len(pods.Items) == 0 {
		diag.Warnings = append(diag.Warnings, "remote tunnel pod is missing")
	}
	for i := range pods.Items {
		podDiag := podDiagnostics(&pods.Items[i])
		diag.Pods = append(diag.Pods, podDiag)
		if !podDiag.Ready {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote pod %s is not ready", podDiag.Name))
		}
	}

	events, err := c.clientset.CoreV1().Events(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote events unavailable: %v", err))
		return diag, nil
	}
	diag.Events = filterEventDiagnostics(events.Items, name, 8)

	return diag, nil
}

func (c *Client) Namespace() string {
	return c.namespace
}

func (c *Client) WithNamespace(namespace string) *Client {
	if namespace == "" || namespace == c.namespace {
		return c
	}

	return &Client{
		clientset: c.clientset,
		namespace: namespace,
		domain:    c.domain,
	}
}

func compactDNSLabel(value string, limit int) string {
	value = strings.ToLower(value)
	value = regexp.MustCompile("[^a-z0-9-]+").ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		value = "sealtun"
	}
	if len(value) <= limit {
		return value
	}

	sum := sha1.Sum([]byte(value))
	suffix := hex.EncodeToString(sum[:])[:8]
	keep := limit - len(suffix) - 1
	if keep < 1 {
		keep = 1
	}
	return strings.Trim(value[:keep], "-") + "-" + suffix
}

func deploymentDiagnostics(dep *appsv1.Deployment) DeploymentDiagnostics {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}

	diag := DeploymentDiagnostics{
		Exists:            true,
		ReadyReplicas:     dep.Status.ReadyReplicas,
		AvailableReplicas: dep.Status.AvailableReplicas,
		DesiredReplicas:   desired,
		UpdatedReplicas:   dep.Status.UpdatedReplicas,
		Conditions:        make([]ConditionDiagnostic, 0, len(dep.Status.Conditions)),
	}
	for _, condition := range dep.Status.Conditions {
		diag.Conditions = append(diag.Conditions, ConditionDiagnostic{
			Type:    string(condition.Type),
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		})
	}
	return diag
}

func serviceDiagnostics(service *corev1.Service) ServiceDiagnostics {
	diag := ServiceDiagnostics{
		Exists:    true,
		Type:      string(service.Spec.Type),
		ClusterIP: service.Spec.ClusterIP,
		Ports:     make([]string, 0, len(service.Spec.Ports)),
	}
	for _, port := range service.Spec.Ports {
		target := port.TargetPort.String()
		if target == "" {
			target = "-"
		}
		diag.Ports = append(diag.Ports, fmt.Sprintf("%s/%d->%s", port.Protocol, port.Port, target))
	}
	return diag
}

func ingressDiagnostics(ingress *netv1.Ingress) IngressDiagnostics {
	diag := IngressDiagnostics{
		Exists: true,
	}
	if ingress.Spec.IngressClassName != nil {
		diag.ClassName = *ingress.Spec.IngressClassName
	}
	for _, rule := range ingress.Spec.Rules {
		if rule.Host != "" {
			diag.Hosts = append(diag.Hosts, rule.Host)
		}
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			diag.Paths = append(diag.Paths, path.Path)
		}
	}
	for _, tls := range ingress.Spec.TLS {
		diag.TLSHosts = append(diag.TLSHosts, tls.Hosts...)
	}
	return diag
}

func podDiagnostics(pod *corev1.Pod) PodDiagnostics {
	diag := PodDiagnostics{
		Name:          pod.Name,
		Phase:         string(pod.Status.Phase),
		Ready:         podReady(pod),
		ContainerInfo: make([]ContainerDiagnostic, 0, len(pod.Status.ContainerStatuses)),
		Conditions:    make([]ConditionDiagnostic, 0, len(pod.Status.Conditions)),
	}
	for _, status := range pod.Status.ContainerStatuses {
		container := ContainerDiagnostic{
			Name:         status.Name,
			Ready:        status.Ready,
			RestartCount: status.RestartCount,
			Image:        status.Image,
		}
		diag.RestartCount += status.RestartCount
		switch {
		case status.State.Waiting != nil:
			container.State = "waiting"
			container.Reason = status.State.Waiting.Reason
			container.Message = status.State.Waiting.Message
		case status.State.Terminated != nil:
			container.State = "terminated"
			container.Reason = status.State.Terminated.Reason
			container.Message = status.State.Terminated.Message
		case status.State.Running != nil:
			container.State = "running"
		}
		if diag.Reason == "" && container.Reason != "" {
			diag.Reason = container.Reason
			diag.Message = container.Message
		}
		diag.ContainerInfo = append(diag.ContainerInfo, container)
	}
	for _, condition := range pod.Status.Conditions {
		diag.Conditions = append(diag.Conditions, ConditionDiagnostic{
			Type:    string(condition.Type),
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		})
	}
	return diag
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func filterEventDiagnostics(events []corev1.Event, name string, limit int) []EventDiagnostic {
	result := []EventDiagnostic{}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.InvolvedObject.Name != name {
			continue
		}
		result = append(result, EventDiagnostic{
			Type:    event.Type,
			Reason:  event.Reason,
			Message: event.Message,
			Object:  fmt.Sprintf("%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Name),
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

// WaitForReady waits for the deployment to become fully ready
func (c *Client) WaitForReady(ctx context.Context, tunnelID string) error {
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	deployClient := c.clientset.AppsV1().Deployments(c.namespace)
	var lastErr error

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w; last Kubernetes error: %v", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-time.After(2 * time.Second):
			dep, err := deployClient.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				lastErr = err
				continue
			}
			if err != nil {
				return err
			}
			for _, condition := range dep.Status.Conditions {
				if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
					return fmt.Errorf("deployment %s failed: %s", name, condition.Message)
				}
			}
			if dep.Status.ReadyReplicas > 0 {
				return nil
			}
		}
	}
}
