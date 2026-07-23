package k8s

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/auth"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/publicauth"
	"github.com/labring/sealtun/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/retry"
)

// NewClient initializes a Kubernetes client from the sealtun config
func NewClient(kubeconfigPath string, authData *auth.AuthData) (*Client, error) {
	kubeconfig, err := readKubeconfigFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	rawConfig, err := clientcmd.Load(kubeconfig)
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

func readKubeconfigFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("kubeconfig %s is not a regular file", path)
	}
	return os.ReadFile(path) // #nosec G304 -- callers pass the fixed Sealtun kubeconfig path or a validated profile path.
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
		if knownDomain := knownSealosDomainForRegion(authData.Region); knownDomain != "" {
			domain = knownDomain
		} else if u, err := url.Parse(authData.Region); err == nil {
			host := u.Host
			if strings.Contains(host, ":") {
				host = strings.Split(host, ":")[0]
			}
			switch {
			case host == "cloud.sealos.io":
				domain = "cloud.sealos.io"
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

func knownSealosDomainForRegion(regionURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(regionURL), "/")
	for _, region := range auth.KnownRegions() {
		if normalized == region.URL {
			return normalizeHostname(region.SealosDomain)
		}
	}
	return ""
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
	protocol = tunnelprotocol.Normalize(protocol)

	name := tunnelResourceName(tunnelID)
	opts.CustomDomain = normalizeHostname(opts.CustomDomain)
	opts.SealosHost = normalizeHostname(opts.SealosHost)
	if opts.SealosHost == "" {
		opts.SealosHost = c.sealosHost(name)
	}
	if err := validateSealosHost(opts.SealosHost); err != nil {
		return TunnelHosts{}, err
	}
	if err := validateBasicAuthOptions(opts.BasicAuth); err != nil {
		return TunnelHosts{}, err
	}
	if err := accesspolicy.Validate(opts.AccessPolicy); err != nil {
		return TunnelHosts{}, fmt.Errorf("invalid access policy: %w", err)
	}
	if !tunnelprotocol.IsHTTP(protocol) {
		if opts.CustomDomain != "" {
			return TunnelHosts{}, fmt.Errorf("custom domains are only supported for https tunnels")
		}
		if opts.BasicAuth != nil {
			return TunnelHosts{}, fmt.Errorf("basic auth is only supported for https tunnels")
		}
		if !accesspolicy.Empty(opts.AccessPolicy) {
			return TunnelHosts{}, fmt.Errorf("access policies are only supported for https tunnels")
		}
	} else if err := validateCustomDomainTarget(opts.CustomDomain, opts.SealosHost); err != nil {
		return TunnelHosts{}, err
	}
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

	authSecretCreated, digestSalt, err := c.ensureAuthSecret(ctx, name, secret, opts.BasicAuth, opts.AccessPolicy)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure tunnel auth secret: %w", err)
	}
	if authSecretCreated {
		created = append(created, createdResource{kind: resourceSecret, name: authSecretName(name)})
	}

	// Create or Update Deployment
	deploymentCreated, err := c.ensureDeployment(ctx, name, secret, protocol, localPort, opts.TargetURL, digestSalt, opts.BasicAuth, opts.AccessPolicy, opts.Resources)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure deployment: %w", err)
	}
	if deploymentCreated {
		created = append(created, createdResource{kind: resourceDeployment, name: name})
	}

	// Create or Update HTTP control/app Service
	serviceCreated, err := c.ensureService(ctx, name)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure service: %w", err)
	}
	if serviceCreated {
		created = append(created, createdResource{kind: resourceService, name: name})
	}

	if tunnelprotocol.UsesRawTCP(protocol) {
		tcpServiceCreated, err := c.ensureTCPService(ctx, name)
		if err != nil {
			return empty, fmt.Errorf("failed to ensure public tcp service: %w", err)
		}
		if tcpServiceCreated {
			created = append(created, createdResource{kind: resourceTCPService, name: tcpServiceName(name)})
		}
	} else {
		if _, err := c.deleteServiceIfOwned(ctx, tcpServiceName(name)); err != nil {
			return empty, fmt.Errorf("failed to remove stale tcp service: %w", err)
		}
	}

	if protocol == tunnelprotocol.HTTPS && opts.CustomDomain != "" {
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
	hosts, ingressCreated, err := c.ensureIngress(ctx, name, protocol, opts)
	if err != nil {
		return empty, fmt.Errorf("failed to ensure ingress: %w", err)
	}
	created = append(created, ingressCreated...)
	if tunnelprotocol.UsesRawTCP(protocol) {
		service, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, tcpServiceName(name), metav1.GetOptions{})
		if err != nil {
			return empty, fmt.Errorf("get public tcp service: %w", err)
		}
		hosts.PublicPort = publicTCPNodePort(service)
		if hosts.PublicPort == 0 {
			return empty, fmt.Errorf("public tcp service did not receive a nodePort")
		}
	}

	rollback = false
	return hosts, nil
}

func authSecretName(name string) string {
	return name + "-auth"
}

func tunnelResourceName(tunnelID string) string {
	return "sealtun-" + tunnelID
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

func tunnelPodLabelSelector(name string) string {
	return klabels.Set(managedLabels(name)).AsSelector().String()
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

func validateBasicAuthOptions(opts *BasicAuthOptions) error {
	if opts == nil {
		return nil
	}
	return publicauth.Validate(publicauth.BasicAuth{
		Username:     opts.Username,
		PasswordHash: opts.PasswordHash,
	})
}

// serverConfigDigest produces a change-detection digest for the server config.
// It is HMAC-keyed with a per-tunnel random salt (stored only in the auth
// Secret, never in the public pod annotation) so the annotation cannot be used
// as an offline brute-force oracle to recover the tunnel secret or auth config.
func serverConfigDigest(salt []byte, secret, targetURL string, basicAuth *BasicAuthOptions, policy *accesspolicy.Policy) string {
	parts := []string{secret, strings.TrimSpace(targetURL)}
	if basicAuth != nil {
		parts = append(parts, strings.TrimSpace(basicAuth.Username), basicAuth.PasswordHash)
	}
	if policy != nil {
		if data, err := json.Marshal(policy); err == nil {
			parts = append(parts, string(data))
		}
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *Client) ensureAuthSecret(ctx context.Context, name, secret string, basicAuth *BasicAuthOptions, policy *accesspolicy.Policy) (bool, []byte, error) {
	secretClient := c.clientset.CoreV1().Secrets(c.namespace)
	existing, getErr := secretClient.Get(ctx, authSecretName(name), metav1.GetOptions{})

	// Preserve an existing config-digest salt across updates so the digest
	// stays stable when the config is unchanged; generate a fresh one on first
	// create.
	var salt []byte
	if getErr == nil {
		salt = existing.Data[configDigestSaltKey]
	}
	if len(salt) == 0 {
		salt = make([]byte, 32)
		if _, err := rand.Read(salt); err != nil {
			return false, nil, fmt.Errorf("generate config digest salt: %w", err)
		}
	}

	data := map[string][]byte{
		tunnelAuthSecretKey: []byte(secret),
		configDigestSaltKey: salt,
	}
	if basicAuth != nil {
		data[basicAuthUserKey] = []byte(strings.TrimSpace(basicAuth.Username))
		data[basicAuthPasswordKey] = []byte(basicAuth.PasswordHash)
	}
	if !accesspolicy.Empty(policy) {
		policyJSON, err := json.Marshal(policy)
		if err != nil {
			return false, nil, fmt.Errorf("marshal access policy: %w", err)
		}
		data[accessPolicyKey] = policyJSON
	}
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      authSecretName(name),
			Namespace: c.namespace,
			Labels:    managedLabels(name),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	if apierrors.IsNotFound(getErr) {
		_, err := secretClient.Create(ctx, authSecret, metav1.CreateOptions{})
		return err == nil, salt, err
	} else if getErr == nil {
		if err := rejectUnmanagedExisting("secret", authSecret.Name, name, existing.Labels); err != nil {
			return false, nil, err
		}
		if authSecretUpToDate(authSecret, existing) {
			return false, salt, nil
		}
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := secretClient.Get(ctx, authSecret.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := rejectUnmanagedExisting("secret", authSecret.Name, name, current.Labels); err != nil {
				return err
			}
			if authSecretUpToDate(authSecret, current) {
				return nil
			}
			next := authSecret.DeepCopy()
			next.ResourceVersion = current.ResourceVersion
			_, err = secretClient.Update(ctx, next, metav1.UpdateOptions{})
			return err
		})
		return false, salt, err
	}
	return false, nil, getErr
}

func authSecretUpToDate(desired, current *corev1.Secret) bool {
	return desired != nil && current != nil &&
		desired.Type == current.Type &&
		apiequality.Semantic.DeepEqual(desired.Data, current.Data) &&
		mapContains(current.Labels, desired.Labels)
}

func (c *Client) ensureDeployment(ctx context.Context, name, secret, protocol, localPort, targetURL string, salt []byte, basicAuth *BasicAuthOptions, policy *accesspolicy.Policy, resources *ResourceConfig) (bool, error) {
	replicas := int32(1)
	labels := managedLabels(name)

	f := false
	t := true
	u := serverRunAsUserID

	args := []string{"server", "--secret-env", "SEALTUN_SECRET", "--port", strconv.Itoa(int(tunnelprotocol.ServerHTTPPort)), "--protocol", protocol, "--local-port", localPort}
	if strings.TrimSpace(targetURL) != "" {
		args = append(args, "--target-url", strings.TrimSpace(targetURL))
	}
	podAnnotations := map[string]string{}
	env := []corev1.EnvVar{
		{
			Name: "SEALTUN_SECRET",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName(name)},
					Key:                  tunnelAuthSecretKey,
				},
			},
		},
	}
	if basicAuth != nil {
		args = append(args,
			"--basic-auth-user-env", "SEALTUN_BASIC_AUTH_USER",
			"--basic-auth-password-hash-env", "SEALTUN_BASIC_AUTH_PASSWORD_HASH",
		)
		env = append(env,
			corev1.EnvVar{
				Name: "SEALTUN_BASIC_AUTH_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName(name)},
						Key:                  basicAuthUserKey,
					},
				},
			},
			corev1.EnvVar{
				Name: "SEALTUN_BASIC_AUTH_PASSWORD_HASH",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName(name)},
						Key:                  basicAuthPasswordKey,
					},
				},
			},
		)
	}
	if !accesspolicy.Empty(policy) {
		args = append(args, "--access-policy-env", "SEALTUN_ACCESS_POLICY")
		env = append(env, corev1.EnvVar{
			Name: "SEALTUN_ACCESS_POLICY",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName(name)},
					Key:                  accessPolicyKey,
				},
			},
		})
	}
	podAnnotations[serverConfigDigestKey] = serverConfigDigest(salt, secret, targetURL, basicAuth, policy)
	resourceRequirements, err := resourceRequirementsForConfig(resources)
	if err != nil {
		return false, err
	}
	serverImage, err := serverImageForVersion(version.Version)
	if err != nil {
		return false, err
	}

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
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: podAnnotations},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: &f,
					Containers: []corev1.Container{
						{
							Name:            name,
							Image:           serverImage,
							ImagePullPolicy: corev1.PullAlways,
							Args:            args,
							Env:             env,
							Ports:           containerPortsForProtocol(protocol),
							Resources:       resourceRequirements,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "tmp", MountPath: "/tmp"},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &f,
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								ReadOnlyRootFilesystem: &t,
								RunAsNonRoot:           &t,
								RunAsUser:              &u,
								RunAsGroup:             &u,
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(tunnelprotocol.ServerHTTPPort),
									},
								},
								InitialDelaySeconds: 1,
								PeriodSeconds:       2,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "tmp",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
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
		if deploymentUpToDate(deployment, existing) {
			return false, nil
		}
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := deployClient.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := rejectUnmanagedExisting("deployment", name, name, current.Labels); err != nil {
				return err
			}
			if deploymentUpToDate(deployment, current) {
				return nil
			}
			next := deployment.DeepCopy()
			next.ResourceVersion = current.ResourceVersion
			_, err = deployClient.Update(ctx, next, metav1.UpdateOptions{})
			return err
		})
	}
	return false, err
}

func deploymentUpToDate(desired, current *appsv1.Deployment) bool {
	if desired == nil || current == nil || !mapContains(current.Labels, desired.Labels) {
		return false
	}
	if !apiequality.Semantic.DeepEqual(desired.Spec.Replicas, current.Spec.Replicas) ||
		!apiequality.Semantic.DeepEqual(desired.Spec.Selector, current.Spec.Selector) ||
		!mapContains(current.Spec.Template.Labels, desired.Spec.Template.Labels) ||
		!mapContains(current.Spec.Template.Annotations, desired.Spec.Template.Annotations) {
		return false
	}
	if !apiequality.Semantic.DeepEqual(desired.Spec.Template.Spec.AutomountServiceAccountToken, current.Spec.Template.Spec.AutomountServiceAccountToken) {
		return false
	}
	for _, desiredContainer := range desired.Spec.Template.Spec.Containers {
		var currentContainer *corev1.Container
		for i := range current.Spec.Template.Spec.Containers {
			if current.Spec.Template.Spec.Containers[i].Name == desiredContainer.Name {
				currentContainer = &current.Spec.Template.Spec.Containers[i]
				break
			}
		}
		if currentContainer == nil || !managedContainerUpToDate(&desiredContainer, currentContainer) {
			return false
		}
	}
	for _, desiredVolume := range desired.Spec.Template.Spec.Volumes {
		found := false
		for i := range current.Spec.Template.Spec.Volumes {
			if current.Spec.Template.Spec.Volumes[i].Name == desiredVolume.Name {
				found = apiequality.Semantic.DeepDerivative(desiredVolume, current.Spec.Template.Spec.Volumes[i])
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func managedContainerUpToDate(desired, current *corev1.Container) bool {
	if desired == nil || current == nil {
		return false
	}
	return desired.Image == current.Image &&
		desired.ImagePullPolicy == current.ImagePullPolicy &&
		apiequality.Semantic.DeepEqual(desired.Args, current.Args) &&
		apiequality.Semantic.DeepEqual(desired.Env, current.Env) &&
		apiequality.Semantic.DeepEqual(desired.Ports, current.Ports) &&
		apiequality.Semantic.DeepEqual(desired.Resources, current.Resources) &&
		apiequality.Semantic.DeepEqual(desired.VolumeMounts, current.VolumeMounts) &&
		apiequality.Semantic.DeepDerivative(desired.SecurityContext, current.SecurityContext) &&
		apiequality.Semantic.DeepDerivative(desired.ReadinessProbe, current.ReadinessProbe)
}

func mapContains(current, desired map[string]string) bool {
	for key, value := range desired {
		if current[key] != value {
			return false
		}
	}
	return true
}

func imageTagForVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "dev" {
		return "latest"
	}
	if releaseVersionPattern.MatchString(value) {
		return strings.TrimPrefix(value, "v")
	}
	return ""
}

func serverImageForVersion(value string) (string, error) {
	if image := strings.TrimSpace(os.Getenv("SEALTUN_SERVER_IMAGE")); image != "" {
		return image, nil
	}
	tag := imageTagForVersion(value)
	if tag == "" {
		return "", fmt.Errorf("unsupported sealtun version %q for server image; set SEALTUN_SERVER_IMAGE to override", value)
	}
	return fmt.Sprintf("ghcr.io/gitlayzer/sealtun:%s", tag), nil
}

func containerPortsForProtocol(protocol string) []corev1.ContainerPort {
	ports := []corev1.ContainerPort{{Name: "http", ContainerPort: tunnelprotocol.ServerHTTPPort}}
	if tunnelprotocol.UsesRawTCP(protocol) {
		ports = append(ports, corev1.ContainerPort{Name: "tcp", ContainerPort: tunnelprotocol.ServerRawTCPPort})
	}
	return ports
}

func resourceRequirementsForConfig(config *ResourceConfig) (corev1.ResourceRequirements, error) {
	effective := EffectiveResourceConfig(config)
	requirements := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if err := setResourceQuantity(requirements.Requests, corev1.ResourceCPU, effective.Requests.CPU); err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("request cpu: %w", err)
	}
	if err := setResourceQuantity(requirements.Requests, corev1.ResourceMemory, effective.Requests.Memory); err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("request memory: %w", err)
	}
	if err := setResourceQuantity(requirements.Limits, corev1.ResourceCPU, effective.Limits.CPU); err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("limit cpu: %w", err)
	}
	if err := setResourceQuantity(requirements.Limits, corev1.ResourceMemory, effective.Limits.Memory); err != nil {
		return corev1.ResourceRequirements{}, fmt.Errorf("limit memory: %w", err)
	}
	return requirements, nil
}

func setResourceQuantity(list corev1.ResourceList, name corev1.ResourceName, value string) error {
	quantity, err := resource.ParseQuantity(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	list[name] = quantity
	return nil
}

// UpdateTunnelResources patches the remote Deployment pod template resources
// without changing the current replica count. This lets stopped tunnels keep
// their public routing resources while updating the template used on next start.
func (c *Client) UpdateTunnelResources(ctx context.Context, tunnelID string, resources *ResourceConfig) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
	name := tunnelResourceName(tunnelID)
	requirements, err := resourceRequirementsForConfig(resources)
	if err != nil {
		return err
	}

	deployClient := c.clientset.AppsV1().Deployments(c.namespace)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := deployClient.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := rejectUnmanagedExisting("deployment", name, name, current.Labels); err != nil {
			return err
		}
		if len(current.Spec.Template.Spec.Containers) == 0 {
			return fmt.Errorf("deployment %s has no containers", name)
		}
		if apiequality.Semantic.DeepEqual(current.Spec.Template.Spec.Containers[0].Resources, requirements) {
			return nil
		}
		next := current.DeepCopy()
		next.Spec.Template.Spec.Containers[0].Resources = requirements
		_, err = deployClient.Update(ctx, next, metav1.UpdateOptions{})
		return err
	}); err != nil {
		return fmt.Errorf("update deployment %s resources: %w", name, err)
	}
	return nil
}

func tcpServiceName(name string) string {
	return name + "-tcp"
}

func (c *Client) ensureService(ctx context.Context, name string) (bool, error) {
	labels := managedLabels(name)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: httpServiceSpec(labels),
	}

	return c.applyService(ctx, service, name)
}

func (c *Client) ensureTCPService(ctx context.Context, name string) (bool, error) {
	labels := managedLabels(name)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tcpServiceName(name),
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: tcpServiceSpec(labels),
	}

	return c.applyService(ctx, service, name)
}

func (c *Client) applyService(ctx context.Context, service *corev1.Service, owner string) (bool, error) {
	svcClient := c.clientset.CoreV1().Services(c.namespace)
	existing, err := svcClient.Get(ctx, service.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = svcClient.Create(ctx, service, metav1.CreateOptions{})
		return err == nil, err
	} else if err == nil {
		if err := rejectUnmanagedExisting("service", service.Name, owner, existing.Labels); err != nil {
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
		preserveExistingNodePorts(&service.Spec, existing.Spec)
		if serviceUpToDate(service, existing) {
			return false, nil
		}
		_, err = svcClient.Update(ctx, service, metav1.UpdateOptions{})
	}
	return false, err
}

func serviceUpToDate(desired, current *corev1.Service) bool {
	if desired == nil || current == nil || !mapContains(current.Labels, desired.Labels) {
		return false
	}
	if desired.Spec.Type != current.Spec.Type || !apiequality.Semantic.DeepEqual(desired.Spec.Selector, current.Spec.Selector) {
		return false
	}
	if len(desired.Spec.Ports) != len(current.Spec.Ports) {
		return false
	}
	for i := range desired.Spec.Ports {
		want := desired.Spec.Ports[i]
		got := current.Spec.Ports[i]
		if want.Protocol == "" {
			want.Protocol = corev1.ProtocolTCP
		}
		if got.Protocol == "" {
			got.Protocol = corev1.ProtocolTCP
		}
		if !apiequality.Semantic.DeepEqual(want, got) {
			return false
		}
	}
	return true
}

func httpServiceSpec(labels map[string]string) corev1.ServiceSpec {
	return corev1.ServiceSpec{
		Type:     corev1.ServiceTypeClusterIP,
		Selector: labels,
		Ports: []corev1.ServicePort{
			{Name: "http", Port: 80, TargetPort: intstr.FromInt32(tunnelprotocol.ServerHTTPPort)},
		},
	}
}

func tcpServiceSpec(labels map[string]string) corev1.ServiceSpec {
	return corev1.ServiceSpec{
		Type:     corev1.ServiceTypeNodePort,
		Selector: labels,
		Ports: []corev1.ServicePort{
			{
				Name:       "tcp",
				Port:       tunnelprotocol.ServerRawTCPPort,
				TargetPort: intstr.FromInt32(tunnelprotocol.ServerRawTCPPort),
				Protocol:   corev1.ProtocolTCP,
			},
		},
	}
}

func preserveExistingNodePorts(next *corev1.ServiceSpec, existing corev1.ServiceSpec) {
	if next == nil {
		return
	}
	existingByName := map[string]int32{}
	for _, port := range existing.Ports {
		if port.NodePort != 0 {
			existingByName[port.Name] = port.NodePort
		}
	}
	for i := range next.Ports {
		if nodePort := existingByName[next.Ports[i].Name]; nodePort != 0 {
			next.Ports[i].NodePort = nodePort
		}
	}
}

func publicTCPNodePort(service *corev1.Service) int32 {
	if service == nil {
		return 0
	}
	for _, port := range service.Spec.Ports {
		if port.Name == "tcp" {
			return port.NodePort
		}
	}
	return 0
}

func (c *Client) ensureIngress(ctx context.Context, name, protocol string, opts TunnelOptions) (TunnelHosts, []createdResource, error) {
	sealosHost := normalizeHostname(opts.SealosHost)
	if sealosHost == "" {
		sealosHost = c.sealosHost(name)
	}
	return c.ensureIngressForHost(ctx, name, sealosHost, protocol, opts)
}

func (c *Client) ensureIngressForHost(ctx context.Context, name, sealosHost, protocol string, opts TunnelOptions) (TunnelHosts, []createdResource, error) {
	protocol = tunnelprotocol.Normalize(protocol)
	sealosHost = normalizeHostname(sealosHost)
	opts.CustomDomain = normalizeHostname(opts.CustomDomain)
	if err := validateSealosHost(sealosHost); err != nil {
		return TunnelHosts{}, nil, err
	}
	switch protocol {
	case tunnelprotocol.SSH, tunnelprotocol.TCP:
		if opts.CustomDomain != "" {
			return TunnelHosts{}, nil, fmt.Errorf("custom domains are only supported for https tunnels")
		}
	case tunnelprotocol.HTTPS:
		if err := validateCustomDomainTarget(opts.CustomDomain, sealosHost); err != nil {
			return TunnelHosts{}, nil, err
		}
	default:
		return TunnelHosts{}, nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
	publicHost := sealosHost
	if protocol == tunnelprotocol.HTTPS && opts.CustomDomain != "" {
		publicHost = opts.CustomDomain
	}
	pathType := netv1.PathTypePrefix
	ingressClass := "nginx"
	paths := []string{"/_sealtun/ws", "/_sealtun/healthz", "/_sealtun/metrics", "/_sealtun/audit"}
	if tunnelprotocol.UsesRawTCP(protocol) {
		paths = append(paths, "/_sealtun/tcp")
	}
	if protocol == tunnelprotocol.HTTPS {
		paths = append(paths, "/")
	}
	ingress := c.generateIngress(name, sealosHost, opts.CustomDomain, paths, &pathType, &ingressClass)
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
	return c.sealosHost(tunnelResourceName(tunnelID))
}

func (c *Client) TunnelPublicPort(ctx context.Context, tunnelID string) (int32, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return 0, err
	}
	name := tunnelResourceName(tunnelID)
	service, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, tcpServiceName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return 0, fmt.Errorf("remote service %s is missing", tcpServiceName(name))
	}
	if err != nil {
		return 0, fmt.Errorf("get service %s: %w", tcpServiceName(name), err)
	}
	if err := rejectUnmanagedExisting("service", tcpServiceName(name), name, service.Labels); err != nil {
		return 0, err
	}
	port := publicTCPNodePort(service)
	if port == 0 {
		return 0, fmt.Errorf("service %s has no public tcp nodePort", tcpServiceName(name))
	}
	return port, nil
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

func validateSealosHost(value string) error {
	if value == "" {
		return fmt.Errorf("sealos CNAME target is required")
	}
	if err := validateCustomDomainHostname(value); err != nil {
		return fmt.Errorf("invalid Sealos CNAME target: %w", err)
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
		if ingressUpToDate(ingress, existing) {
			return false, nil
		}
		_, err = ingClient.Update(ctx, ingress, metav1.UpdateOptions{})
	}
	return false, err
}

func ingressUpToDate(desired, current *netv1.Ingress) bool {
	return desired != nil && current != nil &&
		mapContains(current.Labels, desired.Labels) &&
		mapContains(current.Annotations, desired.Annotations) &&
		apiequality.Semantic.DeepEqual(desired.Spec, current.Spec)
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
	issuerCreated, err := c.applyDynamicResource(ctx, issuerGVR, c.customDomainIssuer(name))
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

// acmeContactEmail derives a best-effort ACME registration contact from the
// authenticated tenant namespace, so certificate expiry/policy notices are not
// funneled into a single shared mailbox across all tenants. Falls back to a
// generic per-namespace address when the namespace is unknown.
func (c *Client) acmeContactEmail() string {
	ns := strings.TrimSpace(c.namespace)
	if ns == "" {
		ns = "tenant"
	}
	domain := strings.TrimSpace(c.domain)
	if domain == "" {
		domain = "sealos.io"
	}
	return fmt.Sprintf("%s@%s", ns, domain)
}

func (c *Client) customDomainIssuer(name string) *unstructured.Unstructured {
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
				"email":  c.acmeContactEmail(),
				"privateKeySecretRef": map[string]interface{}{
					// Per-tunnel ACME account key so tenants do not share a
					// single ACME account/registration.
					"name": "letsencrypt-prod-" + name,
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
	if secretOwnedByManagedCertificate(secret, name, cert) {
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
	name := tunnelResourceName(tunnelID)
	customDomain = normalizeHostname(customDomain)
	if sealosHost == "" {
		sealosHost = c.sealosHost(name)
	}
	if err := validateSealosHost(sealosHost); err != nil {
		return TunnelHosts{}, err
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

	hosts, ingressCreated, err := c.ensureIngressForHost(ctx, name, sealosHost, tunnelprotocol.HTTPS, TunnelOptions{CustomDomain: customDomain})
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
	name := tunnelResourceName(tunnelID)
	if sealosHost == "" {
		sealosHost = c.sealosHost(name)
	}
	if err := validateSealosHost(sealosHost); err != nil {
		return TunnelHosts{}, err
	}
	if err := c.validateTunnelCoreResources(ctx, name); err != nil {
		return TunnelHosts{}, err
	}
	hosts, _, err := c.ensureIngressForHost(ctx, name, sealosHost, tunnelprotocol.HTTPS, TunnelOptions{})
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
	sanitizeDynamicResourceForApply(restore)
	restore.SetNamespace(c.namespace)
	if apierrors.IsNotFound(err) {
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

func sanitizeDynamicResourceForApply(resource *unstructured.Unstructured) {
	if resource == nil {
		return
	}
	resource.SetUID("")
	resource.SetResourceVersion("")
	resource.SetGeneration(0)
	resource.SetCreationTimestamp(metav1.Time{})
	resource.SetManagedFields(nil)
	delete(resource.Object, "status")
}

func (c *Client) deleteSecretIfOwned(ctx context.Context, secretName, owner string, cert *unstructured.Unstructured) (bool, error) {
	secretClient := c.clientset.CoreV1().Secrets(c.namespace)
	secret, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !managedLabelMatches(secret.Labels, owner) && !secretOwnedByManagedCertificate(secret, owner, cert) {
		return false, nil
	}
	if err := secretClient.Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func secretOwnedByManagedCertificate(secret *corev1.Secret, owner string, cert *unstructured.Unstructured) bool {
	if secret == nil || cert == nil {
		return false
	}
	if !managedLabelMatches(cert.GetLabels(), owner) || !certificateSecretNameMatches(cert, secret.Name) {
		return false
	}
	if secret.Annotations["cert-manager.io/certificate-name"] == owner {
		return true
	}
	for _, ref := range secret.OwnerReferences {
		if ref.Name == owner && ref.Kind == "Certificate" && strings.HasPrefix(ref.APIVersion, "cert-manager.io/") {
			return true
		}
	}
	return false
}

func (c *Client) cleanupCustomDomainResources(ctx context.Context, name string) error {
	var firstErr error
	var cert *unstructured.Unstructured
	var issuer *unstructured.Unstructured
	var err error
	if cert, err = c.getDynamicResource(ctx, certificateGVR, name); err != nil {
		firstErr = err
	}
	if issuer, err = c.getDynamicResource(ctx, issuerGVR, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	_, _, err = c.deleteManagedDynamicResourceIfExists(ctx, certificateGVR, name)
	if err != nil {
		recordFirstErr(&firstErr, err)
	}
	if _, err := c.deleteSecretIfOwned(ctx, name, name, cert); err != nil {
		recordFirstErr(&firstErr, err)
	}
	if _, _, err := c.deleteManagedDynamicResourceIfExists(ctx, issuerGVR, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	if err := c.deleteACMEKeySecret(ctx, name, issuer); err != nil {
		recordFirstErr(&firstErr, err)
	}
	return firstErr
}

func (c *Client) deleteACMEKeySecret(ctx context.Context, name string, issuer *unstructured.Unstructured) error {
	if issuer == nil || !managedLabelMatches(issuer.GetLabels(), name) {
		return nil
	}
	keyName, ok, err := unstructured.NestedString(issuer.Object, "spec", "acme", "privateKeySecretRef", "name")
	if err != nil || !ok || keyName == "" {
		return err
	}
	secretClient := c.clientset.CoreV1().Secrets(c.namespace)
	secret, err := secretClient.Get(ctx, keyName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !managedLabelMatches(secret.Labels, name) && !secretOwnedByManagedIssuer(secret, name) {
		return nil
	}
	if err := secretClient.Delete(ctx, keyName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func secretOwnedByManagedIssuer(secret *corev1.Secret, issuerName string) bool {
	if secret == nil {
		return false
	}
	for _, ref := range secret.OwnerReferences {
		if ref.Name == issuerName && ref.Kind == "Issuer" && strings.HasPrefix(ref.APIVersion, "cert-manager.io/") {
			return true
		}
	}
	return false
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
	owner := strings.TrimSuffix(name, "-tcp")
	return c.deleteNamedServiceIfOwned(ctx, name, owner)
}

func (c *Client) deleteNamedServiceIfOwned(ctx context.Context, name, owner string) (bool, error) {
	client := c.clientset.CoreV1().Services(c.namespace)
	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !managedLabelMatches(resource.Labels, owner) {
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
	if deleted, err := c.deleteSecretIfOwned(ctx, authSecretName(name), name, nil); err != nil {
		recordFirstErr(&firstErr, err)
	} else if summary != nil && deleted {
		summary.Secrets++
	}
	if deleted, err := c.deleteDeploymentIfOwned(ctx, name); err != nil {
		recordFirstErr(&firstErr, err)
	} else if summary != nil && deleted {
		summary.Deployments++
	}
	if deleted, err := c.deleteNamedServiceIfOwned(ctx, name, name); err != nil {
		recordFirstErr(&firstErr, err)
	} else if summary != nil && deleted {
		summary.Services++
	}
	if deleted, err := c.deleteNamedServiceIfOwned(ctx, tcpServiceName(name), name); err != nil {
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

func (c *Client) PauseTunnel(ctx context.Context, tunnelID string) error {
	return c.ScaleTunnel(ctx, tunnelID, 0)
}

func (c *Client) ResumeTunnel(ctx context.Context, tunnelID string) error {
	return c.ScaleTunnel(ctx, tunnelID, 1)
}

func (c *Client) ScaleTunnel(ctx context.Context, tunnelID string, replicas int32) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
	if replicas < 0 {
		return fmt.Errorf("replicas must be >= 0")
	}
	name := tunnelResourceName(tunnelID)

	deployClient := c.clientset.AppsV1().Deployments(c.namespace)
	existing, err := deployClient.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("remote deployment %s is missing", name)
	}
	if err != nil {
		return fmt.Errorf("get deployment %s: %w", name, err)
	}
	if err := rejectUnmanagedExisting("deployment", name, name, existing.Labels); err != nil {
		return err
	}
	if deploymentReplicaCount(existing) == replicas {
		return nil
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := deployClient.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := rejectUnmanagedExisting("deployment", name, name, current.Labels); err != nil {
			return err
		}
		if deploymentReplicaCount(current) == replicas {
			return nil
		}
		next := current.DeepCopy()
		next.Spec.Replicas = &replicas
		_, err = deployClient.Update(ctx, next, metav1.UpdateOptions{})
		return err
	}); err != nil {
		return fmt.Errorf("scale deployment %s to %d: %w", name, replicas, err)
	}
	return nil
}

func deploymentReplicaCount(deployment *appsv1.Deployment) int32 {
	if deployment == nil || deployment.Spec.Replicas == nil {
		return 1
	}
	return *deployment.Spec.Replicas
}

func (c *Client) CleanupTunnel(ctx context.Context, tunnelID string) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
	name := tunnelResourceName(tunnelID)

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
			_, err = c.deleteNamedServiceIfOwned(ctx, resource.name, resource.name)
		case resourceTCPService:
			owner := strings.TrimSuffix(resource.name, "-tcp")
			_, err = c.deleteNamedServiceIfOwned(ctx, resource.name, owner)
		case resourceIngress:
			owner := strings.TrimSuffix(resource.name, "-app")
			_, err = c.deleteIngressIfOwned(ctx, resource.name, owner)
		case resourceIssuer:
			_, _, err = c.deleteManagedDynamicResourceIfExists(ctx, issuerGVR, resource.name)
		case resourceCertificate:
			_, _, err = c.deleteManagedDynamicResourceIfExists(ctx, certificateGVR, resource.name)
		case resourceSecret:
			owner := strings.TrimSuffix(resource.name, "-auth")
			_, err = c.deleteSecretIfOwned(ctx, resource.name, owner, nil)
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
	var err error

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

		name := tunnelResourceName(tunnelID)
		var cert *unstructured.Unstructured
		var issuer *unstructured.Unstructured
		if cert, err = c.getDynamicResource(ctx, certificateGVR, name); err != nil {
			recordFirstErr(&firstErr, err)
		}
		if issuer, err = c.getDynamicResource(ctx, issuerGVR, name); err != nil {
			recordFirstErr(&firstErr, err)
		}
		_, deleted, err := c.deleteManagedDynamicResourceIfExists(ctx, certificateGVR, name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else if deleted {
			summary.Certificates++
		}
		if deleted, err := c.deleteSecretIfOwned(ctx, name, name, cert); err != nil {
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
		if err := c.deleteACMEKeySecret(ctx, name, issuer); err != nil && firstErr == nil {
			firstErr = err
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

func (c *Client) TunnelRemoteState(ctx context.Context, tunnelID string) (*TunnelRemoteState, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return nil, err
	}
	name := tunnelResourceName(tunnelID)
	state := &TunnelRemoteState{}
	if err := c.fillTunnelRemoteStateFromDeployment(ctx, name, state); err != nil {
		return nil, err
	}

	probeServices := true
	probeIngress := true
	switch tunnelprotocol.Normalize(state.Protocol) {
	case tunnelprotocol.HTTPS:
		probeServices = false
	case tunnelprotocol.SSH, tunnelprotocol.TCP:
		probeIngress = false
	}
	if probeServices {
		if port, err := c.tunnelPublicPortFromServices(ctx, name); err != nil {
			return nil, err
		} else {
			state.PublicPort = port
		}
	}

	if probeIngress {
		ingress, err := c.clientset.NetworkingV1().Ingresses(c.namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			diag := ingressDiagnostics(ingress)
			state.SealosHost = inferSealosHost(diag.Hosts)
			state.CustomDomain = inferCustomDomain(diag.Hosts, state.SealosHost)
		case !apierrors.IsNotFound(err):
			return nil, fmt.Errorf("get ingress %s: %w", name, err)
		}
	}

	if err := c.fillTunnelRemoteStateFromAuthSecret(ctx, name, state); err != nil {
		return nil, err
	}

	if state.SealosHost == "" && tunnelprotocol.IsHTTP(state.Protocol) {
		state.SealosHost = c.sealosHost(name)
	}
	if state.CustomDomain != "" {
		state.PublicHost = state.CustomDomain
	} else if state.SealosHost != "" {
		state.PublicHost = state.SealosHost
	}
	return state, nil
}

func (c *Client) tunnelPublicPortFromServices(ctx context.Context, name string) (int32, error) {
	serviceNames := []string{name, tcpServiceName(name)}
	var port int32
	for _, serviceName := range serviceNames {
		service, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, serviceName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("get service %s: %w", serviceName, err)
		}
		for _, item := range service.Spec.Ports {
			if item.NodePort > 0 {
				port = item.NodePort
				break
			}
		}
		if port != 0 {
			return port, nil
		}
	}
	return 0, nil
}

func (c *Client) fillTunnelRemoteStateFromAuthSecret(ctx context.Context, name string, state *TunnelRemoteState) error {
	secret, err := c.clientset.CoreV1().Secrets(c.namespace).Get(ctx, authSecretName(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get auth secret %s: %w", authSecretName(name), err)
	}
	state.AuthSecretOK = true
	state.Secret = strings.TrimSpace(string(secret.Data[tunnelAuthSecretKey]))
	username := strings.TrimSpace(string(secret.Data[basicAuthUserKey]))
	passwordHash := strings.TrimSpace(string(secret.Data[basicAuthPasswordKey]))
	if username != "" || passwordHash != "" {
		state.BasicAuth = &BasicAuthOptions{
			Username:     username,
			PasswordHash: passwordHash,
		}
	}
	if raw := secret.Data[accessPolicyKey]; len(raw) > 0 {
		var policy accesspolicy.Policy
		if err := json.Unmarshal(raw, &policy); err != nil {
			return fmt.Errorf("parse auth secret access policy for tunnel %s: %w", name, err)
		}
		state.AccessPolicy = &policy
	}
	return nil
}

func (c *Client) fillTunnelRemoteStateFromDeployment(ctx context.Context, name string, state *TunnelRemoteState) error {
	deployment, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deployment %s: %w", name, err)
	}
	state.DeploymentOK = true
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		return nil
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	state.Protocol = deploymentArgValue(container.Args, "--protocol")
	state.LocalPort = deploymentArgValue(container.Args, "--local-port")
	state.TargetURL = deploymentArgValue(container.Args, "--target-url")
	state.Resources = resourceConfigFromRequirements(container.Resources)
	return nil
}

func deploymentArgValue(args []string, flag string) string {
	for i := 0; i < len(args); i++ {
		if args[i] != flag {
			continue
		}
		if i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		return ""
	}
	return ""
}

func resourceConfigFromRequirements(requirements corev1.ResourceRequirements) *ResourceConfig {
	config := &ResourceConfig{
		Requests: ResourceValues{
			CPU:    quantityString(requirements.Requests, corev1.ResourceCPU),
			Memory: quantityString(requirements.Requests, corev1.ResourceMemory),
		},
		Limits: ResourceValues{
			CPU:    quantityString(requirements.Limits, corev1.ResourceCPU),
			Memory: quantityString(requirements.Limits, corev1.ResourceMemory),
		},
	}
	if config.Requests.CPU == "" && config.Requests.Memory == "" && config.Limits.CPU == "" && config.Limits.Memory == "" {
		return nil
	}
	return config
}

func quantityString(list corev1.ResourceList, name corev1.ResourceName) string {
	if list == nil {
		return ""
	}
	quantity, ok := list[name]
	if !ok {
		return ""
	}
	return quantity.String()
}

func (c *Client) TunnelResources(ctx context.Context, tunnelID string) (*TunnelResourceList, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return nil, err
	}
	name := tunnelResourceName(tunnelID)
	out := &TunnelResourceList{
		Namespace: c.namespace,
		TunnelID:  tunnelID,
	}

	deployment, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		out.Resources = append(out.Resources, missingTunnelResource("Deployment", name, c.namespace, "deployment is missing"))
	} else if err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", name, err)
	} else {
		resource := objectTunnelResource("Deployment", deployment.Name, deployment.Namespace, deployment.Labels, deployment.CreationTimestamp.Time, managedLabelMatches(deployment.Labels, name))
		desired := int32(1)
		if deployment.Spec.Replicas != nil {
			desired = *deployment.Spec.Replicas
		}
		resource.Status = fmt.Sprintf("%d/%d ready", deployment.Status.ReadyReplicas, desired)
		resource.CostHints = append(resource.CostHints, fmt.Sprintf("deployment desired replicas: %d", desired))
		resource.CostHints = append(resource.CostHints, resourceHintsForRequirements(deployment.Spec.Template.Spec.Containers)...)
		if deployment.Status.ReadyReplicas == 0 && desired > 0 {
			resource.Warnings = append(resource.Warnings, "deployment has no ready replicas")
		}
		out.Resources = append(out.Resources, resource)
	}

	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{LabelSelector: tunnelPodLabelSelector(name)})
	if err != nil {
		return nil, fmt.Errorf("list pods for %s: %w", name, err)
	}
	if len(pods.Items) == 0 {
		out.Resources = append(out.Resources, missingTunnelResource("Pod", name, c.namespace, "no managed pods found"))
	} else {
		for i := range pods.Items {
			pod := &pods.Items[i]
			resource := objectTunnelResource("Pod", pod.Name, pod.Namespace, pod.Labels, pod.CreationTimestamp.Time, managedLabelMatches(pod.Labels, name))
			resource.Status = string(pod.Status.Phase)
			if podReady(pod) {
				resource.Status += " ready"
			} else {
				resource.Warnings = append(resource.Warnings, "pod is not ready")
			}
			resource.CostHints = append(resource.CostHints, "pod count: 1")
			out.Resources = append(out.Resources, resource)
		}
	}

	for _, item := range []struct {
		kind string
		name string
	}{
		{kind: "HTTP Service", name: name},
		{kind: "TCP NodePort Service", name: tcpServiceName(name)},
	} {
		service, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, item.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			out.Resources = append(out.Resources, optionalMissingTunnelResource(item.kind, item.name, c.namespace, "service not found for this tunnel"))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get service %s: %w", item.name, err)
		}
		resource := objectTunnelResource(item.kind, service.Name, service.Namespace, service.Labels, service.CreationTimestamp.Time, managedLabelMatches(service.Labels, name))
		resource.Status = string(service.Spec.Type)
		resource.CostHints = append(resource.CostHints, fmt.Sprintf("service type: %s", service.Spec.Type))
		for _, port := range service.Spec.Ports {
			if port.NodePort != 0 {
				resource.CostHints = append(resource.CostHints, fmt.Sprintf("nodePort %s: %d", port.Name, port.NodePort))
			}
		}
		out.Resources = append(out.Resources, resource)
	}

	ingress, err := c.clientset.NetworkingV1().Ingresses(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		out.Resources = append(out.Resources, missingTunnelResource("Ingress", name, c.namespace, "ingress is missing"))
	} else if err != nil {
		return nil, fmt.Errorf("get ingress %s: %w", name, err)
	} else {
		resource := objectTunnelResource("Ingress", ingress.Name, ingress.Namespace, ingress.Labels, ingress.CreationTimestamp.Time, managedLabelMatches(ingress.Labels, name))
		resource.Status = fmt.Sprintf("%d host(s)", len(ingress.Spec.Rules))
		resource.CostHints = append(resource.CostHints, fmt.Sprintf("ingress host count: %d", len(ingress.Spec.Rules)))
		if len(ingress.Spec.Rules) == 0 {
			resource.Warnings = append(resource.Warnings, "ingress has no hosts")
		}
		out.Resources = append(out.Resources, resource)
	}

	out.Resources = append(out.Resources, c.dynamicTunnelResource(ctx, "Certificate", certificateGVR, name, true)...)
	out.Resources = append(out.Resources, c.dynamicTunnelResource(ctx, "Issuer", issuerGVR, name, true)...)

	for _, secretName := range []string{name, authSecretName(name)} {
		secret, err := c.clientset.CoreV1().Secrets(c.namespace).Get(ctx, secretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			out.Resources = append(out.Resources, optionalMissingTunnelResource("Secret", secretName, c.namespace, "secret not found for this tunnel"))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get secret %s: %w", secretName, err)
		}
		resource := objectTunnelResource("Secret", secret.Name, secret.Namespace, secret.Labels, secret.CreationTimestamp.Time, managedLabelMatches(secret.Labels, name))
		resource.Status = string(secret.Type)
		resource.CostHints = append(resource.CostHints, "secret data hidden")
		out.Resources = append(out.Resources, resource)
	}

	for _, resource := range out.Resources {
		if len(resource.Warnings) > 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s %s: %s", resource.Kind, resource.Name, strings.Join(resource.Warnings, "; ")))
		}
	}
	return out, nil
}

func missingTunnelResource(kind, name, namespace, warning string) TunnelResource {
	return TunnelResource{
		Kind:      kind,
		Name:      name,
		Status:    "missing",
		Namespace: namespace,
		Warnings:  []string{warning},
	}
}

func optionalMissingTunnelResource(kind, name, namespace, status string) TunnelResource {
	return TunnelResource{
		Kind:      kind,
		Name:      name,
		Status:    status,
		Namespace: namespace,
	}
}

func objectTunnelResource(kind, name, namespace string, labels map[string]string, created time.Time, managed bool) TunnelResource {
	return TunnelResource{
		Kind:      kind,
		Name:      name,
		Status:    "exists",
		Age:       resourceAge(created),
		Namespace: namespace,
		Managed:   managed,
		Labels:    copyStringMap(labels),
	}
}

func resourceHintsForRequirements(containers []corev1.Container) []string {
	if len(containers) == 0 {
		return nil
	}
	requirements := containers[0].Resources
	hints := []string{}
	if cpu, ok := requirements.Requests[corev1.ResourceCPU]; ok {
		hints = append(hints, "request cpu: "+cpu.String())
	}
	if memory, ok := requirements.Requests[corev1.ResourceMemory]; ok {
		hints = append(hints, "request memory: "+memory.String())
	}
	if cpu, ok := requirements.Limits[corev1.ResourceCPU]; ok {
		hints = append(hints, "limit cpu: "+cpu.String())
	}
	if memory, ok := requirements.Limits[corev1.ResourceMemory]; ok {
		hints = append(hints, "limit memory: "+memory.String())
	}
	return hints
}

func (c *Client) dynamicTunnelResource(ctx context.Context, kind string, gvr schema.GroupVersionResource, name string, optional bool) []TunnelResource {
	resource, err := c.getDynamicResource(ctx, gvr, name)
	if err != nil {
		return []TunnelResource{missingTunnelResource(kind, name, c.namespace, fmt.Sprintf("%s diagnostics unavailable: %v", strings.ToLower(kind), err))}
	}
	if resource == nil {
		if optional {
			return []TunnelResource{optionalMissingTunnelResource(kind, name, c.namespace, strings.ToLower(kind)+" not found for this tunnel")}
		}
		return []TunnelResource{missingTunnelResource(kind, name, c.namespace, strings.ToLower(kind)+" is missing")}
	}
	item := objectTunnelResource(kind, resource.GetName(), resource.GetNamespace(), resource.GetLabels(), resource.GetCreationTimestamp().Time, managedLabelMatches(resource.GetLabels(), name))
	switch kind {
	case "Certificate":
		item.Status = "exists"
		if ready, ok := dynamicReadyCondition(resource); ok {
			if ready {
				item.Status = "ready"
			} else {
				item.Status = "not ready"
				item.Warnings = append(item.Warnings, "certificate is not ready")
			}
		}
		item.CostHints = append(item.CostHints, "certificate exists: yes")
	case "Issuer":
		item.CostHints = append(item.CostHints, "issuer exists: yes")
	}
	return []TunnelResource{item}
}

func dynamicReadyCondition(resource *unstructured.Unstructured) (bool, bool) {
	conditions, ok, _ := unstructured.NestedSlice(resource.Object, "status", "conditions")
	if !ok {
		return false, false
	}
	for _, item := range conditions {
		condition, ok := item.(map[string]interface{})
		if !ok || stringValue(condition["type"]) != "Ready" {
			continue
		}
		return stringValue(condition["status"]) == "True", true
	}
	return false, false
}

func resourceAge(created time.Time) string {
	if created.IsZero() {
		return ""
	}
	elapsed := time.Since(created)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (c *Client) StreamTunnelLogs(ctx context.Context, tunnelID string, out io.Writer, opts TunnelLogOptions) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
	if out == nil {
		return fmt.Errorf("log output writer is required")
	}
	name := tunnelResourceName(tunnelID)
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: tunnelPodLabelSelector(name),
	})
	if err != nil {
		return fmt.Errorf("list tunnel pods for %s: %w", tunnelID, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no tunnel pods found for %s", tunnelID)
	}

	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].CreationTimestamp.Time.After(pods.Items[j].CreationTimestamp.Time)
	})
	pod := pods.Items[0]
	logOpts := tunnelPodLogOptions(name, opts)

	stream, err := c.clientset.CoreV1().Pods(c.namespace).GetLogs(pod.Name, logOpts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("open logs for pod %s: %w", pod.Name, err)
	}
	defer stream.Close()

	if _, err := fmt.Fprintf(out, "# pod=%s container=%s namespace=%s\n", pod.Name, name, c.namespace); err != nil {
		return err
	}
	_, err = io.Copy(out, stream)
	return err
}

func tunnelPodLogOptions(container string, opts TunnelLogOptions) *corev1.PodLogOptions {
	logOpts := &corev1.PodLogOptions{
		Container: container,
		Follow:    opts.Follow,
	}
	if opts.TailLines >= 0 {
		tailLines := opts.TailLines
		logOpts.TailLines = &tailLines
	}
	if opts.SinceSeconds > 0 {
		sinceSeconds := opts.SinceSeconds
		logOpts.SinceSeconds = &sinceSeconds
	}
	return logOpts
}

func (c *Client) DiagnoseTunnelWithOptions(ctx context.Context, tunnelID string, opts TunnelOptions) (*TunnelDiagnostics, error) {
	if err := validateTunnelID(tunnelID); err != nil {
		return nil, err
	}
	name := tunnelResourceName(tunnelID)
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
	if opts.SealosHost != "" && diag.Ingress.Exists {
		if !hostnameInList(diag.Ingress.Hosts, opts.SealosHost) {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote ingress is missing Sealos CNAME host %s", opts.SealosHost))
		}
		if !hostnameInList(diag.Ingress.TLSHosts, opts.SealosHost) {
			diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote ingress TLS is missing Sealos CNAME host %s", opts.SealosHost))
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
	eventObjectNames := []string{name}
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: tunnelPodLabelSelector(name),
	})
	if err != nil {
		diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote pods unavailable: %v", err))
	} else {
		if len(pods.Items) == 0 {
			diag.Warnings = append(diag.Warnings, "remote tunnel pod is missing")
		}
		for i := range pods.Items {
			podDiag := podDiagnostics(&pods.Items[i])
			diag.Pods = append(diag.Pods, podDiag)
			eventObjectNames = append(eventObjectNames, podDiag.Name)
			if !podDiag.Ready {
				diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote pod %s is not ready", podDiag.Name))
			}
		}
	}

	// Bound the event fetch so a busy shared namespace cannot force us to pull
	// thousands of events (memory pressure, slow diagnostics). We filter
	// client-side by involved object name afterwards; the cap is generous
	// enough to still surface the relevant recent events for this tunnel.
	events, err := c.clientset.CoreV1().Events(c.namespace).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		diag.Warnings = append(diag.Warnings, fmt.Sprintf("remote events unavailable: %v", err))
		return diag, nil
	}
	diag.Events = filterEventDiagnostics(events.Items, eventObjectNames, 8)

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
	value = compactDNSLabelPattern.ReplaceAllString(value, "-")
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

func inferSealosHost(hosts []string) string {
	for _, host := range hosts {
		if managedDomainSuffix(host) != "" {
			return normalizeHostname(host)
		}
	}
	return ""
}

func inferCustomDomain(hosts []string, sealosHost string) string {
	sealosHost = normalizeHostname(sealosHost)
	for _, host := range hosts {
		normalized := normalizeHostname(host)
		if normalized == "" || normalized == sealosHost {
			continue
		}
		return normalized
	}
	return ""
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

func filterEventDiagnostics(events []corev1.Event, names []string, limit int) []EventDiagnostic {
	allowedNames := map[string]struct{}{}
	for _, name := range names {
		if name != "" {
			allowedNames[name] = struct{}{}
		}
	}
	result := []EventDiagnostic{}
	sort.SliceStable(events, func(i, j int) bool {
		return eventLastTimestamp(events[i]).After(eventLastTimestamp(events[j]))
	})
	for _, event := range events {
		if _, ok := allowedNames[event.InvolvedObject.Name]; !ok {
			continue
		}
		result = append(result, EventDiagnostic{
			Type:           event.Type,
			Reason:         event.Reason,
			Message:        event.Message,
			Object:         fmt.Sprintf("%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Name),
			Count:          event.Count,
			FirstTimestamp: formatEventTime(event.FirstTimestamp),
			LastTimestamp:  formatEventTimeValue(eventLastTimestamp(event)),
		})
		if len(result) >= limit {
			break
		}
	}
	return result
}

func eventLastTimestamp(event corev1.Event) time.Time {
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	if !event.EventTime.IsZero() {
		return event.EventTime.Time
	}
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp.Time
	}
	return event.CreationTimestamp.Time
}

func formatEventTime(value metav1.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Time.Format(time.RFC3339)
}

func formatEventTimeValue(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

// WaitForReady waits for the deployment to become fully ready
func (c *Client) WaitForReady(ctx context.Context, tunnelID string) error {
	if err := validateTunnelID(tunnelID); err != nil {
		return err
	}
	name := tunnelResourceName(tunnelID)
	deployClient := c.clientset.AppsV1().Deployments(c.namespace)
	var lastErr error
	pollTimer := time.NewTimer(2 * time.Second)
	defer pollTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w; last Kubernetes error: %v", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-pollTimer.C:
			dep, err := deployClient.Get(ctx, name, metav1.GetOptions{})
			pollTimer.Reset(2 * time.Second)
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
			desired := int32(1)
			if dep.Spec.Replicas != nil {
				desired = *dep.Spec.Replicas
			}
			if desired <= 0 {
				return nil
			}
			if dep.Status.ObservedGeneration < dep.Generation {
				continue
			}
			if dep.Status.Replicas <= desired &&
				dep.Status.UpdatedReplicas >= desired &&
				dep.Status.ReadyReplicas >= desired &&
				dep.Status.AvailableReplicas >= desired {
				return nil
			}
		}
	}
}
