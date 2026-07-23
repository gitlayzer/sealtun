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

type eventsPayload struct {
	TunnelID string                `json:"tunnelId"`
	Events   []k8s.EventDiagnostic `json:"events,omitempty"`
	Warnings []string              `json:"warnings,omitempty"`
}

var eventsJSON bool
var eventsTimeout time.Duration

var eventsCmd = &cobra.Command{
	Use:          "events [tunnel-id]",
	Short:        "Show recent Kubernetes events for a tunnel",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if eventsTimeout <= 0 {
			return fmt.Errorf("--timeout must be greater than 0")
		}
		payload, err := collectEventsPayloadWithContext(cmd.Context(), args[0], eventsTimeout)
		if payload != nil {
			if eventsJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if encErr := enc.Encode(payload); encErr != nil {
					return encErr
				}
			} else {
				printEvents(cmd, payload)
			}
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(eventsCmd)
	markAlpha(eventsCmd)
	eventsCmd.Flags().BoolVar(&eventsJSON, "json", false, "Output events as JSON")
	eventsCmd.Flags().DurationVar(&eventsTimeout, "timeout", 8*time.Second, "Maximum time to wait for remote event diagnostics")
}

func collectEventsPayloadWithContext(ctx context.Context, tunnelID string, timeout time.Duration) (*eventsPayload, error) {
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	return collectEventsPayloadForSession(ctx, *sess, timeout)
}

func collectEventsPayloadForSession(ctx context.Context, sess session.TunnelSession, timeout time.Duration) (*eventsPayload, error) {
	remoteCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	diag, err := collectRemoteDiagnosticsWithContext(remoteCtx, sess)
	payload := &eventsPayload{TunnelID: sess.TunnelID}
	if err != nil {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote diagnostics unavailable: %v", err))
		return payload, err
	}
	payload.Events = diag.Events
	for _, warning := range diag.Warnings {
		if strings.Contains(warning, "event") {
			payload.Warnings = append(payload.Warnings, warning)
		}
	}
	return payload, nil
}

func printEvents(cmd *cobra.Command, payload *eventsPayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Events")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	if len(payload.Events) == 0 {
		fmt.Fprintln(out, "  No recent remote events found.")
	}
	for _, event := range payload.Events {
		when := valueOr(event.LastTimestamp, event.FirstTimestamp)
		if when == "" {
			when = "unknown time"
		}
		count := ""
		if event.Count > 1 {
			count = fmt.Sprintf(" x%d", event.Count)
		}
		fmt.Fprintf(out, "  - %s [%s/%s%s] %s: %s\n", when, valueOr(event.Type, "Normal"), valueOr(event.Reason, "Event"), count, valueOr(event.Object, "-"), event.Message)
	}
	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
}
