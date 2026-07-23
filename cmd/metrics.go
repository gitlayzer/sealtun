package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type metricsPayload struct {
	TunnelID       string                 `json:"tunnelId"`
	Status         string                 `json:"status"`
	Host           string                 `json:"host"`
	SealosHost     string                 `json:"sealosHost,omitempty"`
	CustomDomain   string                 `json:"customDomain,omitempty"`
	LocalPort      string                 `json:"localPort"`
	TargetURL      string                 `json:"targetUrl,omitempty"`
	ProcessAlive   bool                   `json:"processAlive"`
	LocalReachable bool                   `json:"localReachable"`
	Remote         *remoteMetricsPayload  `json:"remote,omitempty"`
	Server         map[string]interface{} `json:"server,omitempty"`
	Warnings       []string               `json:"warnings,omitempty"`
}

type remoteMetricsPayload struct {
	DeploymentReady string   `json:"deploymentReady"`
	ServiceExists   bool     `json:"serviceExists"`
	IngressHosts    []string `json:"ingressHosts,omitempty"`
	PodCount        int      `json:"podCount"`
	ReadyPods       int      `json:"readyPods"`
	RestartCount    int32    `json:"restartCount"`
	Warnings        []string `json:"warnings,omitempty"`
}

var metricsJSON bool
var metricsRemote bool
var metricsServer bool

const metricsResponseMaxBytes = 1 << 20

var metricsCmd = &cobra.Command{
	Use:          "metrics [tunnel-id]",
	Short:        "Show tunnel runtime metrics",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := collectMetricsPayloadWithContext(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if metricsJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printMetrics(cmd, payload)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(metricsCmd)
	markAlpha(metricsCmd)
	metricsCmd.Flags().BoolVar(&metricsJSON, "json", false, "Output metrics as JSON")
	metricsCmd.Flags().BoolVar(&metricsRemote, "remote", true, "Include Kubernetes readiness metrics")
	metricsCmd.Flags().BoolVar(&metricsServer, "server", true, "Include tunnel server request counters when supported by the remote pod")
}

func collectMetricsPayloadWithContext(ctx context.Context, tunnelID string) (*metricsPayload, error) {
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	return collectMetricsPayloadForSession(ctx, *sess)
}

func collectMetricsPayloadForSession(ctx context.Context, sess session.TunnelSession) (*metricsPayload, error) {
	snapshot := classifySession(sess, true)
	payload := &metricsPayload{
		TunnelID:       sess.TunnelID,
		Status:         snapshot.Status,
		Host:           sess.Host,
		SealosHost:     sess.SealosHost,
		CustomDomain:   sess.CustomDomain,
		LocalPort:      sess.LocalPort,
		TargetURL:      sessionTargetLabel(sess),
		ProcessAlive:   snapshot.ProcessAlive,
		LocalReachable: snapshot.LocalPortReachable,
	}

	if metricsRemote {
		remoteCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		remote, err := collectRemoteDiagnosticsWithContext(remoteCtx, sess)
		if err != nil {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote metrics unavailable: %v", err))
		} else {
			payload.Remote = remoteMetricsFromDiagnostics(remote)
			payload.Warnings = append(payload.Warnings, remote.Warnings...)
		}
	}
	if metricsServer {
		serverMetrics, err := fetchServerMetrics(ctx, sess)
		if err != nil {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("server request counters unavailable: %v", err))
		} else {
			payload.Server = serverMetrics
		}
	}
	return payload, nil
}

func remoteMetricsFromDiagnostics(diag *k8s.TunnelDiagnostics) *remoteMetricsPayload {
	payload := &remoteMetricsPayload{
		DeploymentReady: fmt.Sprintf("%d/%d", diag.Deployment.ReadyReplicas, diag.Deployment.DesiredReplicas),
		ServiceExists:   diag.Service.Exists,
		IngressHosts:    diag.Ingress.Hosts,
		PodCount:        len(diag.Pods),
		Warnings:        append([]string{}, diag.Warnings...),
	}
	for _, pod := range diag.Pods {
		if pod.Ready {
			payload.ReadyPods++
		}
		payload.RestartCount += pod.RestartCount
	}
	return payload
}

func fetchServerMetrics(ctx context.Context, sess session.TunnelSession) (map[string]interface{}, error) {
	host, err := normalizePublicHostname(sessionControlHost(sess))
	if err != nil {
		return nil, fmt.Errorf("invalid session metrics host: %w", err)
	}
	if sess.Secret == "" {
		return nil, fmt.Errorf("session secret is unavailable")
	}
	client := newMetricsHTTPClient()
	metricsURL := (&url.URL{Scheme: "https", Host: host, Path: "/_sealtun/metrics"}).String()
	probeReq, err := http.NewRequestWithContext(ctx, http.MethodHead, metricsURL, nil) // #nosec G107 -- host is validated as a DNS hostname before constructing the URL.
	if err != nil {
		return nil, err
	}
	probeResp, err := client.Do(probeReq)
	if err != nil {
		return nil, err
	}
	_ = probeResp.Body.Close()
	if probeResp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("remote server metrics endpoint is not available yet; upgrade the remote tunnel image")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil) // #nosec G107 -- host is validated as a DNS hostname before constructing the URL.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+sess.Secret)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote endpoint returned %s", resp.Status)
	}
	var payload map[string]interface{}
	if err := decodeMetricsJSON(resp.Body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeMetricsJSON(r io.Reader, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r, metricsResponseMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > metricsResponseMaxBytes {
		return fmt.Errorf("metrics response exceeds %d bytes", metricsResponseMaxBytes)
	}
	return json.Unmarshal(body, v)
}

func newMetricsHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func printMetrics(cmd *cobra.Command, payload *metricsPayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Metrics")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Status: %s\n", payload.Status)
	fmt.Fprintf(out, "  Host: %s\n", valueOr(payload.Host, "unknown"))
	if payload.SealosHost != "" {
		fmt.Fprintf(out, "  Sealos host: %s\n", payload.SealosHost)
	}
	if payload.CustomDomain != "" {
		fmt.Fprintf(out, "  Custom domain: %s\n", payload.CustomDomain)
	}
	fmt.Fprintf(out, "  Target: %s\n", valueOr(payload.TargetURL, "unknown"))
	fmt.Fprintf(out, "  Process alive: %s\n", yesNo(payload.ProcessAlive))
	fmt.Fprintf(out, "  Target reachable: %s\n", yesNo(payload.LocalReachable))

	if payload.Remote != nil {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Remote")
		fmt.Fprintf(out, "  Deployment ready: %s\n", payload.Remote.DeploymentReady)
		fmt.Fprintf(out, "  Service exists: %s\n", yesNo(payload.Remote.ServiceExists))
		fmt.Fprintf(out, "  Pods: %d total, %d ready, %d restarts\n", payload.Remote.PodCount, payload.Remote.ReadyPods, payload.Remote.RestartCount)
		if len(payload.Remote.IngressHosts) > 0 {
			fmt.Fprintf(out, "  Ingress hosts: %s\n", strings.Join(payload.Remote.IngressHosts, ", "))
		}
	}

	if len(payload.Server) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Server counters")
		for _, key := range []string{"clientConnected", "totalRequests", "activeRequests", "totalResponseBytes", "total5xx", "lastStatus", "lastRequestAt", "averageDurationMs", "totalTCPConnections", "activeTCPConnections", "totalTCPBytes", "totalTCPErrors", "lastTCPConnectedAt"} {
			if value, ok := payload.Server[key]; ok {
				fmt.Fprintf(out, "  %s: %v\n", key, value)
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
}
