package k8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/labring/sealtun/pkg/mesh"
	"github.com/labring/sealtun/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
)

func meshGatewayName(meshName string) string {
	name := mesh.NormalizeName(meshName)
	if name == "" {
		name = mesh.DefaultName
	}
	return "sealtun-mesh-" + name
}

func meshGatewaySelector(owner string) map[string]string {
	return map[string]string{
		"app":           mesh.DefaultGatewayName,
		managedLabelKey: owner,
	}
}

func meshGatewayLabels(owner string) map[string]string {
	return meshGatewaySelector(owner)
}

func (c *Client) ensureMeshSecret(ctx context.Context, name, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels:    managedLabels(name),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{meshTokenKey: []byte(token)},
	}
	client := c.clientset.CoreV1().Secrets(c.namespace)
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, secret, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if err := rejectUnmanagedExisting("secret", name, name, existing.Labels); err != nil {
		return err
	}
	if authSecretUpToDate(secret, existing) {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := rejectUnmanagedExisting("secret", name, name, current.Labels); err != nil {
			return err
		}
		if authSecretUpToDate(secret, current) {
			return nil
		}
		next := secret.DeepCopy()
		next.ResourceVersion = current.ResourceVersion
		_, err = client.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

func (c *Client) ensureMeshConfigMap(ctx context.Context, name string, routesJSON []byte) error {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels:    managedLabels(name),
		},
		Data: map[string]string{meshRoutesKey: string(routesJSON)},
	}
	client := c.clientset.CoreV1().ConfigMaps(c.namespace)
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, configMap, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if err := rejectUnmanagedExisting("configmap", name, name, existing.Labels); err != nil {
		return err
	}
	if meshConfigMapUpToDate(configMap, existing) {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := rejectUnmanagedExisting("configmap", name, name, current.Labels); err != nil {
			return err
		}
		if meshConfigMapUpToDate(configMap, current) {
			return nil
		}
		next := configMap.DeepCopy()
		next.ResourceVersion = current.ResourceVersion
		_, err = client.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

func meshConfigMapUpToDate(desired, current *corev1.ConfigMap) bool {
	return desired != nil && current != nil &&
		apiequality.Semantic.DeepEqual(desired.Data, current.Data) &&
		apiequality.Semantic.DeepEqual(desired.BinaryData, current.BinaryData) &&
		mapContains(current.Labels, desired.Labels)
}

func (c *Client) ensureMeshDeployment(ctx context.Context, name, configDigest string) error {
	replicas := int32(1)
	f := false
	t := true
	u := serverRunAsUserID
	image, err := serverImageForVersion(version.Version)
	if err != nil {
		return err
	}
	requirements, err := resourceRequirementsForConfig(DefaultResourceConfig())
	if err != nil {
		return err
	}
	labels := meshGatewayLabels(name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: meshGatewaySelector(name)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{meshConfigDigestKey: configDigest},
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: &f,
					Containers: []corev1.Container{
						{
							Name:            mesh.DefaultGatewayName,
							Image:           image,
							ImagePullPolicy: corev1.PullAlways,
							Args: []string{
								"mesh", "gateway",
								"--listen", fmt.Sprintf(":%d", mesh.DefaultGatewayPort),
								"--routes-env", "SEALTUN_MESH_ROUTES",
								"--token-env", "SEALTUN_MESH_TOKEN",
							},
							Env: []corev1.EnvVar{
								{
									Name: "SEALTUN_MESH_ROUTES",
									ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{Name: name},
										Key:                  meshRoutesKey,
									}},
								},
								{
									Name: "SEALTUN_MESH_TOKEN",
									ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{Name: name},
										Key:                  meshTokenKey,
									}},
								},
							},
							Ports:     []corev1.ContainerPort{{Name: "http", ContainerPort: mesh.DefaultGatewayPort}},
							Resources: requirements,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "tmp", MountPath: "/tmp"},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &f,
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								ReadOnlyRootFilesystem:   &t,
								RunAsNonRoot:             &t,
								RunAsUser:                &u,
								RunAsGroup:               &u,
								SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/_sealtun/mesh/healthz", Port: intstr.FromInt32(mesh.DefaultGatewayPort)}},
								InitialDelaySeconds: 1,
								PeriodSeconds:       2,
							},
						},
					},
					Volumes: []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
				},
			},
		},
	}
	client := c.clientset.AppsV1().Deployments(c.namespace)
	existing, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(ctx, deployment, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if err := rejectUnmanagedExisting("deployment", name, name, existing.Labels); err != nil {
		return err
	}
	if deploymentUpToDate(deployment, existing) {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.Get(ctx, name, metav1.GetOptions{})
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
		_, err = client.Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

func (c *Client) ensureMeshGatewayService(ctx context.Context, name string) (bool, error) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.namespace, Labels: managedLabels(name)},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: meshGatewaySelector(name),
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(mesh.DefaultGatewayPort)}},
		},
	}
	return c.applyService(ctx, service, name)
}

func (c *Client) ensureMeshGatewayIngress(ctx context.Context, name, host string) (bool, error) {
	pathType := netv1.PathTypePrefix
	ingressClass := "nginx"
	ingress := c.generateIngress(name, host, "", []string{"/"}, &pathType, &ingressClass)
	return c.applyIngress(ctx, ingress)
}

func (c *Client) deleteMeshConfigMap(ctx context.Context, name string) error {
	client := c.clientset.CoreV1().ConfigMaps(c.namespace)
	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !managedLabelMatches(resource.Labels, name) {
		return nil
	}
	if err := client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) deleteMeshSecret(ctx context.Context, name string) error {
	client := c.clientset.CoreV1().Secrets(c.namespace)
	resource, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !managedLabelMatches(resource.Labels, name) {
		return nil
	}
	if err := client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Client) MeshGatewayHost(meshName string) string {
	name := meshGatewayName(meshName)
	return c.sealosHost(name)
}

func (c *Client) EnsureMeshGateway(ctx context.Context, spec MeshGatewaySpec) (MeshGatewayStatus, error) {
	spec.MeshName = mesh.NormalizeName(spec.MeshName)
	if spec.MeshName == "" {
		spec.MeshName = mesh.DefaultName
	}
	if err := mesh.ValidateName("mesh name", spec.MeshName); err != nil {
		return MeshGatewayStatus{}, err
	}
	if strings.TrimSpace(spec.Token) == "" {
		return MeshGatewayStatus{}, fmt.Errorf("mesh gateway token is required")
	}
	if err := mesh.ValidateGatewayRoutes(spec.Routes); err != nil {
		return MeshGatewayStatus{}, err
	}
	name := meshGatewayName(spec.MeshName)
	host := c.MeshGatewayHost(spec.MeshName)
	if err := validateSealosHost(host); err != nil {
		return MeshGatewayStatus{}, err
	}
	routesJSON, err := json.Marshal(spec.Routes)
	if err != nil {
		return MeshGatewayStatus{}, err
	}
	configDigest := meshConfigDigest(spec.Token, routesJSON)
	if err := c.ensureMeshSecret(ctx, name, spec.Token); err != nil {
		return MeshGatewayStatus{}, fmt.Errorf("ensure mesh secret: %w", err)
	}
	if err := c.ensureMeshConfigMap(ctx, name, routesJSON); err != nil {
		return MeshGatewayStatus{}, fmt.Errorf("ensure mesh config: %w", err)
	}
	if err := c.ensureMeshDeployment(ctx, name, configDigest); err != nil {
		return MeshGatewayStatus{}, fmt.Errorf("ensure mesh deployment: %w", err)
	}
	if _, err := c.ensureMeshGatewayService(ctx, name); err != nil {
		return MeshGatewayStatus{}, fmt.Errorf("ensure mesh gateway service: %w", err)
	}
	if _, err := c.ensureMeshGatewayIngress(ctx, name, host); err != nil {
		return MeshGatewayStatus{}, fmt.Errorf("ensure mesh gateway ingress: %w", err)
	}
	return MeshGatewayStatus{Name: name, Host: host, Namespace: c.namespace}, nil
}

func meshConfigDigest(token string, routesJSON []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(strings.TrimSpace(token)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(routesJSON)
	return hex.EncodeToString(hash.Sum(nil))
}

func (c *Client) EnsureMeshImport(ctx context.Context, spec MeshImportSpec) (string, error) {
	name := mesh.ImportServiceName(spec.Name)
	if err := mesh.ValidateName("mesh import service", name); err != nil {
		return "", err
	}
	if err := mesh.ValidateProtocol(spec.Protocol); err != nil {
		return "", err
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return "", fmt.Errorf("invalid mesh import port %d", spec.Port)
	}
	if spec.TargetPort < 1 || spec.TargetPort > 65535 {
		return "", fmt.Errorf("invalid mesh import target port %d", spec.TargetPort)
	}
	owner := meshGatewayName(spec.MeshName)
	labels := managedLabels(meshOwnerName)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: meshGatewaySelector(owner),
			Ports: []corev1.ServicePort{
				{
					Name:       mesh.NormalizeProtocol(spec.Protocol),
					Port:       spec.Port,
					TargetPort: intstr.FromInt32(spec.TargetPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	if _, err := c.applyService(ctx, service, meshOwnerName); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", name, c.namespace, spec.Port), nil
}

func (c *Client) CleanupMeshImport(ctx context.Context, name string) error {
	_, err := c.deleteNamedServiceIfOwned(ctx, mesh.ImportServiceName(name), meshOwnerName)
	return err
}

func (c *Client) MeshCheck(ctx context.Context, meshName string, service mesh.Service) (MeshCheck, error) {
	out := MeshCheck{}
	name := meshGatewayName(meshName)
	deployment, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		out.Warnings = append(out.Warnings, "mesh gateway deployment is missing")
	} else if err != nil {
		return out, err
	} else {
		out.GatewayDeploymentReady = deployment.Status.ReadyReplicas > 0
		if !out.GatewayDeploymentReady {
			out.Warnings = append(out.Warnings, fmt.Sprintf("mesh gateway deployment has %d ready replicas", deployment.Status.ReadyReplicas))
		}
	}
	if _, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		out.Warnings = append(out.Warnings, "mesh gateway service is missing")
	} else if err != nil {
		return out, err
	} else {
		out.GatewayServiceExists = true
	}
	if ingress, err := c.clientset.NetworkingV1().Ingresses(c.namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		out.Warnings = append(out.Warnings, "mesh gateway ingress is missing")
	} else if err != nil {
		return out, err
	} else if len(ingress.Spec.Rules) > 0 {
		out.GatewayIngressHost = ingress.Spec.Rules[0].Host
	}
	importName := mesh.ImportServiceName(service.Name)
	if _, err := c.clientset.CoreV1().Services(c.namespace).Get(ctx, importName, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mesh import service %s is missing", importName))
	} else if err != nil {
		return out, err
	} else {
		out.ImportServiceExists = true
	}
	return out, nil
}

func (c *Client) KubernetesServiceReady(ctx context.Context, namespace, serviceName string) (bool, int, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = c.namespace
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return false, 0, fmt.Errorf("service name is required")
	}
	service, err := c.clientset.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	selector := klabels.Set(service.Spec.Selector).AsSelector()
	if selector.Empty() {
		return true, 0, nil
	}
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return false, 0, err
	}
	ready := 0
	for _, pod := range pods.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				ready++
				break
			}
		}
	}
	return true, ready, nil
}

func (c *Client) CleanupMesh(ctx context.Context, meshName string) error {
	name := meshGatewayName(meshName)
	var firstErr error
	if deleted, err := c.deleteDeploymentIfOwned(ctx, name); err != nil {
		recordFirstErr(&firstErr, err)
	} else if !deleted {
		_ = deleted
	}
	if _, err := c.deleteNamedServiceIfOwned(ctx, name, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	if _, err := c.deleteIngressIfOwned(ctx, name, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	if err := c.deleteMeshConfigMap(ctx, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	if err := c.deleteMeshSecret(ctx, name); err != nil {
		recordFirstErr(&firstErr, err)
	}
	return firstErr
}
