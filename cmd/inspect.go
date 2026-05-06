package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type inspectPayload struct {
	TunnelID           string                 `json:"tunnelId"`
	Status             string                 `json:"status"`
	Mode               string                 `json:"mode,omitempty"`
	Region             string                 `json:"region,omitempty"`
	Namespace          string                 `json:"namespace,omitempty"`
	Protocol           string                 `json:"protocol,omitempty"`
	Host               string                 `json:"host,omitempty"`
	LocalPort          string                 `json:"localPort,omitempty"`
	PID                int                    `json:"pid"`
	ProcessAlive       bool                   `json:"processAlive"`
	LocalPortReachable bool                   `json:"localPortReachable"`
	CreatedAt          string                 `json:"createdAt,omitempty"`
	Resources          []string               `json:"resources,omitempty"`
	LastError          string                 `json:"lastError,omitempty"`
	Remote             *k8s.TunnelDiagnostics `json:"remote,omitempty"`
	Warnings           []string               `json:"warnings,omitempty"`
}

var inspectJSON bool
var inspectRemote bool

var inspectCmd = &cobra.Command{
	Use:   "inspect [tunnel-id]",
	Short: "Inspect a local Sealtun tunnel session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := collectInspectPayload(args[0])
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
	inspectCmd.Flags().BoolVar(&inspectRemote, "remote", false, "Include best-effort remote Kubernetes diagnostics")
}

func collectInspectPayload(tunnelID string) (*inspectPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}

	processAlive := sessionOwnerAlive(*sess)
	portReachable := localPortReachable(sess.LocalPort)
	payload := &inspectPayload{
		TunnelID:           sess.TunnelID,
		Status:             sessionState(*sess),
		Mode:               valueOr(sess.Mode, "foreground"),
		Region:             sess.Region,
		Namespace:          sess.Namespace,
		Protocol:           sess.Protocol,
		Host:               sess.Host,
		LocalPort:          sess.LocalPort,
		PID:                sess.PID,
		ProcessAlive:       processAlive,
		LocalPortReachable: portReachable,
		CreatedAt:          formatAuthTime(sess.CreatedAt),
		Resources:          sess.Resources,
		LastError:          sess.LastError,
	}
	if payload.Status == "active" && payload.ProcessAlive && !payload.LocalPortReachable {
		payload.Status = "degraded"
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
	if inspectRemote {
		if remote, err := collectRemoteDiagnostics(*sess); err != nil {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote diagnostics unavailable: %v", err))
		} else {
			payload.Remote = remote
			payload.Warnings = append(payload.Warnings, remote.Warnings...)
		}
	}

	return payload, nil
}

func collectRemoteDiagnostics(sess session.TunnelSession) (*k8s.TunnelDiagnostics, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return collectRemoteDiagnosticsWithContext(ctx, sess)
}

func collectRemoteDiagnosticsWithContext(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelDiagnostics, error) {
	client, err := k8sClientForSession(sess)
	if err != nil {
		return nil, err
	}
	return client.WithNamespace(sess.Namespace).DiagnoseTunnel(ctx, sess.TunnelID)
}

func printInspect(cmd *cobra.Command, payload *inspectPayload) {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Sealtun Tunnel")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Status: %s\n", payload.Status)
	fmt.Fprintf(out, "  Mode: %s\n", valueOr(payload.Mode, "unknown"))
	fmt.Fprintf(out, "  Host: %s\n", valueOr(payload.Host, "unknown"))
	fmt.Fprintf(out, "  Local port: %s\n", valueOr(payload.LocalPort, "unknown"))
	fmt.Fprintf(out, "  Protocol: %s\n", valueOr(payload.Protocol, "unknown"))
	fmt.Fprintf(out, "  Namespace: %s\n", valueOr(payload.Namespace, "unknown"))
	fmt.Fprintf(out, "  Region: %s\n", valueOr(payload.Region, "unknown"))
	fmt.Fprintf(out, "  PID: %d\n", payload.PID)
	fmt.Fprintf(out, "  Process alive: %s\n", yesNo(payload.ProcessAlive))
	fmt.Fprintf(out, "  Local port reachable: %s\n", yesNo(payload.LocalPortReachable))
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
				fmt.Fprintf(out, "    - %s %s: %s\n", valueOr(event.Type, "-"), valueOr(event.Reason, "-"), valueOr(event.Message, "-"))
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
