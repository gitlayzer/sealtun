package clusterconnect

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/labring/sealtun/pkg/auth"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Environment struct {
	Profile    string
	Region     string
	Namespace  string
	RESTConfig *rest.Config
	Clientset  kubernetes.Interface
}

type AccessReviewer interface {
	Review(ctx context.Context, namespace, verb, group, resource, subresource string) (allowed bool, reason string, err error)
}

type k8sAccessReviewer struct {
	clientset kubernetes.Interface
}

func NewActiveEnvironment() (*Environment, error) {
	root, err := auth.CurrentSealtunDir()
	if err != nil {
		return nil, err
	}
	profile, err := auth.CurrentProfileNameFromDir(root)
	if err != nil {
		return nil, fmt.Errorf("failed to load active profile marker: %w", err)
	}
	authData, err := auth.LoadAuthDataFromDir(root)
	if err != nil {
		return nil, fmt.Errorf("failed to load active Sealtun auth: %w", err)
	}
	kubeconfig, err := auth.ActiveKubeconfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load active kubeconfig: %w", err)
	}
	rawConfig, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to parse active kubeconfig: %w", err)
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to build Kubernetes REST config: %w", err)
	}
	restConfig.WarningHandler = rest.NoWarnings{}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	namespace := "default"
	if ctx, ok := rawConfig.Contexts[rawConfig.CurrentContext]; ok && strings.TrimSpace(ctx.Namespace) != "" {
		namespace = ctx.Namespace
	}

	return &Environment{
		Profile:    profile,
		Region:     authData.Region,
		Namespace:  namespace,
		RESTConfig: restConfig,
		Clientset:  clientset,
	}, nil
}

func (e *Environment) Preflight(ctx context.Context, opts Options) (*Preflight, error) {
	if e == nil || e.Clientset == nil {
		return nil, fmt.Errorf("connect environment is not initialized")
	}
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" {
		namespace = e.Namespace
	}
	mode, err := NormalizeMode(opts.Mode)
	if err != nil {
		return nil, err
	}
	caps := ProbeCapabilities(ctx, namespace, &k8sAccessReviewer{clientset: e.Clientset})
	selected, modes, selectErr := SelectMode(mode, caps)
	payload := &Preflight{
		LoggedIn:      true,
		ActiveProfile: e.Profile,
		Region:        e.Region,
		Namespace:     namespace,
		Mode:          mode,
		SelectedMode:  selected,
		Capabilities:  caps,
		Modes:         modes,
	}
	if selectErr != nil {
		return payload, selectErr
	}
	return payload, nil
}

func ProbeCapabilities(ctx context.Context, namespace string, reviewer AccessReviewer) []Capability {
	type capabilityCheck struct {
		name        string
		required    bool
		verb        string
		group       string
		resource    string
		subresource string
	}
	checks := []capabilityCheck{
		{name: CapabilityKubeconfig, required: true},
		{name: CapabilityServicesGet, required: true, verb: "get", resource: "services"},
		{name: CapabilityServicesList, required: true, verb: "list", resource: "services"},
		{name: CapabilityEndpointSlicesList, required: true, verb: "list", group: "discovery.k8s.io", resource: "endpointslices"},
		{name: CapabilityPodsGet, required: true, verb: "get", resource: "pods"},
		{name: CapabilityPodsList, required: true, verb: "list", resource: "pods"},
		{name: CapabilityPodsPortForward, required: true, verb: "create", resource: "pods", subresource: "portforward"},
		{name: CapabilityDeployments, verb: "create", group: "apps", resource: "deployments"},
		{name: CapabilitySecrets, verb: "create", resource: "secrets"},
		{name: CapabilityConfigMaps, verb: "create", resource: "configmaps"},
	}
	caps := make([]Capability, len(checks))
	var wg sync.WaitGroup
	for i, check := range checks {
		cap := Capability{Name: check.name, Required: check.required, Namespace: namespace}
		if check.name == CapabilityKubeconfig {
			cap.Allowed = true
			caps[i] = cap
			continue
		}
		wg.Add(1)
		go func(index int, check capabilityCheck, cap Capability) {
			defer wg.Done()
			allowed, reason, err := reviewer.Review(ctx, namespace, check.verb, check.group, check.resource, check.subresource)
			cap.Allowed = allowed
			cap.Reason = reason
			if err != nil {
				cap.Error = err.Error()
			}
			caps[index] = cap
		}(i, check, cap)
	}
	wg.Wait()
	return caps
}

func (r *k8sAccessReviewer) Review(ctx context.Context, namespace, verb, group, resource, subresource string) (bool, string, error) {
	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace:   namespace,
				Verb:        verb,
				Group:       group,
				Resource:    resource,
				Subresource: subresource,
			},
		},
	}
	result, err := r.clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", err
	}
	return result.Status.Allowed, result.Status.Reason, nil
}
