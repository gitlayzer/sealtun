package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

const doctorRemoteTimeout = 12 * time.Second
const doctorRemoteConcurrency = 4

type doctorPayload struct {
	DaemonRunning        bool     `json:"daemonRunning"`
	LoggedIn             bool     `json:"loggedIn"`
	KubeconfigPresent    bool     `json:"kubeconfigPresent"`
	TotalSessions        int      `json:"totalSessions"`
	ActiveSessions       int      `json:"activeSessions"`
	ConnectingSessions   int      `json:"connectingSessions"`
	ErrorSessions        int      `json:"errorSessions"`
	DegradedSessions     int      `json:"degradedSessions"`
	StoppedSessions      int      `json:"stoppedSessions"`
	StaleSessions        int      `json:"staleSessions"`
	ReachableActivePorts int      `json:"reachableActivePorts"`
	RemoteChecked        int      `json:"remoteChecked"`
	RemoteIssues         int      `json:"remoteIssues"`
	Warnings             []string `json:"warnings,omitempty"`
}

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run local Sealtun diagnostics",
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := collectDoctorPayload()
		if err != nil {
			return err
		}

		if doctorJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}

		printDoctor(cmd, payload)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output diagnostics as JSON")
}

func collectDoctorPayload() (*doctorPayload, error) {
	status, err := collectStatus()
	if err != nil {
		return nil, err
	}

	items, err := collectListItemsWithLocalCheck(true)
	if err != nil {
		return nil, err
	}

	payload := &doctorPayload{
		DaemonRunning:     status.DaemonRunning,
		LoggedIn:          status.LoggedIn,
		KubeconfigPresent: status.Kubeconfig.Present,
		TotalSessions:     len(items),
		Warnings:          append([]string{}, status.Warnings...),
	}

	for _, item := range items {
		switch item.Status {
		case "active":
			payload.ActiveSessions++
			payload.ReachableActivePorts++
		case "degraded":
			payload.DegradedSessions++
		case "connecting":
			payload.ConnectingSessions++
		case "error":
			payload.ErrorSessions++
		case "stopped":
			payload.StoppedSessions++
		default:
			payload.StaleSessions++
		}
	}
	runDoctorRemoteDiagnostics(payload, items)

	if payload.TotalSessions == 0 {
		payload.Warnings = append(payload.Warnings, "no local tunnel sessions found")
	}
	daemonManaged := 0
	for _, item := range items {
		if item.Mode == "daemon" {
			daemonManaged++
		}
	}
	if daemonManaged > 0 && !payload.DaemonRunning {
		payload.Warnings = append(payload.Warnings, "daemon is not running; daemon-managed tunnels will not reconnect until it starts")
	}
	if payload.StaleSessions > 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("%d stale tunnel session(s) found; consider running `sealtun cleanup`", payload.StaleSessions))
	}
	if payload.StoppedSessions > 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("%d stopped tunnel session(s) found; consider running `sealtun cleanup`", payload.StoppedSessions))
	}
	if payload.ConnectingSessions > 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("%d tunnel session(s) are still connecting", payload.ConnectingSessions))
	}
	if payload.ErrorSessions > 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("%d tunnel session(s) are in error state; inspect them for the last error", payload.ErrorSessions))
	}
	if payload.DegradedSessions > 0 {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("%d tunnel session(s) have a live owner but unreachable local port", payload.DegradedSessions))
	}

	return payload, nil
}

func runDoctorRemoteDiagnostics(payload *doctorPayload, items []listItem) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorRemoteTimeout)
	defer cancel()

	type result struct {
		tunnelID string
		checked  bool
		warnings []string
		err      error
	}

	jobs := make(chan listItem, len(items))
	results := make(chan result, len(items))
	var wg sync.WaitGroup
	queued := 0

	workerCount := doctorRemoteConcurrency
	if workerCount < 1 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				sess, err := findSession(item.TunnelID)
				if err != nil {
					select {
					case results <- result{tunnelID: item.TunnelID, err: fmt.Errorf("session disappeared during diagnostics: %w", err)}:
					case <-ctx.Done():
					}
					continue
				}
				remote, err := collectRemoteDiagnosticsWithContext(ctx, *sess)
				if err != nil {
					if ctx.Err() != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
						return
					}
					select {
					case results <- result{tunnelID: item.TunnelID, err: fmt.Errorf("remote diagnostics unavailable: %w", err)}:
					case <-ctx.Done():
					}
					continue
				}
				select {
				case results <- result{tunnelID: item.TunnelID, checked: true, warnings: remote.Warnings}:
				case <-ctx.Done():
				}
			}
		}()
	}

enqueue:
	for _, item := range items {
		if item.Status == "stopped" || item.Status == "stale" {
			continue
		}
		select {
		case jobs <- item:
			queued++
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		if result.checked {
			payload.RemoteChecked++
		}
		if result.err != nil {
			if ctx.Err() != nil && (errors.Is(result.err, context.DeadlineExceeded) || errors.Is(result.err, context.Canceled)) {
				continue
			}
			payload.RemoteIssues++
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("tunnel %s %v", result.tunnelID, result.err))
			continue
		}
		if len(result.warnings) > 0 {
			payload.RemoteIssues++
			for _, warning := range result.warnings {
				payload.Warnings = append(payload.Warnings, fmt.Sprintf("tunnel %s: %s", result.tunnelID, warning))
			}
		}
	}

	if queued > 0 && ctx.Err() != nil {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote diagnostics stopped after %s; some tunnels may not have been checked", doctorRemoteTimeout))
	}
}

func printDoctor(cmd *cobra.Command, payload *doctorPayload) {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Sealtun Doctor")
	fmt.Fprintf(out, "  Daemon running: %s\n", yesNo(payload.DaemonRunning))
	fmt.Fprintf(out, "  Logged in: %s\n", yesNo(payload.LoggedIn))
	fmt.Fprintf(out, "  Kubeconfig present: %s\n", yesNo(payload.KubeconfigPresent))
	fmt.Fprintf(out, "  Sessions: %d total, %d active, %d degraded, %d connecting, %d error, %d stopped, %d stale\n", payload.TotalSessions, payload.ActiveSessions, payload.DegradedSessions, payload.ConnectingSessions, payload.ErrorSessions, payload.StoppedSessions, payload.StaleSessions)
	fmt.Fprintf(out, "  Reachable active local ports: %d\n", payload.ReachableActivePorts)
	fmt.Fprintf(out, "  Remote checks: %d checked, %d with issues\n", payload.RemoteChecked, payload.RemoteIssues)

	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	} else {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "No issues detected from local diagnostics.")
	}
}
