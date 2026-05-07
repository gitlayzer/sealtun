package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Client struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	namespace     string
	domain        string // inferred sealos domain
}

type CleanupSummary struct {
	Deployments  int
	Services     int
	Ingresses    int
	Certificates int
	Issuers      int
	Secrets      int
}

type TunnelOptions struct {
	CustomDomain string
}

type TunnelHosts struct {
	PublicHost   string
	SealosHost   string
	CustomDomain string
}

type TunnelDiagnostics struct {
	Namespace   string                  `json:"namespace"`
	Name        string                  `json:"name"`
	Deployment  DeploymentDiagnostics   `json:"deployment"`
	Service     ServiceDiagnostics      `json:"service"`
	Ingress     IngressDiagnostics      `json:"ingress"`
	Certificate *CertificateDiagnostics `json:"certificate,omitempty"`
	Pods        []PodDiagnostics        `json:"pods,omitempty"`
	Events      []EventDiagnostic       `json:"events,omitempty"`
	Warnings    []string                `json:"warnings,omitempty"`
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

type CertificateDiagnostics struct {
	Exists     bool                  `json:"exists"`
	Ready      bool                  `json:"ready"`
	SecretName string                `json:"secretName,omitempty"`
	DNSNames   []string              `json:"dnsNames,omitempty"`
	Conditions []ConditionDiagnostic `json:"conditions,omitempty"`
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
	resourceDeployment  resourceKind = "deployment"
	resourceService     resourceKind = "service"
	resourceIngress     resourceKind = "ingress"
	resourceIssuer      resourceKind = "issuer"
	resourceCertificate resourceKind = "certificate"
	resourceSecret      resourceKind = "secret"
)

const (
	managedLabelKey       = "cloud.sealos.io/app-deploy-manager"
	managedDomainLabelKey = "cloud.sealos.io/app-deploy-manager-domain"
	tunnelAuthSecretKey   = "secret"
)

var reservedCustomDomainSuffixes = []string{
	"cloud.sealos.app",
	"cloud.sealos.io",
	"sealosgzg.site",
	"sealoshzh.site",
}

var (
	tunnelIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,53}[a-z0-9])?$`)
	dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
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
	dynamicClient, err := dynamic.NewForConfig(config)
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
		domain = normalizeHostname(authData.SealosDomain)
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
	if err := validateCustomDomainHostname(domain); err != nil {
		return nil, fmt.Errorf("invalid Sealos ingress domain %q: %w", domain, err)
	}

	return &Client{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		namespace:     namespace,
		domain:        domain,
	}, nil
}

// EnsureTunnel deploys the server module in kubernetes
func (c *Client) EnsureTunnel(ctx context.Context, tunnelID string, secret string, protocol string, localPort string) (string, error) {
	hosts, err := c.EnsureTunnelWithOptions(ctx, tunnelID, secret, protocol, localPort, TunnelOptions{})
	if err != nil {
		return "", err
	}
	return hosts.PublicHost, nil
}

// EnsureTunnelWithOptions deploys the server module in kubernetes.
func (c *Client) EnsureTunnelWithOptions(ctx context.Context, tunnelID string, secret string, protocol string, localPort string, opts TunnelOptions) (TunnelHosts, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return TunnelHosts{}, err
	}
	if strings.TrimSpace(secret) == "" {
		return TunnelHosts{}, fmt.Errorf("tunnel secret is required")
	}
	if err := tunnelprotocol.ValidateServer(protocol); err != nil {
		return TunnelHosts{}, err
	}
	if err := validateLocalPort(localPort); err != nil {
		return TunnelHosts{}, err
	}

	name := fmt.Sprintf("sealtun-%s", tunnelID)
	opts.CustomDomain = normalizeHostname(opts.CustomDomain)
	created := []createdResource{}
	rollback := true
	customSnapshotsCaptured := false
	var previousIssuer *unstructured.Unstructured
	var previousCertificate *unstructured.Unstructured
	var empty TunnelHosts
	defer func() {
		if rollback {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = c.cleanupCreated(cleanupCtx, created)
			if customSnapshotsCaptured {
				_ = c.restoreDynamicResource(cleanupCtx, issuerGVR, name, previousIssuer)
				_ = c.restoreDynamicResource(cleanupCtx, certificateGVR, name, previousCertificate)
			}
		}
	}()

	authSecretCreated, err := c.ensureAuthSecret(ctx, name, secret)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure tunnel auth secret: %w", err)
	}
	if authSecretCreated {
		created = append(created, createdResource{kind: resourceSecret, name: authSecretName(name)})
	}

	// Create or Update Deployment
	deploymentCreated, err := c.ensureDeployment(ctx, name, protocol, localPort)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure deployment: %w", err)
	}
	if deploymentCreated {
		created = append(created, createdResource{kind: resourceDeployment, name: name})
	}

	// Create or Update Service
	serviceCreated, err := c.ensureService(ctx, name)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure service: %w", err)
	}
	if serviceCreated {
		created = append(created, createdResource{kind: resourceService, name: name})
	}

	if opts.CustomDomain != "" {
		previousIssuer, err = c.getDynamicResource(ctx, issuerGVR, name)
		if err != nil {
			return empty, fmt.Errorf("snapshot issuer %s: %w", name, err)
		}
		previousCertificate, err = c.getDynamicResource(ctx, certificateGVR, name)
		if err != nil {
			return empty, fmt.Errorf("snapshot certificate %s: %w", name, err)
		}
		customSnapshotsCaptured = true

		customCreated, err := c.ensureCustomDomainResources(ctx, name, opts.CustomDomain)
		if err != nil {
			return empty, fmt.Errorf("failed to ensure custom domain certificate resources: %w", err)
		}
		created = append(created, customCreated...)
	}

	// Create or Update Ingress only after custom-domain certificate resources are safe.
	hosts, ingressCreated, err := c.ensureIngress(ctx, name, opts)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure ingress: %w", err)
	}
	created = append(created, ingressCreated...)

	rollback = false
	return hosts, nil
}

func authSecretName(name string) string {
	return name + "-auth"
}

func managedLabels(name string) map[string]string {
	return map[string]string{
		"app":           name,
		managedLabelKey: name,
	}
}

func managedLabelMatches(labels map[string]string, owner string) bool {
	return labels != nil && labels[managedLabelKey] == owner
}

func rejectUnmanagedExisting(kind, resourceName, owner string, labels map[string]string) error {
	if managedLabelMatches(labels, owner) {
		return nil
	}
	return fmt.Errorf("%s %s already exists but is not managed by Sealtun", kind, resourceName)
}

func validateTunnelID(tunnelID string) error {
	if tunnelID == "" {
		return fmt.Errorf("tunnel id is required")
	}
	if !tunnelIDPattern.MatchString(tunnelID) {
		return fmt.Errorf("invalid tunnel id %q", tunnelID)
	}
	return nil
}

func validateLocalPort(port string) error {
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("invalid local port %q: must be a number between 1 and 65535", port)
	}
	return nil
}

func (c *Client) ensureAuthSecret(ctx context.Context, name, secret string) (bool, error) {
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authSecretName(name),
			Namespace: c.namespace,
			Labels:    managedLabels(name),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			tunnelAuthSecretKey: []byte(secret),
		},
	}

	secretClient := c.clientset.CoreV1().Secrets(c.namespace)
	existing, err := secretClient.Get(ctx, authSecret.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secretClient.Create(ctx, authSecret, metav1.CreateOptions{})
		return err == nil, err
	} else if err == nil {
		if err := rejectUnmanagedExisting("secret", authSecret.Name, name, existing.Labels); err != nil {
			return false, err
		}
		authSecret.ResourceVersion = existing.ResourceVersion
		_, err = secretClient.Update(ctx, authSecret, metav1.UpdateOptions{})
	}
	return false, err
}

func (c *Client) ensureDeployment(ctx context.Context, name, protocol, localPort string) (bool, error) {
	replicas := int32(1)
	labels := managedLabels(name)

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
							Args:            []string{"server", "--secret-env", "SEALTUN_SECRET", "--port", "8080", "--protocol", protocol, "--local-port", localPort},
							Env: []corev1.EnvVar{
								{
									Name: "SEALTUN_SECRET",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName(name)},
											Key:                  tunnelAuthSecretKey,
										},
									},
								},
							},
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
		if err := rejectUnmanagedExisting("deployment", name, name, existing.Labels); err != nil {
			return false, err
		}
		deployment.ResourceVersion = existing.ResourceVersion
		_, err = deployClient.Update(ctx, deployment, metav1.UpdateOptions{})
	}
	return false, err
}

func (c *Client) ensureService(ctx context.Context, name string) (bool, error) {
	labels := managedLabels(name)
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
		if err := rejectUnmanagedExisting("service", name, name, existing.Labels); err != nil {
			return false, err
		}
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

func (c *Client) ensureIngress(ctx context.Context, name string, opts TunnelOptions) (TunnelHosts, []createdResource, error) {
	return c.ensureIngressForHost(ctx, name, c.sealosHost(name), opts)
}

func (c *Client) ensureIngressForHost(ctx context.Context, name, sealosHost string, opts TunnelOptions) (TunnelHosts, []createdResource, error) {
	if err := validateCustomDomainTarget(opts.CustomDomain, sealosHost); err != nil {
		return TunnelHosts{}, nil, err
	}

	publicHost := sealosHost
	if opts.CustomDomain != "" {
		publicHost = opts.CustomDomain
	}
	pathType := netv1.PathTypePrefix
	ingressClass := "nginx"
	ingress := c.generateIngress(name, sealosHost, opts.CustomDomain, []string{"/_sealtun/ws", "/_sealtun/healthz", "/"}, &pathType, &ingressClass)
	ingressCreated, err := c.applyIngress(ctx, ingress)
	if err != nil {
		return TunnelHosts{}, nil, fmt.Errorf("failed to apply ingress: %w", err)
	}
	hosts := TunnelHosts{
		PublicHost:   publicHost,
		SealosHost:   sealosHost,
		CustomDomain: opts.CustomDomain,
	}
	if ingressCreated {
		return hosts, []createdResource{{kind: resourceIngress, name: name}}, nil
	}

	return hosts, nil, nil
}

func (c *Client) sealosHost(name string) string {
	return fmt.Sprintf("%s.%s", compactDNSLabel(fmt.Sprintf("%s-%s", name, c.namespace), 63), c.domain)
}

func (c *Client) SealosHost(tunnelID string) string {
	return c.sealosHost(fmt.Sprintf("sealtun-%s", tunnelID))
}

func validateCustomDomainTarget(customDomain, sealosHost string) error {
	if customDomain == "" {
		return nil
	}
	customDomain = normalizeHostname(customDomain)
	if err := validateCustomDomainHostname(customDomain); err != nil {
		return err
	}
	sealosHost = normalizeHostname(sealosHost)
	if customDomain == sealosHost {
		return fmt.Errorf("custom domain %s must be different from the Sealos CNAME target %s", customDomain, sealosHost)
	}
	if suffix := managedDomainSuffix(sealosHost); suffix != "" && (customDomain == suffix || strings.HasSuffix(customDomain, "."+suffix)) {
		return fmt.Errorf("custom domain %s must not be under the Sealos-managed domain %s", customDomain, suffix)
	}
	for _, suffix := range reservedCustomDomainSuffixes {
		if customDomain == suffix || strings.HasSuffix(customDomain, "."+suffix) {
			return fmt.Errorf("custom domain %s must not be under reserved Sealos domain %s", customDomain, suffix)
		}
	}
	return nil
}

func validateCustomDomainHostname(value string) error {
	if value == "" {
		return nil
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/:@") {
		return fmt.Errorf("invalid custom domain %q: provide a hostname only, not a URL", value)
	}
	if len(value) > 253 {
		return fmt.Errorf("invalid custom domain %q: hostname is too long", value)
	}
	if net.ParseIP(value) != nil {
		return fmt.Errorf("invalid custom domain %q: custom domain must be a DNS hostname, not an IP address", value)
	}
	if !strings.Contains(value, ".") {
		return fmt.Errorf("invalid custom domain %q: custom domain must contain at least two labels", value)
	}
	for _, label := range strings.Split(value, ".") {
		if !dnsLabelPattern.MatchString(label) {
			return fmt.Errorf("invalid custom domain %q: label %q is not DNS compatible", value, label)
		}
	}
	return nil
}

func normalizeHostname(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func managedDomainSuffix(sealosHost string) string {
	_, suffix, ok := strings.Cut(normalizeHostname(sealosHost), ".")
	if !ok {
		return ""
	}
	return suffix
}

func hostnameInList(hosts []string, want string) bool {
	normalizedWant := normalizeHostname(want)
	for _, host := range hosts {
		if normalizeHostname(host) == normalizedWant {
			return true
		}
	}
	return false
}

func (c *Client) generateIngress(name, sealosHost, customDomain string, paths []string, pathType *netv1.PathType, ingressClass *string) *netv1.Ingress {
	labels := map[string]string{
		"app":                 name,
		managedLabelKey:       strings.TrimSuffix(name, "-app"),
		managedDomainLabelKey: strings.Split(sealosHost, ".")[0],
	}

	annotations := map[string]string{
		"kubernetes.io/ingress.class":                         "nginx",
		"nginx.ingress.kubernetes.io/backend-protocol":        "HTTP",
		"nginx.ingress.kubernetes.io/client-body-buffer-size": "64k",
		"nginx.ingress.kubernetes.io/proxy-body-size":         "32m",
		"nginx.ingress.kubernetes.io/proxy-buffer-size":       "64k",
		"nginx.ingress.kubernetes.io/proxy-read-timeout":      "3600",
		"nginx.ingress.kubernetes.io/proxy-send-timeout":      "3600",
		"nginx.ingress.kubernetes.io/server-snippet":          "client_header_buffer_size 64k;\nlarge_client_header_buffers 4 128k;",
		"nginx.ingress.kubernetes.io/ssl-redirect":            "false",
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

	hosts := []struct {
		host       string
		secretName string
	}{
		{host: sealosHost, secretName: "wildcard-cert"},
	}
	if customDomain != "" && customDomain != sealosHost {
		hosts = append(hosts, struct {
			host       string
			secretName string
		}{host: customDomain, secretName: name})
	}

	rules := make([]netv1.IngressRule, 0, len(hosts))
	tls := make([]netv1.IngressTLS, 0, len(hosts))
	for _, item := range hosts {
		rules = append(rules, netv1.IngressRule{
			Host: item.host,
			IngressRuleValue: netv1.IngressRuleValue{
				HTTP: &netv1.HTTPIngressRuleValue{
					Paths: httpPaths,
				},
			},
		})
		tls = append(tls, netv1.IngressTLS{
			Hosts:      []string{item.host},
			SecretName: item.secretName,
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
			Rules:            rules,
			TLS:              tls,
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
		owner := strings.TrimSuffix(ingress.Name, "-app")
		if !managedLabelMatches(existing.Labels, owner) {
			return false, fmt.Errorf("ingress %s already exists but is not managed by Sealtun", ingress.Name)
		}
		ingress.ResourceVersion = existing.ResourceVersion
		_, err = ingClient.Update(ctx, ingress, metav1.UpdateOptions{})
	}
	return false, err
}

var (
	issuerGVR = schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "issuers",
	}
	certificateGVR = schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}
)

func (c *Client) ensureCustomDomainResources(ctx context.Context, name, customDomain string) ([]createdResource, error) {
	if customDomain == "" {
		return nil, nil
	}
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic Kubernetes client is unavailable")
	}
	if err := c.validateCustomDomainSecretSlot(ctx, name); err != nil {
		return nil, err
	}

	created := []createdResource{}
	issuerCreated, err := c.applyDynamicResource(ctx, issuerGVR, customDomainIssuer(name))
	if err != nil {
		return nil, fmt.Errorf("apply issuer %s: %w", name, err)
	}
	if issuerCreated {
		created = append(created, createdResource{kind: resourceIssuer, name: name})
	}

	certificateCreated, err := c.applyDynamicResource(ctx, certificateGVR, customDomainCertificate(name, customDomain))
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.cleanupCreated(cleanupCtx, created)
		return nil, fmt.Errorf("apply certificate %s: %w", name, err)
	}
	if certificateCreated {
		created = append(created, createdResource{kind: resourceCertificate, name: name})
	}

	return created, nil
}

func customDomainIssuer(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Issuer",
		"metadata": map[string]interface{}{
			"name": name,
			"labels": map[string]interface{}{
				managedLabelKey: name,
			},
		},
		"spec": map[string]interface{}{
			"acme": map[string]interface{}{
				"server": "https://acme-v02.api.letsencrypt.org/directory",
				"email":  "admin@sealos.io",
				"privateKeySecretRef": map[string]interface{}{
					"name": "letsencrypt-prod",
				},
				"solvers": []interface{}{
					map[string]interface{}{
						"http01": map[string]interface{}{
							"ingress": map[string]interface{}{
								"class":       "nginx",
								"serviceType": "ClusterIP",
							},
						},
					},
				},
			},
		},
	}}
}

func customDomainCertificate(name, customDomain string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]interface{}{
			"name": name,
			"labels": map[string]interface{}{
				managedLabelKey: name,
			},
		},
		"spec": map[string]interface{}{
			"secretName": name,
			"secretTemplate": map[string]interface{}{
				"labels": map[string]interface{}{
					managedLabelKey: name,
				},
			},
			"dnsNames": []interface{}{
				customDomain,
			},
			"issuerRef": map[string]interface{}{
				"name": name,
				"kind": "Issuer",
			},
		},
	}}
}

func (c *Client) applyDynamicResource(ctx context.Context, gvr schema.GroupVersionResource, resource *unstructured.Unstructured) (bool, error) {
	resource.SetNamespace(c.namespace)
	resClient := c.dynamicClient.Resource(gvr).Namespace(c.namespace)
	existing, err := resClient.Get(ctx, resource.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = resClient.Create(ctx, resource, metav1.CreateOptions{})
		return err == nil, err
	} else if err == nil {
		if err := rejectUnmanagedExisting(gvr.Resource, resource.GetName(), resource.GetName(), existing.GetLabels()); err != nil {
			return false, err
		}
		resource.SetResourceVersion(existing.GetResourceVersion())
		_, err = resClient.Update(ctx, resource, metav1.UpdateOptions{})
	}
	return false, err
}

func (c *Client) validateCustomDomainSecretSlot(ctx context.Context, name string) error {
	secret, err := c.clientset.CoreV1().Secrets(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check custom domain TLS secret %s: %w", name, err)
	}
	if managedLabelMatches(secret.Labels, name) {
		return nil
	}
	cert, err := c.getDynamicResource(ctx, certificateGVR, name)
	if err != nil {
		return fmt.Errorf("check custom domain certificate %s: %w", name, err)
	}
	if cert != nil && managedLabelMatches(cert.GetLabels(), name) && certificateSecretNameMatches(cert, name) {
		return nil
	}
	return fmt.Errorf("secret %s already exists but is not managed by Sealtun", name)
}

func certificateSecretNameMatches(cert *unstructured.Unstructured, secretName string) bool {
	if cert == nil {
		return false
	}
	value, ok, err := unstructured.NestedString(cert.Object, "spec", "secretName")
	return err == nil && ok && value == secretName
}

func (c *Client) deleteManagedDynamicResourceIfExists(ctx context.Context, gvr schema.GroupVersionResource, name string) (bool, bool, error) {
	if c.dynamicClient == nil {
		return false, false, nil
	}
	resClient := c.dynamicClient.Resource(gvr).Namespace(c.namespace)
	resource, err := resClient.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !managedLabelMatches(resource.GetLabels(), name) {
		return false, false, nil
	}
	if err := resClient.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return true, false, err
	}
	return true, true, nil
}

func (c *Client) ConfigureCustomDomain(ctx context.Context, tunnelID, sealosHost, customDomain string) (TunnelHosts, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return TunnelHosts{}, err
	}
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	customDomain = normalizeHostname(customDomain)
	if sealosHost == "" {
		sealosHost = c.sealosHost(name)
	}
	if err := validateCustomDomainTarget(customDomain, sealosHost); err != nil {
		return TunnelHosts{}, err
	}
	if err := c.validateTunnelCoreResources(ctx, name); err != nil {
		return TunnelHosts{}, err
	}

	previousIssuer, err := c.getDynamicResource(ctx, issuerGVR, name)
	if err != nil {
		return TunnelHosts{}, fmt.Errorf("snapshot issuer %s: %w", name, err)
	}
	previousCertificate, err := c.getDynamicResource(ctx, certificateGVR, name)
	if err != nil {
		return TunnelHosts{}, fmt.Errorf("snapshot certificate %s: %w", name, err)
	}
	created := []createdResource{}
	rollback := true
	defer func() {
		if rollback {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = c.cleanupCreated(cleanupCtx, created)
			_ = c.restoreDynamicResource(cleanupCtx, issuerGVR, name, previousIssuer)
			_ = c.restoreDynamicResource(cleanupCtx, certificateGVR, name, previousCertificate)
		}
	}()

	customCreated, err := c.ensureCustomDomainResources(ctx, name, customDomain)
	if err != nil {
		return TunnelHosts{}, err
	}
	created = append(created, customCreated...)

	hosts, ingressCreated, err := c.ensureIngressForHost(ctx, name, sealosHost, TunnelOptions{CustomDomain: customDomain})
	if err != nil {
		return TunnelHosts{}, err
	}
	created = append(created, ingressCreated...)
	rollback = false
	return hosts, nil
}

func (c *Client) ClearCustomDomain(ctx context.Context, tunnelID, sealosHost string) (TunnelHosts, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return TunnelHosts{}, err
	}
	name := fmt.Sprintf("sealtun-%s", tunnelID)
	if sealosHost == "" {
		sealosHost = c.sealosHost(name)
	}
	if err := c.validateTunnelCoreResources(ctx, name); err != nil {
		return TunnelHosts{}, err
	}
	hosts, _, err := c.ensureIngressForHost(ctx, name, sealosHost, TunnelOptions{})
	if err != nil {
		return TunnelHosts{}, err
	}
	if err := c.cleanupCustomDomainResources(ctx, name); err != nil {
		return hosts, fmt.Errorf("custom domain certificate cleanup incomplete: %w", err)
	}
	return hosts, nil
}

func (c *Client) validateTunnelCoreResources(ctx context.Context, name string) error {
	deployment, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("remote deployment %s is missing", name)
	} else if err != nil {
		return fmt.Errorf("get deployment %s: %w", name, err)
	} else if !managedLabelMatches(deployment.Labels, name) {
		return fmt.Errorf("remote deployment %s is not managed by Sealtun", name)
	}
	service, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("remote service %s is missing", name)
	} else if err != nil {
		return fmt.Errorf("get service %s: %w", name, err)
	} else if !managedLabelMatches(service.Labels, name) {
		return fmt.Errorf("remote service %s is not managed by Sealtun", name)
	}
	return nil
}

func (c *Client) getDynamicResource(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	if c.dynamicClient == nil {
		return nil, nil
	}
	resource, err := c.dynamicClient.Resource(gvr).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resource.DeepCopy(), nil
}

func (c *Client) restoreDynamicResource(ctx context.Context, gvr schema.GroupVersionResource, name string, snapshot *unstructured.Unstructured) error {
	if c.dynamicClient == nil {
		return nil
	}
	resClient := c.dynamicClient.Resource(gvr).Namespace(c.namespace)
	current, err := resClient.Get(ctx, name, metav1.GetOptions{})
	if snapshot == nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !managedLabelMatches(current.GetLabels(), name) {
			return nil
		}
		return resClient.Delete(ctx, current.GetName(), metav1.DeleteOptions{})
	}
	if !managedLabelMatches(snapshot.GetLabels(), name) {
		return nil
	}
	restore := snapshot.DeepCopy()
	restore.SetNamespace(c.namespace)
	if apierrors.IsNotFound(err) {
		restore.SetResourceVersion("")
		_, err = resClient.Create(ctx, restore, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if !managedLabelMatches(current.GetLabels(), name) {
		return nil
	}
	restore.SetResourceVersion(current.GetResourceVersion())
	_, err = resClient.Update(ctx, restore, metav1.UpdateOptions{})
	return err
}

func (c *Client) deleteSecretIfOwned(ctx context.Context, secretName, owner string, ownedByManagedCertificate bool) (bool, error) {
	secretClient := c.clientset.CoreV1().Secrets(c.namespace)
	secret, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !ownedByManagedCertificate && !managedLabelMatches(secret.Labels, owner) {
		return false, nil
	}
	if err := secretClient.Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func (c *Client) cleanupCustomDomainResources(ctx context.Context, name string) error {
	var firstErr error
	certificateOwnsTLSSecret := false
	if cert, err := c.getDynamicResource(ctx, certificateGVR, name); err != nil {
		firstErr = err
	} else if cert != nil && managedLabelMatches(cert.GetLabels(), name) && certificateSecretNameMatches(cert, name) {
		certificateOwnsTLSSecret = true
	}
	_, _, err := c.deleteManagedDynamicResourceIfExists(ctx, certificateGVR, name)
	if err != nil {
		recordFirstErr(&firstErr, err)
	}
	if _, err := c.deleteSecretIfOwned(ctx, name, name, certificateOwnsTLSSecret); err != nil {
		recordFirstErr(&firstErr, err)
	}
	if _, _, err := c.deleteManagedDynamicResourceIfExists(ctx, issuerGVR, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	return firstErr
}

func (c *Client) deleteDeploymentIfOwned(ctx context.Context, name string) (bool, error) {
	client := c.clientset.AppsV1().Deployments(c.namespace)
	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !managedLabelMatches(resource.Labels, name) {
		return false, nil
	}
	if err := client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func (c *Client) deleteServiceIfOwned(ctx context.Context, name string) (bool, error) {
	client := c.clientset.CoreV1().Services(c.namespace)
	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !managedLabelMatches(resource.Labels, name) {
		return false, nil
	}
	if err := client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func (c *Client) deleteIngressIfOwned(ctx context.Context, ingressName, owner string) (bool, error) {
	client := c.clientset.NetworkingV1().Ingresses(c.namespace)
	resource, err := client.Get(ctx, ingressName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !managedLabelMatches(resource.Labels, owner) {
		return false, nil
	}
	if err := client.Delete(ctx, ingressName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func recordFirstErr(firstErr *error, err error) {
	if err != nil && *firstErr == nil {
		*firstErr = err
	}
}

func (c *Client) cleanupCoreResources(ctx context.Context, name string, summary *CleanupSummary) error {
	var firstErr error
	if deleted, err := c.deleteSecretIfOwned(ctx, authSecretName(name), name, false); err != nil {
		recordFirstErr(&firstErr, err)
	} else if summary != nil && deleted {
		summary.Secrets++
	}
	if deleted, err := c.deleteDeploymentIfOwned(ctx, name); err != nil {
		recordFirstErr(&firstErr, err)
	} else if summary != nil && deleted {
		summary.Deployments++
	}
	if deleted, err := c.deleteServiceIfOwned(ctx, name); err != nil {
		recordFirstErr(&firstErr, err)
	} else if summary != nil && deleted {
		summary.Services++
	}
	for _, ingressName := range []string{name, name + "-app"} {
		if deleted, err := c.deleteIngressIfOwned(ctx, ingressName, name); err != nil {
			recordFirstErr(&firstErr, err)
		} else if summary != nil && deleted {
			summary.Ingresses++
		}
	}
	return firstErr
}

// Cleanup resources
func (c *Client) Cleanup(ctx context.Context, tunnelID string) error {
	return c.CleanupTunnel(ctx, tunnelID)
}

func (c *Client) CleanupTunnel(ctx context.Context, tunnelID string) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
	name := fmt.Sprintf("sealtun-%s", tunnelID)

	var firstErr error
	if err := c.cleanupCustomDomainResources(ctx, name); err != nil {
		firstErr = err
	}
	if err := c.cleanupCoreResources(ctx, name, nil); err != nil && firstErr == nil {
		firstErr = err
	}

	return firstErr
}

func (c *Client) cleanupCreated(ctx context.Context, resources []createdResource) error {
	for i := len(resources) - 1; i >= 0; i-- {
		resource := resources[i]
		var err error
		switch resource.kind {
		case resourceDeployment:
			_, err = c.deleteDeploymentIfOwned(ctx, resource.name)
		case resourceService:
			_, err = c.deleteServiceIfOwned(ctx, resource.name)
		case resourceIngress:
			owner := strings.TrimSuffix(resource.name, "-app")
			_, err = c.deleteIngressIfOwned(ctx, resource.name, owner)
		case resourceIssuer:
			_, _, err = c.deleteManagedDynamicResourceIfExists(ctx, issuerGVR, resource.name)
		case resourceCertificate:
			_, _, err = c.deleteManagedDynamicResourceIfExists(ctx, certificateGVR, resource.name)
		case resourceSecret:
			owner := strings.TrimSuffix(resource.name, "-auth")
			_, err = c.deleteSecretIfOwned(ctx, resource.name, owner, false)
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *Client) CleanupManaged(ctx context.Context, tunnelIDs []string) (*CleanupSummary, error) {
	summary := &CleanupSummary{}
	var firstErr error

	seen := map[string]struct{}{}
	for _, tunnelID := range tunnelIDs {
		if tunnelID == "" {
			continue
		}
		if err := validateTunnelID(tunnelID); err != nil {
			recordFirstErr(&firstErr, err)
			continue
		}
		if _, ok := seen[tunnelID]; ok {
			continue
		}
		seen[tunnelID] = struct{}{}

		name := fmt.Sprintf("sealtun-%s", tunnelID)
		certificateOwnsTLSSecret := false
		if cert, err := c.getDynamicResource(ctx, certificateGVR, name); err != nil {
			recordFirstErr(&firstErr, err)
		} else if cert != nil && managedLabelMatches(cert.GetLabels(), name) && certificateSecretNameMatches(cert, name) {
			certificateOwnsTLSSecret = true
		}
		_, deleted, err := c.deleteManagedDynamicResourceIfExists(ctx, certificateGVR, name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if deleted {
			summary.Certificates++
		}
		if deleted, err := c.deleteSecretIfOwned(ctx, name, name, certificateOwnsTLSSecret); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if deleted {
			summary.Secrets++
		}
		if _, deleted, err := c.deleteManagedDynamicResourceIfExists(ctx, issuerGVR, name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if deleted {
			summary.Issuers++
		}
		if err := c.cleanupCoreResources(ctx, name, summary); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return summary, firstErr
}

func (c *Client) DiagnoseTunnel(ctx context.Context, tunnelID string) (*TunnelDiagnostics, error) {
	return c.DiagnoseTunnelWithOptions(ctx, tunnelID, TunnelOptions{})
}

func (c *Client) DiagnoseTunnelWithOptions(ctx context.Context, tunnelID string, opts TunnelOptions) (*TunnelDiagnostics, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return nil, err
	}
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
	if opts.CustomDomain != "" {
		if diag.Ingress.Exists {
			if !hostnameInList(diag.Ingress.Hosts, opts.CustomDomain) {
				diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote ingress is missing custom domain host %s", opts.CustomDomain))
			}
			if !hostnameInList(diag.Ingress.TLSHosts, opts.CustomDomain) {
				diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote ingress TLS is missing custom domain host %s", opts.CustomDomain))
			}
		}
		certDiag, err := c.certificateDiagnostics(ctx, name)
		if err != nil {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf("custom domain certificate diagnostics unavailable: %v", err))
		} else {
			diag.Certificate = certDiag
			if !certDiag.Exists {
				diag.Warnings = append(diag.Warnings, "custom domain certificate is missing")
			} else if !hostnameInList(certDiag.DNSNames, opts.CustomDomain) {
				diag.Warnings = append(diag.Warnings, fmt.Sprintf("custom domain certificate does not include DNS name %s", opts.CustomDomain))
			} else if !certDiag.Ready {
				diag.Warnings = append(diag.Warnings, "custom domain certificate is not ready")
			}
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
		clientset:     c.clientset,
		dynamicClient: c.dynamicClient,
		namespace:     namespace,
		domain:        c.domain,
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

	sum := sha256.Sum256([]byte(value))
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

func (c *Client) certificateDiagnostics(ctx context.Context, name string) (*CertificateDiagnostics, error) {
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic Kubernetes client is unavailable")
	}
	cert, err := c.dynamicClient.Resource(certificateGVR).Namespace(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return &CertificateDiagnostics{}, nil
	}
	if err != nil {
		return nil, err
	}

	diag := &CertificateDiagnostics{Exists: true}
	if secretName, ok, _ := unstructured.NestedString(cert.Object, "spec", "secretName"); ok {
		diag.SecretName = secretName
	}
	if dnsNames, ok, _ := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames"); ok {
		diag.DNSNames = dnsNames
	}
	conditions, ok, _ := unstructured.NestedSlice(cert.Object, "status", "conditions")
	if !ok {
		return diag, nil
	}
	for _, item := range conditions {
		condition, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		cond := ConditionDiagnostic{
			Type:    stringValue(condition["type"]),
			Status:  stringValue(condition["status"]),
			Reason:  stringValue(condition["reason"]),
			Message: stringValue(condition["message"]),
		}
		if cond.Type == "Ready" && cond.Status == "True" {
			diag.Ready = true
		}
		diag.Conditions = append(diag.Conditions, cond)
	}
	return diag, nil
}

func stringValue(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
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
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
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
