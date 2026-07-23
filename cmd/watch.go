package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type watchOptions struct {
	JSON     bool
	Interval time.Duration
	Count    int
}

type watchEvent struct {
	Time   string               `json:"time"`
	Type   string               `json:"type"`
	Tunnel *tunnelDoctorPayload `json:"tunnel,omitempty"`
	Global *doctorPayload       `json:"global,omitempty"`
	Error  string               `json:"error,omitempty"`
}

var watchOpts watchOptions

var watchTunnelCollector = collectTunnelDoctorPayload
var watchGlobalCollector = collectDoctorPayloadWithContext

var watchCmd = &cobra.Command{
	Use:          "watch [tunnel-id]",
	Short:        "Watch Sealtun tunnel status in real time",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if watchOpts.Interval <= 0 {
			return fmt.Errorf("--interval must be greater than 0")
		}
		if watchOpts.Count < 0 {
			return fmt.Errorf("--count must be greater than or equal to 0")
		}
		tunnelID := ""
		if len(args) > 0 {
			tunnelID = args[0]
		}
		return runWatch(cmd, tunnelID, watchOpts)
	},
}

func init() {
	rootCmd.AddCommand(watchCmd)
	markAlpha(watchCmd)
	watchCmd.Flags().BoolVar(&watchOpts.JSON, "json", false, "Output watch events as newline-delimited JSON")
	watchCmd.Flags().DurationVar(&watchOpts.Interval, "interval", 3*time.Second, "Refresh interval")
	watchCmd.Flags().IntVar(&watchOpts.Count, "count", 0, "Stop after N refreshes; 0 watches until interrupted")
}

func runWatch(cmd *cobra.Command, tunnelID string, opts watchOptions) error {
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
		event := collectWatchEvent(cmd.Context(), tunnelID)
		if opts.JSON {
			if err := enc.Encode(event); err != nil {
				return err
			}
		} else {
			printWatchEvent(cmd, event)
		}
		if event.Error != "" && !opts.JSON {
			fmt.Fprintf(cmd.ErrOrStderr(), "[!] %s\n", event.Error)
		}
		if event.Error != "" {
			return errors.New(event.Error)
		}
		if remaining > 0 {
			remaining--
			if remaining == 0 {
				return nil
			}
		}
	}
}

func collectWatchEvent(ctx context.Context, tunnelID string) watchEvent {
	event := watchEvent{
		Time: time.Now().Format(time.RFC3339),
	}
	if tunnelID != "" {
		event.Type = "tunnel"
		payload, err := watchTunnelCollector(ctx, tunnelID)
		if err != nil {
			event.Error = err.Error()
			return event
		}
		event.Tunnel = payload
		return event
	}
	event.Type = "summary"
	payload, err := watchGlobalCollector(ctx)
	if err != nil {
		event.Error = err.Error()
		return event
	}
	event.Global = payload
	return event
}

func printWatchEvent(cmd *cobra.Command, event watchEvent) {
	out := cmd.OutOrStdout()
	if event.Tunnel != nil {
		tunnel := event.Tunnel
		remote := "unknown"
		if tunnel.Remote != nil {
			remote = fmt.Sprintf("%d/%d deployment ready", tunnel.Remote.Deployment.ReadyReplicas, tunnel.Remote.Deployment.DesiredReplicas)
		}
		domain := "none"
		if tunnel.Remote != nil && tunnel.Remote.Certificate != nil {
			domain = "certificate pending"
			if tunnel.Remote.Certificate.Ready {
				domain = "certificate ready"
			}
		}
		fmt.Fprintf(out, "%s  %s  status=%s local=%s reachable=%s remote=%s domain=%s endpoint=%s\n",
			event.Time,
			tunnel.TunnelID,
			tunnel.Status,
			valueOr(tunnel.LocalTarget, "-"),
			yesNo(tunnel.LocalPortReachable),
			remote,
			domain,
			valueOr(tunnel.Endpoint, "-"),
		)
		return
	}
	if event.Global != nil {
		global := event.Global
		fmt.Fprintf(out, "%s  sessions=%d active=%d degraded=%d stopped=%d stale=%d remoteIssues=%d daemon=%s\n",
			event.Time,
			global.TotalSessions,
			global.ActiveSessions,
			global.DegradedSessions,
			global.StoppedSessions,
			global.StaleSessions,
			global.RemoteIssues,
			yesNo(global.DaemonRunning),
		)
	}
}
