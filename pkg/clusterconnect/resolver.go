package clusterconnect

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

type Target struct {
	Namespace string `json:"namespace"`
	PodName   string `json:"podName"`
	PodPort   int32  `json:"podPort"`
}

type ClusterAPI interface {
	GetService(ctx context.Context, namespace, name string) (*corev1.Service, error)
	ListEndpointSlices(ctx context.Context, namespace, serviceName string) (*discoveryv1.EndpointSliceList, error)
	ListServices(ctx context.Context, namespace string) (*corev1.ServiceList, error)
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error)
}

type kubeAPI struct {
	clientset kubernetes.Interface
}

type Resolver struct {
	Client           ClusterAPI
	DefaultNamespace string
}

func NewResolver(clientset kubernetes.Interface, defaultNamespace string) *Resolver {
	return &Resolver{Client: &kubeAPI{clientset: clientset}, DefaultNamespace: defaultNamespace}
}

func (k *kubeAPI) GetService(ctx context.Context, namespace, name string) (*corev1.Service, error) {
	return k.clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (k *kubeAPI) ListEndpointSlices(ctx context.Context, namespace, serviceName string) (*discoveryv1.EndpointSliceList, error) {
	selector := labels.Set{discoveryv1.LabelServiceName: serviceName}.AsSelector().String()
	return k.clientset.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
}

func (k *kubeAPI) ListServices(ctx context.Context, namespace string) (*corev1.ServiceList, error) {
	return k.clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
}

func (k *kubeAPI) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.PodList, error) {
	return k.clientset.CoreV1().Pods(namespace).List(ctx, opts)
}

func (r *Resolver) Resolve(ctx context.Context, host string, port int) (*Target, error) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return nil, fmt.Errorf("target host is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if target, err := r.resolveServiceIP(ctx, host, port); err == nil {
			return target, nil
		}
		return r.resolvePodIP(ctx, host, port)
	}
	return r.resolveServiceHost(ctx, host, port)
}

func (r *Resolver) resolveServiceHost(ctx context.Context, host string, port int) (*Target, error) {
	service, namespace, ok := parseServiceHost(host, r.DefaultNamespace)
	if !ok {
		return nil, fmt.Errorf("unsupported cluster host %q", host)
	}
	svc, err := r.Client.GetService(ctx, namespace, service)
	if err != nil {
		return nil, err
	}
	return r.resolveService(ctx, svc, port)
}

func (r *Resolver) resolveServiceIP(ctx context.Context, ip string, port int) (*Target, error) {
	services, err := r.Client.ListServices(ctx, r.DefaultNamespace)
	if err != nil {
		return nil, err
	}
	for i := range services.Items {
		svc := &services.Items[i]
		if serviceHasIP(svc, ip) {
			return r.resolveService(ctx, svc, port)
		}
	}
	return nil, fmt.Errorf("no service found for cluster ip %s in namespace %s", ip, r.DefaultNamespace)
}

func serviceHasIP(svc *corev1.Service, ip string) bool {
	if svc.Spec.ClusterIP == ip {
		return true
	}
	for _, clusterIP := range svc.Spec.ClusterIPs {
		if clusterIP == ip {
			return true
		}
	}
	return false
}

func (r *Resolver) resolveService(ctx context.Context, svc *corev1.Service, port int) (*Target, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}
	servicePort, err := chooseServicePort(svc, port)
	if err != nil {
		return nil, err
	}
	slices, err := r.Client.ListEndpointSlices(ctx, svc.Namespace, svc.Name)
	if err != nil {
		return nil, err
	}
	for sliceIndex := range slices.Items {
		slice := &slices.Items[sliceIndex]
		endpointPort := chooseEndpointSlicePort(servicePort, slice.Ports, port)
		if endpointPort == nil {
			continue
		}
		for endpointIndex := range slice.Endpoints {
			endpoint := &slice.Endpoints[endpointIndex]
			if (endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready) ||
				(endpoint.Conditions.Terminating != nil && *endpoint.Conditions.Terminating) {
				continue
			}
			if endpoint.TargetRef != nil && endpoint.TargetRef.Kind == "Pod" && endpoint.TargetRef.Name != "" {
				ns := endpoint.TargetRef.Namespace
				if ns == "" {
					ns = svc.Namespace
				}
				return &Target{Namespace: ns, PodName: endpoint.TargetRef.Name, PodPort: *endpointPort.Port}, nil
			}
			for _, address := range endpoint.Addresses {
				target, err := r.resolvePodIP(ctx, address, int(*endpointPort.Port))
				if err == nil {
					return target, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("service %s/%s has no ready pod endpoints for port %d", svc.Namespace, svc.Name, port)
}

func chooseServicePort(svc *corev1.Service, requested int) (*corev1.ServicePort, error) {
	if len(svc.Spec.Ports) == 0 {
		return nil, fmt.Errorf("service %s/%s has no ports", svc.Namespace, svc.Name)
	}
	if requested > 0 {
		for i := range svc.Spec.Ports {
			if int(svc.Spec.Ports[i].Port) == requested || svc.Spec.Ports[i].TargetPort.IntValue() == requested {
				return &svc.Spec.Ports[i], nil
			}
			if svc.Spec.Ports[i].Name == strconv.Itoa(requested) {
				return &svc.Spec.Ports[i], nil
			}
		}
	}
	if len(svc.Spec.Ports) == 1 {
		return &svc.Spec.Ports[0], nil
	}
	return nil, fmt.Errorf("service %s/%s has multiple ports; specify one", svc.Namespace, svc.Name)
}

func chooseEndpointSlicePort(servicePort *corev1.ServicePort, endpointPorts []discoveryv1.EndpointPort, requested int) *discoveryv1.EndpointPort {
	for i := range endpointPorts {
		port := &endpointPorts[i]
		if endpointSlicePortMatches(servicePort, port, requested) {
			return port
		}
	}
	if len(endpointPorts) == 1 && endpointPorts[0].Port != nil {
		return &endpointPorts[0]
	}
	return nil
}

func endpointSlicePortMatches(servicePort *corev1.ServicePort, endpointPort *discoveryv1.EndpointPort, requested int) bool {
	if servicePort == nil || endpointPort == nil || endpointPort.Port == nil {
		return false
	}
	if servicePort.Name != "" && endpointPort.Name != nil && servicePort.Name == *endpointPort.Name {
		return true
	}
	if requested > 0 && int(*endpointPort.Port) == requested {
		return true
	}
	return targetPortMatches(servicePort.TargetPort, *endpointPort.Port)
}

func targetPortMatches(target intstr.IntOrString, endpointPort int32) bool {
	if target.Type == intstr.Int && target.IntVal > 0 {
		return target.IntVal == endpointPort
	}
	return false
}

func (r *Resolver) resolvePodIP(ctx context.Context, ip string, port int) (*Target, error) {
	if port > 65535 {
		return nil, fmt.Errorf("pod ip %s target port must be between 1 and 65535", ip)
	}
	pods, err := r.Client.ListPods(ctx, r.DefaultNamespace, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("status.podIP", ip).String(),
	})
	if err != nil {
		return nil, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
			continue
		}
		if port <= 0 {
			return nil, fmt.Errorf("pod ip %s requires an explicit target port", ip)
		}
		return &Target{Namespace: pod.Namespace, PodName: pod.Name, PodPort: int32(port)}, nil
	}
	return nil, fmt.Errorf("no running pod found for ip %s in namespace %s", ip, r.DefaultNamespace)
}

func parseServiceHost(host, defaultNamespace string) (service string, namespace string, ok bool) {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	switch {
	case len(parts) == 1:
		return parts[0], defaultNamespace, parts[0] != "" && defaultNamespace != ""
	case len(parts) == 3 && parts[2] == "svc":
		return parts[0], parts[1], parts[0] != "" && parts[1] != ""
	case len(parts) == 4 && parts[2] == "svc" && parts[3] == "cluster":
		return parts[0], parts[1], parts[0] != "" && parts[1] != ""
	case len(parts) == 5 && parts[2] == "svc" && parts[3] == "cluster" && parts[4] == "local":
		return parts[0], parts[1], parts[0] != "" && parts[1] != ""
	default:
		return "", "", false
	}
}
