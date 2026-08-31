package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type inspectPayload struct {
	TunnelID                    string                  `json:"tunnelId"`
	Status                      string                  `json:"status"`
	Mode                        string                  `json:"mode,omitempty"`
	Region                      string                  `json:"region,omitempty"`
	Namespace                   string                  `json:"namespace,omitempty"`
	Protocol                    string                  `json:"protocol,omitempty"`
	Host                        string                  `json:"host,omitempty"`
	SealosHost                  string                  `json:"sealosHost,omitempty"`
	CustomDomain                string                  `json:"customDomain,omitempty"`
	PublicPort                  int32                   `json:"publicPort,omitempty"`
	LocalPort                   string                  `json:"localPort,omitempty"`
	TargetURL                   string                  `json:"targetUrl,omitempty"`
	TargetTLSInsecureSkipVerify bool                    `json:"targetTlsInsecureSkipVerify,omitempty"`
	BasicAuth                   *inspectBasicAuth       `json:"basicAuth,omitempty"`
	AccessPolicy                *inspectAccessPolicy    `json:"accessPolicy,omitempty"`
	TTL                         string                  `json:"ttl,omitempty"`
	ExpiresAt                   string                  `json:"expiresAt,omitempty"`
	PID                         int                     `json:"pid"`
	ProcessAlive                bool                    `json:"processAlive"`
	LocalPortReachable          bool                    `json:"localPortReachable"`
	CreatedAt                   string                  `json:"createdAt,omitempty"`
	Resources                   []string                `json:"resources,omitempty"`
	LastError                   string                  `json:"lastError,omitempty"`
	Remote                      *k8s.TunnelDiagnostics  `json:"remote,omitempty"`
	Metrics                     *metricsPayload         `json:"metrics,omitempty"`
	KubernetesResources         *k8s.TunnelResourceList `json:"kubernetesResources,omitempty"`
	Warnings                    []string                `json:"warnings,omitempty"`
}

type inspectBasicAuth struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username,omitempty"`
}

type inspectAccessPolicy struct {
	BearerTokens   int      `json:"bearerTokens,omitempty"`
	IPAllowlist    []string `json:"ipAllowlist,omitempty"`
	IPDenylist     []string `json:"ipDenylist,omitempty"`
	TemporaryLinks int      `json:"temporaryLinks,omitempty"`
	RateLimit      string   `json:"rateLimit,omitempty"`
	AuditEnabled   bool     `json:"auditEnabled,omitempty"`
}

type remoteDiagnosticsCollector func(context.Context, session.TunnelSession) (*k8s.TunnelDiagnostics, error)

var inspectJSON bool
var inspectRemote bool
var inspectMetrics bool
var inspectResources bool
var inspectWatch bool
var inspectInterval time.Duration
var inspectCount int

var inspectCmd = &cobra.Command{
	Use:   "inspect [tunnel-id]",
	Short: "Inspect a local Sealtun tunnel session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if inspectWatch {
			if inspectInterval <= 0 {
				return fmt.Errorf("--interval must be greater than 0")
			}
			if inspectCount < 0 {
				return fmt.Errorf("--count must be greater than or equal to 0")
			}
			return runInspectWatch(cmd, args[0], watchOptions{JSON: inspectJSON, Interval: inspectInterval, Count: inspectCount})
		}
		payload, err := collectInspectPayloadWithContext(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if inspectJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}

		printInspect(cmd, payload)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().BoolVar(&inspectJSON, "json", false, "Output tunnel session details as JSON")
	inspectCmd.Flags().BoolVar(&inspectRemote, "remote", false, "Include best-effort remote Kubernetes diagnostics and recent events")
	inspectCmd.Flags().BoolVar(&inspectMetrics, "metrics", false, "Include local, Kubernetes, and server metrics")
	inspectCmd.Flags().BoolVar(&inspectResources, "resources", false, "Include the Kubernetes resource inventory and occupancy hints")
	inspectCmd.Flags().BoolVar(&inspectWatch, "watch", false, "Refresh inspection until interrupted or --count is reached")
	inspectCmd.Flags().DurationVar(&inspectInterval, "interval", 3*time.Second, "Refresh interval when --watch is enabled")
	inspectCmd.Flags().IntVar(&inspectCount, "count", 0, "Stop after N refreshes; 0 watches until interrupted")
}

func runInspectWatch(cmd *cobra.Command, tunnelID string, opts watchOptions) error {
	out := cmd.OutOrStdout()
	enc := json.NewEncoder(out)
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	remaining := opts.Count
	first := true
	for {
		if !first {
			select {
			case <-cmd.Context().Done():
				return nil
			case <-ticker.C:
			}
		}
		first = false
		payload, err := collectInspectPayloadWithContext(cmd.Context(), tunnelID)
		if err != nil {
			if opts.JSON {
				_ = enc.Encode(map[string]interface{}{"time": time.Now().Format(time.RFC3339), "error": err.Error()})
			}
			return err
		}
		if opts.JSON {
			if err := enc.Encode(map[string]interface{}{"time": time.Now().Format(time.RFC3339), "payload": payload}); err != nil {
				return err
			}
		} else {
			printInspect(cmd, payload)
		}
		if remaining > 0 {
			remaining--
			if remaining == 0 {
				return nil
			}
		}
	}
}

func collectInspectPayload(tunnelID string) (*inspectPayload, error) {
	return collectInspectPayloadWithContext(context.Background(), tunnelID)
}

func collectInspectPayloadWithContext(ctx context.Context, tunnelID string) (*inspectPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	_ = refreshSessionFromRemote(ctx, sess)

	snapshot := classifySession(*sess, true)
	payload := &inspectPayload{
		TunnelID:                    sess.TunnelID,
		Status:                      snapshot.Status,
		Mode:                        valueOr(sess.Mode, "foreground"),
		Region:                      sess.Region,
		Namespace:                   sess.Namespace,
		Protocol:                    sess.Protocol,
		Host:                        sess.Host,
		SealosHost:                  sess.SealosHost,
		CustomDomain:                sess.CustomDomain,
		PublicPort:                  sess.PublicPort,
		LocalPort:                   sess.LocalPort,
		TargetURL:                   sessionTargetLabel(*sess),
		TargetTLSInsecureSkipVerify: targetTLSInsecureSkipVerifyEnabled(sess.TargetTLS),
		BasicAuth:                   inspectBasicAuthFromSession(sess.BasicAuth),
		AccessPolicy:                inspectAccessPolicyFromSession(sess.AccessPolicy),
		TTL:                         sess.TTL,
		ExpiresAt:                   formatAuthTime(sess.ExpiresAt),
		PID:                         sess.PID,
		ProcessAlive:                snapshot.ProcessAlive,
		LocalPortReachable:          snapshot.LocalPortReachable,
		CreatedAt:                   formatAuthTime(sess.CreatedAt),
		Resources:                   sess.Resources,
		LastError:                   sess.LastError,
	}

	if payload.Status == "stale" {
		payload.Warnings = append(payload.Warnings, "session is stale and may need cleanup")
	}
	if payload.Status == "degraded" {
		payload.Warnings = append(payload.Warnings, "tunnel process is alive but local port is not reachable")
	}
	if payload.Status == "connecting" {
		payload.Warnings = append(payload.Warnings, "tunnel is still connecting in daemon mode")
	}
	if payload.Status == "error" {
		payload.Warnings = append(payload.Warnings, "tunnel is in error state and the daemon will keep retrying")
	}
	if !payload.ProcessAlive && payload.PID > 0 {
		payload.Warnings = append(payload.Warnings, "recorded process is no longer running")
	}
	if inspectRemote || inspectMetrics {
		remoteCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		remote, remoteErr := collectRemoteDiagnosticsWithContext(remoteCtx, *sess)
		cancel()

		if remoteErr != nil {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote diagnostics unavailable: %v", remoteErr))
			if inspectMetrics {
				metrics, metricsErr := collectMetricsPayloadForSession(ctx, *sess)
				if metricsErr == nil {
					payload.Metrics = metrics
					payload.Warnings = append(payload.Warnings, metrics.Warnings...)
				} else {
					payload.Warnings = append(payload.Warnings, fmt.Sprintf("metrics unavailable: %v", metricsErr))
				}
			}
		} else {
			if inspectRemote {
				payload.Remote = remote
				payload.Warnings = append(payload.Warnings, remote.Warnings...)
			}
			if inspectMetrics {
				metrics, metricsErr := collectMetricsPayloadForSession(ctx, *sess)
				if metricsErr == nil {
					metrics.Remote = remoteMetricsFromDiagnostics(remote)
					payload.Metrics = metrics
					payload.Warnings = append(payload.Warnings, metrics.Warnings...)
				} else {
					payload.Warnings = append(payload.Warnings, fmt.Sprintf("metrics unavailable: %v", metricsErr))
				}
			}
		}
	}
	if inspectResources {
		resources, resourcesErr := collectTunnelResources(ctx, tunnelID)
		if resourcesErr != nil {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("kubernetes resources unavailable: %v", resourcesErr))
		} else {
			payload.KubernetesResources = resources
			payload.Warnings = append(payload.Warnings, resources.Warnings...)
		}
	}

	return payload, nil
}

func collectRemoteDiagnosticsWithContext(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelDiagnostics, error) {
	client, err := k8sClientForSession(sess)
	if err != nil {
		return nil, err
	}
	return collectRemoteDiagnosticsWithClient(ctx, sess, client)
}

func collectRemoteDiagnosticsWithClient(ctx context.Context, sess session.TunnelSession, client *k8s.Client) (*k8s.TunnelDiagnostics, error) {
	if client == nil {
		return nil, fmt.Errorf("kubernetes client is unavailable")
	}
	namespacedClient := client.WithNamespace(sess.Namespace)
	return namespacedClient.DiagnoseTunnelWithOptions(ctx, sess.TunnelID, k8s.TunnelOptions{
		CustomDomain: sess.CustomDomain,
		SealosHost:   sessionSealosHostForDomain(sess, namespacedClient.SealosHost(sess.TunnelID)),
	})
}

func printInspect(cmd *cobra.Command, payload *inspectPayload) {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Sealtun Tunnel")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Status: %s\n", payload.Status)
	fmt.Fprintf(out, "  Mode: %s\n", valueOr(payload.Mode, "unknown"))
	endpoint := endpointDisplay(payload.Protocol, payload.Host, payload.SealosHost, payload.PublicPort)
	if endpoint.Kind == "ssh" {
		fmt.Fprintf(out, "  Public SSH host: %s\n", valueOr(endpoint.Host, "unknown"))
		if endpoint.Port != 0 {
			fmt.Fprintf(out, "  Public SSH port: %d\n", endpoint.Port)
		}
		if endpoint.Command != "" {
			fmt.Fprintf(out, "  SSH command: %s\n", endpoint.Command)
		}
		if endpoint.ControlHost != "" && endpoint.ControlHost != endpoint.Host {
			fmt.Fprintf(out, "  Control host: %s\n", endpoint.ControlHost)
		}
	} else if endpoint.Kind == "tcp" {
		fmt.Fprintf(out, "  Public TCP host: %s\n", valueOr(endpoint.Host, "unknown"))
		if endpoint.Port != 0 {
			fmt.Fprintf(out, "  Public TCP port: %d\n", endpoint.Port)
			fmt.Fprintf(out, "  Public TCP endpoint: %s\n", endpointLabel(payload.Protocol, payload.Host, payload.SealosHost, payload.PublicPort))
		}
		if endpoint.ControlHost != "" && endpoint.ControlHost != endpoint.Host {
			fmt.Fprintf(out, "  Control host: %s\n", endpoint.ControlHost)
		}
	} else {
		fmt.Fprintf(out, "  Public URL: %s\n", valueOr(endpoint.URL, "unknown"))
	}
	if payload.SealosHost != "" {
		fmt.Fprintf(out, "  Sealos host: %s\n", payload.SealosHost)
	}
	if payload.CustomDomain != "" {
		fmt.Fprintf(out, "  Custom domain: %s\n", payload.CustomDomain)
		fmt.Fprintf(out, "  DNS CNAME target: %s\n", valueOr(payload.SealosHost, payload.Host))
	}
	fmt.Fprintf(out, "  Target: %s\n", valueOr(payload.TargetURL, sessionTargetLabel(session.TunnelSession{Protocol: payload.Protocol, LocalPort: payload.LocalPort})))
	if payload.TargetTLSInsecureSkipVerify {
		fmt.Fprintln(out, "  Target TLS: certificate verification disabled")
	}
	if payload.BasicAuth != nil && payload.BasicAuth.Enabled {
		fmt.Fprintf(out, "  Basic Auth: enabled")
		if payload.BasicAuth.Username != "" {
			fmt.Fprintf(out, " (user: %s)", payload.BasicAuth.Username)
		}
		fmt.Fprintln(out)
	}
	if payload.AccessPolicy != nil {
		fmt.Fprintln(out, "  Access policy: enabled")
		if len(payload.AccessPolicy.IPAllowlist) > 0 {
			fmt.Fprintf(out, "  IP allowlist: %s\n", strings.Join(payload.AccessPolicy.IPAllowlist, ", "))
		}
		if len(payload.AccessPolicy.IPDenylist) > 0 {
			fmt.Fprintf(out, "  IP denylist: %s\n", strings.Join(payload.AccessPolicy.IPDenylist, ", "))
		}
		if payload.AccessPolicy.BearerTokens > 0 {
			fmt.Fprintf(out, "  Bearer tokens: %d configured\n", payload.AccessPolicy.BearerTokens)
		}
		if payload.AccessPolicy.TemporaryLinks > 0 {
			fmt.Fprintf(out, "  Temporary links: %d configured\n", payload.AccessPolicy.TemporaryLinks)
		}
		if payload.AccessPolicy.RateLimit != "" {
			fmt.Fprintf(out, "  Rate limit: %s\n", payload.AccessPolicy.RateLimit)
		}
		if payload.AccessPolicy.AuditEnabled {
			fmt.Fprintln(out, "  Access audit: enabled")
		}
	}
	if payload.ExpiresAt != "" {
		if payload.TTL != "" {
			fmt.Fprintf(out, "  TTL: %s\n", payload.TTL)
		}
		fmt.Fprintf(out, "  Expires at: %s\n", payload.ExpiresAt)
	}
	fmt.Fprintf(out, "  Protocol: %s\n", valueOr(payload.Protocol, "unknown"))
	fmt.Fprintf(out, "  Namespace: %s\n", valueOr(payload.Namespace, "unknown"))
	fmt.Fprintf(out, "  Region: %s\n", valueOr(payload.Region, "unknown"))
	fmt.Fprintf(out, "  PID: %d\n", payload.PID)
	fmt.Fprintf(out, "  Process alive: %s\n", yesNo(payload.ProcessAlive))
	fmt.Fprintf(out, "  Target reachable: %s\n", yesNo(payload.LocalPortReachable))
	fmt.Fprintf(out, "  Created at: %s\n", valueOr(payload.CreatedAt, "unknown"))

	if len(payload.Resources) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Resources")
		for _, resource := range payload.Resources {
			fmt.Fprintf(out, "  - %s\n", resource)
		}
	}

	if payload.LastError != "" {
		fmt.Fprintln(out, "")
		fmt.Fprintf(out, "Last error: %s\n", payload.LastError)
	}

	if payload.Remote != nil {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Remote")
		fmt.Fprintf(out, "  Deployment: %s", yesNo(payload.Remote.Deployment.Exists))
		if payload.Remote.Deployment.Exists {
			fmt.Fprintf(out, " (%d/%d ready)", payload.Remote.Deployment.ReadyReplicas, payload.Remote.Deployment.DesiredReplicas)
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  Service: %s\n", yesNo(payload.Remote.Service.Exists))
		fmt.Fprintf(out, "  Ingress: %s\n", yesNo(payload.Remote.Ingress.Exists))
		if payload.Remote.Certificate != nil {
			fmt.Fprintf(out, "  Certificate: %s", yesNo(payload.Remote.Certificate.Exists))
			if payload.Remote.Certificate.Exists {
				fmt.Fprintf(out, " (ready=%s)", yesNo(payload.Remote.Certificate.Ready))
			}
			fmt.Fprintln(out)
		}
		if len(payload.Remote.Pods) > 0 {
			fmt.Fprintln(out, "  Pods:")
			for _, pod := range payload.Remote.Pods {
				fmt.Fprintf(out, "    - %s phase=%s ready=%s restarts=%d", pod.Name, pod.Phase, yesNo(pod.Ready), pod.RestartCount)
				if pod.Reason != "" {
					fmt.Fprintf(out, " reason=%s", pod.Reason)
				}
				fmt.Fprintln(out)
			}
		}
		if len(payload.Remote.Events) > 0 {
			fmt.Fprintln(out, "  Recent events:")
			for _, event := range payload.Remote.Events {
				when := valueOr(event.LastTimestamp, event.FirstTimestamp)
				if when != "" {
					fmt.Fprintf(out, "    - %s %s %s: %s\n", when, valueOr(event.Type, "-"), valueOr(event.Reason, "-"), valueOr(event.Message, "-"))
				} else {
					fmt.Fprintf(out, "    - %s %s: %s\n", valueOr(event.Type, "-"), valueOr(event.Reason, "-"), valueOr(event.Message, "-"))
				}
			}
		}
	}

	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}

	if payload.Metrics != nil && payload.Metrics.Remote != nil {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Metrics")
		fmt.Fprintf(out, "  Deployment ready: %s\n", valueOr(payload.Metrics.Remote.DeploymentReady, "-"))
		fmt.Fprintf(out, "  Pods: %d total, %d ready, %d restarts\n", payload.Metrics.Remote.PodCount, payload.Metrics.Remote.ReadyPods, payload.Metrics.Remote.RestartCount)
		if len(payload.Metrics.Server) > 0 {
			fmt.Fprintln(out, "  Server counters:")
			for _, key := range []string{"clientConnected", "totalRequests", "activeRequests", "totalResponseBytes", "total5xx", "lastStatus", "lastRequestAt", "averageDurationMs", "totalTCPConnections", "activeTCPConnections", "totalTCPBytes", "totalTCPErrors", "lastTCPConnectedAt"} {
				if value, ok := payload.Metrics.Server[key]; ok {
					fmt.Fprintf(out, "    %s: %v\n", key, value)
				}
			}
		}
	}

	if payload.KubernetesResources != nil {
		printTunnelResources(cmd, payload.KubernetesResources)
	}
}

func inspectBasicAuthFromSession(config *session.BasicAuthConfig) *inspectBasicAuth {
	if config == nil || !config.Enabled {
		return nil
	}
	return &inspectBasicAuth{Enabled: true, Username: config.Username}
}

func inspectAccessPolicyFromSession(config *session.AccessPolicy) *inspectAccessPolicy {
	if config == nil {
		return nil
	}
	payload := &inspectAccessPolicy{
		BearerTokens:   len(config.BearerTokenHashes),
		IPAllowlist:    append([]string(nil), config.IPAllowlist...),
		IPDenylist:     append([]string(nil), config.IPDenylist...),
		TemporaryLinks: len(config.TemporaryTokens),
		RateLimit:      config.RateLimit,
		AuditEnabled:   config.Audit != nil && config.Audit.Enabled,
	}
	if payload.BearerTokens == 0 && payload.TemporaryLinks == 0 && len(payload.IPAllowlist) == 0 && len(payload.IPDenylist) == 0 && payload.RateLimit == "" && !payload.AuditEnabled {
		return nil
	}
	return payload
}
