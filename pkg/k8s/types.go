package k8s

import (
	"regexp"
	"strings"

	"github.com/labring/sealtun/pkg/accesspolicy"
	"github.com/labring/sealtun/pkg/mesh"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
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
	SealosHost   string
	TargetURL    string
	BasicAuth    *BasicAuthOptions
	AccessPolicy *accesspolicy.Policy
	Resources    *ResourceConfig
}

type ResourceConfig struct {
	Requests ResourceValues
	Limits   ResourceValues
}

type ResourceValues struct {
	CPU    string
	Memory string
}

type MeshGatewaySpec struct {
	MeshName string
	Token    string
	Routes   []mesh.GatewayRoute
}

type MeshGatewayStatus struct {
	Name      string
	Host      string
	Namespace string
}

type MeshImportSpec struct {
	Name       string
	MeshName   string
	Protocol   string
	Port       int32
	TargetPort int32
}

type MeshCheck struct {
	GatewayDeploymentReady bool
	GatewayServiceExists   bool
	GatewayIngressHost     string
	ImportServiceExists    bool
	Warnings               []string
}

const (
	DefaultRequestCPU    = "10m"
	DefaultRequestMemory = "32Mi"
	DefaultLimitCPU      = "200m"
	DefaultLimitMemory   = "128Mi"
)

func DefaultResourceConfig() *ResourceConfig {
	return &ResourceConfig{
		Requests: ResourceValues{CPU: DefaultRequestCPU, Memory: DefaultRequestMemory},
		Limits:   ResourceValues{CPU: DefaultLimitCPU, Memory: DefaultLimitMemory},
	}
}

func EffectiveResourceConfig(config *ResourceConfig) *ResourceConfig {
	if config == nil {
		return DefaultResourceConfig()
	}
	defaults := DefaultResourceConfig()
	out := &ResourceConfig{
		Requests: ResourceValues{
			CPU:    defaultResourceValue(config.Requests.CPU, defaults.Requests.CPU),
			Memory: defaultResourceValue(config.Requests.Memory, defaults.Requests.Memory),
		},
		Limits: ResourceValues{
			CPU:    defaultResourceValue(config.Limits.CPU, defaults.Limits.CPU),
			Memory: defaultResourceValue(config.Limits.Memory, defaults.Limits.Memory),
		},
	}
	return out
}

func defaultResourceValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

type BasicAuthOptions struct {
	Username     string
	PasswordHash string
}

type TunnelHosts struct {
	PublicHost   string
	SealosHost   string
	CustomDomain string
	PublicPort   int32
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

type TunnelRemoteState struct {
	PublicHost   string               `json:"publicHost,omitempty"`
	SealosHost   string               `json:"sealosHost,omitempty"`
	CustomDomain string               `json:"customDomain,omitempty"`
	PublicPort   int32                `json:"publicPort,omitempty"`
	Secret       string               `json:"secret,omitempty"`
	Protocol     string               `json:"protocol,omitempty"`
	LocalPort    string               `json:"localPort,omitempty"`
	TargetURL    string               `json:"targetUrl,omitempty"`
	BasicAuth    *BasicAuthOptions    `json:"basicAuth,omitempty"`
	AccessPolicy *accesspolicy.Policy `json:"accessPolicy,omitempty"`
	Resources    *ResourceConfig      `json:"resources,omitempty"`
	AuthSecretOK bool                 `json:"-"`
	DeploymentOK bool                 `json:"-"`
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
	Type           string `json:"type,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Message        string `json:"message,omitempty"`
	Object         string `json:"object,omitempty"`
	Count          int32  `json:"count,omitempty"`
	FirstTimestamp string `json:"firstTimestamp,omitempty"`
	LastTimestamp  string `json:"lastTimestamp,omitempty"`
}

type TunnelResourceList struct {
	Namespace string           `json:"namespace"`
	TunnelID  string           `json:"tunnelId"`
	Resources []TunnelResource `json:"resources,omitempty"`
	Warnings  []string         `json:"warnings,omitempty"`
}

type TunnelResource struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Age       string            `json:"age,omitempty"`
	Namespace string            `json:"namespace"`
	Managed   bool              `json:"managed"`
	Labels    map[string]string `json:"labels,omitempty"`
	Warnings  []string          `json:"warnings,omitempty"`
	CostHints []string          `json:"costHints,omitempty"`
}

type TunnelLogOptions struct {
	TailLines    int64
	SinceSeconds int64
	Follow       bool
}

type resourceKind string

const (
	resourceDeployment  resourceKind = "deployment"
	resourceService     resourceKind = "service"
	resourceTCPService  resourceKind = "tcp-service"
	resourceIngress     resourceKind = "ingress"
	resourceIssuer      resourceKind = "issuer"
	resourceCertificate resourceKind = "certificate"
	resourceSecret      resourceKind = "secret"
)

const (
	managedLabelKey       = "cloud.sealos.io/app-deploy-manager"
	managedDomainLabelKey = "cloud.sealos.io/app-deploy-manager-domain"
	serverConfigDigestKey = "sealtun.labring.com/server-config"
	tunnelAuthSecretKey   = "secret"
	basicAuthUserKey      = "basicAuthUsername"
	basicAuthPasswordKey  = "basicAuthPasswordHash"
	accessPolicyKey       = "accessPolicy"
	configDigestSaltKey   = "configDigestSalt"
	meshConfigDigestKey   = "sealtun.labring.com/mesh-config"
	meshRoutesKey         = "routes.json"
	meshTokenKey          = "token"
)

var reservedCustomDomainSuffixes = []string{
	"cloud.sealos.app",
	"cloud.sealos.io",
	"sealosbja.site",
	"sealosgzg.site",
	"sealoshzh.site",
	"usw-1.sealos.app",
}

var (
	tunnelIDPattern        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,53}[a-z0-9])?$`)
	dnsLabelPattern        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	releaseVersionPattern  = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	compactDNSLabelPattern = regexp.MustCompile(`[^a-z0-9-]+`)
)

const serverRunAsUserID int64 = 1001

const meshOwnerName = "sealtun-mesh"

type createdResource struct {
	kind resourceKind
	name string
}
