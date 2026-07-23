package cmd

import (
	"context"
	"encoding/json"
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

var resourcesJSON bool
var resourcesSetRequestCPU string
var resourcesSetRequestMemory string
var resourcesSetLimitCPU string
var resourcesSetLimitMemory string

var resourcesCmd = &cobra.Command{
	Use:          "resources [tunnel-id]",
	Short:        "Show Kubernetes resources used by a Sealtun tunnel",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := collectTunnelResources(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if resourcesJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printTunnelResources(cmd, payload)
		return nil
	},
}

var resourcesSetCmd = &cobra.Command{
	Use:          "set [tunnel-id]",
	Short:        "Update tunnel pod CPU and memory requests/limits",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if resourcesSetRequestCPU == "" && resourcesSetRequestMemory == "" && resourcesSetLimitCPU == "" && resourcesSetLimitMemory == "" {
			return fmt.Errorf("set at least one resource option")
		}
		payload, err := setTunnelResources(cmd.Context(), args[0], resourceSetInput{
			RequestCPU:    resourcesSetRequestCPU,
			RequestMemory: resourcesSetRequestMemory,
			LimitCPU:      resourcesSetLimitCPU,
			LimitMemory:   resourcesSetLimitMemory,
		})
		if err != nil {
			return err
		}
		if resourcesJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printResourceUpdate(cmd, payload)
		return nil
	},
}

var resourcesUnsetCmd = &cobra.Command{
	Use:          "unset [tunnel-id]",
	Short:        "Reset tunnel pod resources to Sealtun defaults",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := unsetTunnelResources(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if resourcesJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printResourceUpdate(cmd, payload)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resourcesCmd)
	resourcesCmd.AddCommand(resourcesSetCmd, resourcesUnsetCmd)
	markAlphaTree(resourcesCmd)
	resourcesCmd.Flags().BoolVar(&resourcesJSON, "json", false, "Output resources as JSON")
	resourcesSetCmd.Flags().StringVar(&resourcesSetRequestCPU, "request-cpu", "", "Set pod CPU request, e.g. 10m")
	resourcesSetCmd.Flags().StringVar(&resourcesSetRequestMemory, "request-memory", "", "Set pod memory request, e.g. 32Mi")
	resourcesSetCmd.Flags().StringVar(&resourcesSetLimitCPU, "limit-cpu", "", "Set pod CPU limit, e.g. 200m")
	resourcesSetCmd.Flags().StringVar(&resourcesSetLimitMemory, "limit-memory", "", "Set pod memory limit, e.g. 128Mi")
	resourcesSetCmd.Flags().BoolVar(&resourcesJSON, "json", false, "Output resource update as JSON")
	resourcesUnsetCmd.Flags().BoolVar(&resourcesJSON, "json", false, "Output resource update as JSON")
}

type resourceSetInput struct {
	RequestCPU    string
	RequestMemory string
	LimitCPU      string
	LimitMemory   string
}

type resourceUpdatePayload struct {
	TunnelID           string                  `json:"tunnelId"`
	Namespace          string                  `json:"namespace"`
	Deployment         string                  `json:"deployment"`
	Resources          *session.ResourceConfig `json:"resources"`
	RolloutReady       bool                    `json:"rolloutReady"`
	DaemonReconnected  bool                    `json:"daemonReconnected"`
	AppliedImmediately bool                    `json:"appliedImmediately"`
	Message            string                  `json:"message,omitempty"`
}

func setTunnelResources(parent context.Context, tunnelID string, input resourceSetInput) (*resourceUpdatePayload, error) {
	sess, err := activeScopedSession(parent, tunnelID)
	if err != nil {
		return nil, err
	}
	if sessionExpired(*sess, nowUTC()) {
		return nil, fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
	}
	next, err := mergeResourceSetInput(sess.ResourceConfig, input)
	if err != nil {
		return nil, err
	}
	return updateTunnelResourceConfig(parent, sess, next)
}

func unsetTunnelResources(parent context.Context, tunnelID string) (*resourceUpdatePayload, error) {
	sess, err := activeScopedSession(parent, tunnelID)
	if err != nil {
		return nil, err
	}
	if sessionExpired(*sess, nowUTC()) {
		return nil, fmt.Errorf("tunnel %s has expired; run cleanup and recreate the tunnel", sess.TunnelID)
	}
	return updateTunnelResourceConfig(parent, sess, defaultSessionResourceConfig())
}

func updateTunnelResourceConfig(parent context.Context, sess *session.TunnelSession, config *session.ResourceConfig) (*resourceUpdatePayload, error) {
	normalized, err := normalizeResourceConfig(config)
	if err != nil {
		return nil, err
	}
	var payload *resourceUpdatePayload
	err = withTunnelOperationLock(sess.TunnelID, func() error {
		current, err := findSession(sess.TunnelID)
		if err != nil {
			return err
		}
		changed := resourceConfigChanged(current.ResourceConfig, normalized)
		client, err := k8sClientForSession(*current)
		if err != nil {
			return err
		}
		namespacedClient := client.WithNamespace(current.Namespace)
		mutatedAt := nowUTC().Add(-time.Second)
		ctx, cancel := context.WithTimeout(parent, readyTimeout)
		defer cancel()
		if err := namespacedClient.UpdateTunnelResources(ctx, current.TunnelID, resourcesToK8s(normalized)); err != nil {
			return err
		}

		current.ResourceConfig = normalized
		if err := session.Update(*current); err != nil {
			return fmt.Errorf("save updated session resources: %w", err)
		}

		payload = &resourceUpdatePayload{
			TunnelID:           current.TunnelID,
			Namespace:          current.Namespace,
			Deployment:         "sealtun-" + current.TunnelID,
			Resources:          normalized,
			AppliedImmediately: current.ConnectionState != session.ConnectionStateStopped,
		}
		if current.ConnectionState == session.ConnectionStateStopped {
			payload.Message = "Tunnel is stopped; deployment template was updated without starting replicas."
			return nil
		}

		readyCtx, cancelReady := context.WithTimeout(parent, readyTimeout)
		defer cancelReady()
		if err := namespacedClient.WaitForReady(readyCtx, current.TunnelID); err != nil {
			return fmt.Errorf("resources were updated, but rollout readiness was not confirmed: %w", err)
		}
		payload.RolloutReady = true
		if current.Mode == "daemon" && changed {
			if err := waitForDaemonSessionAfter(current.TunnelID, daemonConnectTimeout, mutatedAt); err != nil {
				return fmt.Errorf("resources were updated, but local daemon reconnection was not confirmed: %w", err)
			}
			payload.DaemonReconnected = true
		}
		return nil
	})
	return payload, err
}

func mergeResourceSetInput(existing *session.ResourceConfig, input resourceSetInput) (*session.ResourceConfig, error) {
	next := cloneSessionResourceConfig(effectiveSessionResourceConfig(existing))
	if strings.TrimSpace(input.RequestCPU) != "" {
		next.Requests.CPU = strings.TrimSpace(input.RequestCPU)
	}
	if strings.TrimSpace(input.RequestMemory) != "" {
		next.Requests.Memory = strings.TrimSpace(input.RequestMemory)
	}
	if strings.TrimSpace(input.LimitCPU) != "" {
		next.Limits.CPU = strings.TrimSpace(input.LimitCPU)
	}
	if strings.TrimSpace(input.LimitMemory) != "" {
		next.Limits.Memory = strings.TrimSpace(input.LimitMemory)
	}
	return normalizeResourceConfig(next)
}

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

func printResourceUpdate(cmd *cobra.Command, payload *resourceUpdatePayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Resources Updated")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Namespace: %s\n", payload.Namespace)
	fmt.Fprintf(out, "  Deployment: %s\n", payload.Deployment)
	if payload.Resources != nil && payload.Resources.Requests != nil && payload.Resources.Limits != nil {
		fmt.Fprintf(out, "  Requests: cpu=%s memory=%s\n", payload.Resources.Requests.CPU, payload.Resources.Requests.Memory)
		fmt.Fprintf(out, "  Limits: cpu=%s memory=%s\n", payload.Resources.Limits.CPU, payload.Resources.Limits.Memory)
	}
	fmt.Fprintf(out, "  Applied immediately: %s\n", yesNo(payload.AppliedImmediately))
	if payload.AppliedImmediately {
		fmt.Fprintf(out, "  Rollout ready: %s\n", yesNo(payload.RolloutReady))
		if payload.DaemonReconnected {
			fmt.Fprintln(out, "  Daemon reconnected: yes")
		}
	}
	if payload.Message != "" {
		fmt.Fprintf(out, "  Note: %s\n", payload.Message)
	}
}
