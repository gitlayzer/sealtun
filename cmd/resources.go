package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Resource configuration helpers are shared by YAML apply and
// `inspect --resources`. Resource mutation goes through YAML apply only;
// there is no standalone resources command.

func normalizeResourceConfig(config *session.ResourceConfig) (*session.ResourceConfig, error) {
	out := defaultSessionResourceConfig()
	if config != nil {
		if config.Requests != nil {
			if strings.TrimSpace(config.Requests.CPU) != "" {
				out.Requests.CPU = strings.TrimSpace(config.Requests.CPU)
			}
			if strings.TrimSpace(config.Requests.Memory) != "" {
				out.Requests.Memory = strings.TrimSpace(config.Requests.Memory)
			}
		}
		if config.Limits != nil {
			if strings.TrimSpace(config.Limits.CPU) != "" {
				out.Limits.CPU = strings.TrimSpace(config.Limits.CPU)
			}
			if strings.TrimSpace(config.Limits.Memory) != "" {
				out.Limits.Memory = strings.TrimSpace(config.Limits.Memory)
			}
		}
	}
	if err := validateResourcePair("cpu", corev1.ResourceCPU, out.Requests.CPU, out.Limits.CPU); err != nil {
		return nil, err
	}
	if err := validateResourcePair("memory", corev1.ResourceMemory, out.Requests.Memory, out.Limits.Memory); err != nil {
		return nil, err
	}
	return out, nil
}

func validateResourcePair(label string, resourceName corev1.ResourceName, requestValue, limitValue string) error {
	request, err := parseResourceQuantity(label+" request", requestValue)
	if err != nil {
		return err
	}
	limit, err := parseResourceQuantity(label+" limit", limitValue)
	if err != nil {
		return err
	}
	if limit.Cmp(request) < 0 {
		return fmt.Errorf("%s limit must be greater than or equal to %s request", resourceName, resourceName)
	}
	return nil
}

func parseResourceQuantity(label, value string) (resource.Quantity, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return resource.Quantity{}, fmt.Errorf("%s is required", label)
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("invalid %s quantity %q: %w", label, value, err)
	}
	return quantity, nil
}

func defaultSessionResourceConfig() *session.ResourceConfig {
	return resourcesFromK8s(k8s.DefaultResourceConfig())
}

func effectiveSessionResourceConfig(config *session.ResourceConfig) *session.ResourceConfig {
	normalized, err := normalizeResourceConfig(config)
	if err != nil {
		return defaultSessionResourceConfig()
	}
	return normalized
}

func cloneSessionResourceConfig(config *session.ResourceConfig) *session.ResourceConfig {
	if config == nil {
		return nil
	}
	out := &session.ResourceConfig{}
	if config.Requests != nil {
		out.Requests = &session.ResourceValues{CPU: config.Requests.CPU, Memory: config.Requests.Memory}
	}
	if config.Limits != nil {
		out.Limits = &session.ResourceValues{CPU: config.Limits.CPU, Memory: config.Limits.Memory}
	}
	return out
}

func resourceConfigChanged(current, desired *session.ResourceConfig) bool {
	currentNormalized := effectiveSessionResourceConfig(current)
	desiredNormalized := effectiveSessionResourceConfig(desired)
	return currentNormalized.Requests.CPU != desiredNormalized.Requests.CPU ||
		currentNormalized.Requests.Memory != desiredNormalized.Requests.Memory ||
		currentNormalized.Limits.CPU != desiredNormalized.Limits.CPU ||
		currentNormalized.Limits.Memory != desiredNormalized.Limits.Memory
}

func collectTunnelResources(parent context.Context, tunnelID string) (*k8s.TunnelResourceList, error) {
	sess, err := activeScopedSession(parent, tunnelID)
	if err != nil {
		return nil, err
	}
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	return client.WithNamespace(sess.Namespace).TunnelResources(ctx, sess.TunnelID)
}

func activeScopedSession(ctx context.Context, tunnelID string) (*session.TunnelSession, error) {
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	scope, err := currentActiveScope()
	if err != nil {
		return nil, err
	}
	if sess.Region != scope.region || sess.Namespace != scope.namespace {
		return nil, fmt.Errorf("tunnel %s is outside the active scope", sess.TunnelID)
	}
	return sess, nil
}

func printTunnelResources(cmd *cobra.Command, payload *k8s.TunnelResourceList) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Resources")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Namespace: %s\n", payload.Namespace)
	fmt.Fprintln(out, "  Note: resource hints show Kubernetes occupancy, not cloud billing estimates.")
	if len(payload.Resources) == 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "No Kubernetes resources were reported for this tunnel.")
		return
	}
	fmt.Fprintln(out, "")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tSTATUS\tMANAGED\tAGE\tHINTS")
	for _, item := range payload.Resources {
		hints := append([]string{}, item.CostHints...)
		hints = append(hints, item.Warnings...)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Kind,
			item.Name,
			valueOr(item.Status, "-"),
			yesNo(item.Managed),
			valueOr(item.Age, "-"),
			valueOr(strings.Join(hints, "; "), "-"),
		)
	}
	_ = tw.Flush()
	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
}
