package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

const doctorRemoteTimeout = 12 * time.Second
const doctorRemoteConcurrency = 4

var doctorReportSafePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type doctorRedactionRule struct {
	pattern *regexp.Regexp
	repl    string
}

var doctorRedactionRules = []doctorRedactionRule{
	{pattern: regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)[^\s]+(?:\s+[^\s]+)?`), repl: `${1}<redacted>`},
	{pattern: regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`), repl: `${1}<redacted>`},
	{pattern: regexp.MustCompile(`(?i)((?:token|secret|password|passwd|pwd)\s*[:=]\s*)[^\s&]+`), repl: `${1}<redacted>`},
	{pattern: regexp.MustCompile(`(?i)(_sealtun_token=)[^&\s]+`), repl: `${1}<redacted>`},
	{pattern: regexp.MustCompile(`(?i)(basic\s+)[A-Za-z0-9+/=-]+`), repl: `${1}<redacted>`},
}

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

type tunnelDoctorPayload struct {
	TunnelID           string                 `json:"tunnelId"`
	Status             string                 `json:"status"`
	Protocol           string                 `json:"protocol,omitempty"`
	Endpoint           string                 `json:"endpoint,omitempty"`
	LocalTarget        string                 `json:"localTarget,omitempty"`
	Mode               string                 `json:"mode,omitempty"`
	Region             string                 `json:"region,omitempty"`
	Namespace          string                 `json:"namespace,omitempty"`
	ProcessAlive       bool                   `json:"processAlive"`
	LocalPortReachable bool                   `json:"localPortReachable"`
	LastError          string                 `json:"lastError,omitempty"`
	Remote             *k8s.TunnelDiagnostics `json:"remote,omitempty"`
	Checks             []doctorCheck          `json:"checks"`
	Suggestions        []string               `json:"suggestions,omitempty"`
	Warnings           []string               `json:"warnings,omitempty"`
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

var doctorJSON bool
var doctorFix bool
var doctorFixDryRun bool
var doctorReport bool
var doctorReportFile string

type doctorFixPayload struct {
	DryRun  bool              `json:"dryRun"`
	Actions []doctorFixAction `json:"actions"`
}

type doctorFixAction struct {
	Action   string `json:"action"`
	TunnelID string `json:"tunnelId,omitempty"`
	Command  string `json:"command,omitempty"`
	Reason   string `json:"reason"`
	Allowed  bool   `json:"allowed"`
	Executed bool   `json:"executed,omitempty"`
	Error    string `json:"error,omitempty"`
}

var doctorFixStartTunnel = startTunnelSession
var doctorFixCleanupResources = cleanupSessionResources
var doctorFixEnsureDaemon = ensureDaemonRunning

var doctorCmd = &cobra.Command{
	Use:          "doctor [tunnel-id]",
	Short:        "Run Sealtun diagnostics",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if doctorReport && doctorFix {
			return fmt.Errorf("--report cannot be used with --fix")
		}
		if doctorReport && doctorJSON {
			return fmt.Errorf("--report cannot be used with --json")
		}
		if doctorReport && len(args) == 0 {
			return fmt.Errorf("--report requires a tunnel id")
		}
		if doctorFixDryRun && !doctorFix {
			return fmt.Errorf("--dry-run requires --fix")
		}
		if doctorFix {
			payload, err := runDoctorFix(cmd.Context(), args, doctorFixDryRun)
			if err != nil {
				return err
			}
			if doctorJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(payload); err != nil {
					return err
				}
				return doctorFixExecutionError(payload)
			}
			printDoctorFix(cmd, payload)
			return doctorFixExecutionError(payload)
		}
		if len(args) > 0 {
			payload, err := collectTunnelDoctorPayload(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if doctorReport {
				path, err := writeTunnelDoctorReport(doctorReportFile, payload)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Doctor report written to %s\n", path)
				return nil
			}
			if doctorJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			printTunnelDoctor(cmd, payload)
			return nil
		}

		payload, err := collectDoctorPayloadWithContext(cmd.Context())
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
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "Execute conservative automatic fixes")
	doctorCmd.Flags().BoolVar(&doctorFixDryRun, "dry-run", false, "Show conservative automatic fixes without executing them")
	doctorCmd.Flags().BoolVar(&doctorReport, "report", false, "Write a redacted Markdown report for a tunnel")
	doctorCmd.Flags().StringVar(&doctorReportFile, "report-file", "", "Path for --report output")
}

func collectDoctorPayload() (*doctorPayload, error) {
	return collectDoctorPayloadWithContext(context.Background())
}

func collectTunnelDoctorPayload(ctx context.Context, tunnelID string) (*tunnelDoctorPayload, error) {
	sess, err := findSessionRefreshed(ctx, tunnelID)
	if err != nil {
		return nil, err
	}
	snapshot := classifySession(*sess, true)
	endpoint := endpointLabel(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort)
	payload := &tunnelDoctorPayload{
		TunnelID:           sess.TunnelID,
		Status:             snapshot.Status,
		Protocol:           valueOr(sess.Protocol, "https"),
		Endpoint:           endpoint,
		LocalTarget:        sessionTargetLabel(*sess),
		Mode:               valueOr(sess.Mode, "foreground"),
		Region:             sess.Region,
		Namespace:          sess.Namespace,
		ProcessAlive:       snapshot.ProcessAlive,
		LocalPortReachable: snapshot.LocalPortReachable,
		LastError:          sess.LastError,
	}

	payload.Checks = append(payload.Checks,
		doctorCheck{Name: "session", Status: "ok", Detail: "local session record exists"},
		ownerDoctorCheck(*sess, snapshot.ProcessAlive),
		targetDoctorCheck(*sess, snapshot.LocalPortReachable),
	)

	remoteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	remote, err := collectRemoteDiagnosticsWithContext(remoteCtx, *sess)
	cancel()
	if err != nil {
		payload.Checks = append(payload.Checks, doctorCheck{Name: "remote", Status: "warn", Detail: err.Error()})
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote diagnostics unavailable: %v", err))
		if hint := actionableErrorHint(err); hint != "" {
			payload.Suggestions = append(payload.Suggestions, hint)
		}
	} else {
		payload.Remote = remote
		payload.Checks = append(payload.Checks, remoteDoctorChecks(remote)...)
		payload.Warnings = append(payload.Warnings, remote.Warnings...)
	}

	payload.Suggestions = append(payload.Suggestions, tunnelDoctorSuggestions(*sess, payload)...)
	return payload, nil
}

func collectDoctorPayloadWithContext(ctx context.Context) (*doctorPayload, error) {
	status, err := collectStatus()
	if err != nil {
		return nil, err
	}

	items, err := collectListItemsWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	return collectDoctorPayloadFromItems(ctx, status, items, collectRemoteDiagnosticsWithContext)
}

func collectDoctorPayloadFromItems(ctx context.Context, status *statusPayload, items []listItem, remoteCollector remoteDiagnosticsCollector) (*doctorPayload, error) {
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
	runDoctorRemoteDiagnostics(ctx, payload, items, remoteCollector)

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

func runDoctorFix(ctx context.Context, args []string, dryRun bool) (*doctorFixPayload, error) {
	sessions, err := session.List()
	if err != nil {
		return nil, fmt.Errorf("load local session records: %w", err)
	}
	if len(args) > 0 {
		target := args[0]
		filtered := sessions[:0]
		for _, sess := range sessions {
			if sess.TunnelID == target {
				filtered = append(filtered, sess)
			}
		}
		if len(filtered) == 0 {
			if _, err := findSession(target); err != nil {
				return nil, err
			}
		}
		sessions = filtered
	}
	status, err := collectStatus()
	if err != nil {
		return nil, err
	}
	payload := &doctorFixPayload{DryRun: dryRun}
	if !status.DaemonRunning && hasDaemonSessionNeedingDaemon(sessions) {
		payload.Actions = append(payload.Actions, doctorFixAction{
			Action:  "daemon-start",
			Command: "sealtun daemon",
			Reason:  "daemon-managed tunnel sessions exist but the local daemon is not running",
			Allowed: true,
		})
	}
	for _, sess := range sessions {
		payload.Actions = append(payload.Actions, doctorFixActionsForSession(sess)...)
	}
	if dryRun {
		return payload, nil
	}
	for i := range payload.Actions {
		if !payload.Actions[i].Allowed {
			continue
		}
		err := executeDoctorFixAction(ctx, payload.Actions[i])
		if err != nil {
			payload.Actions[i].Error = err.Error()
			continue
		}
		payload.Actions[i].Executed = true
	}
	return payload, nil
}

func hasDaemonSessionNeedingDaemon(sessions []session.TunnelSession) bool {
	now := time.Now()
	for _, sess := range sessions {
		if sess.Mode == "daemon" && sess.ConnectionState != session.ConnectionStateStopped && !sessionExpired(sess, now) {
			return true
		}
	}
	return false
}

func doctorFixActionsForSession(sess session.TunnelSession) []doctorFixAction {
	if sess.ConnectionState == session.ConnectionStateStopped {
		if sessionExpired(sess, time.Now()) {
			return []doctorFixAction{{
				Action:   "cleanup",
				TunnelID: sess.TunnelID,
				Command:  commandForTunnelAction("cleanup", sess.TunnelID),
				Reason:   "stopped tunnel session has expired",
				Allowed:  true,
			}}
		}
		if strings.TrimSpace(sess.Secret) == "" {
			return []doctorFixAction{{
				Action:   "start",
				TunnelID: sess.TunnelID,
				Command:  commandForTunnelAction("start", sess.TunnelID),
				Reason:   "stopped tunnel has no local secret and must be recreated",
				Allowed:  false,
			}}
		}
		return []doctorFixAction{{
			Action:   "start",
			TunnelID: sess.TunnelID,
			Command:  commandForTunnelAction("start", sess.TunnelID),
			Reason:   "tunnel is stopped and can be resumed",
			Allowed:  true,
		}}
	}
	if sessionExpired(sess, time.Now()) {
		return []doctorFixAction{{
			Action:   "cleanup",
			TunnelID: sess.TunnelID,
			Command:  commandForTunnelAction("cleanup", sess.TunnelID),
			Reason:   "tunnel session has expired",
			Allowed:  true,
		}}
	}
	if sess.Mode == "daemon" {
		return nil
	}
	if session.IsStaleWithOwner(sess, time.Minute, sessionOwnerAlive(sess)) {
		return []doctorFixAction{{
			Action:   "cleanup",
			TunnelID: sess.TunnelID,
			Command:  commandForTunnelAction("cleanup", sess.TunnelID),
			Reason:   "tunnel session is stale and no active owner is keeping it alive",
			Allowed:  true,
		}}
	}
	return nil
}

func executeDoctorFixAction(ctx context.Context, action doctorFixAction) error {
	switch action.Action {
	case "daemon-start":
		return doctorFixEnsureDaemon()
	case "start":
		return withTunnelOperationLockContext(ctx, action.TunnelID, func() error {
			sess, err := findSession(action.TunnelID)
			if err != nil {
				return err
			}
			if strings.TrimSpace(sess.Secret) == "" {
				return fmt.Errorf("tunnel %s cannot be started because its local secret is unavailable", sess.TunnelID)
			}
			if sessionExpired(*sess, time.Now()) {
				return fmt.Errorf("tunnel %s has expired", sess.TunnelID)
			}
			return doctorFixStartTunnel(ctx, sess)
		})
	case "cleanup":
		return withTunnelOperationLockContext(ctx, action.TunnelID, func() error {
			sess, err := findSession(action.TunnelID)
			if err != nil {
				return err
			}
			if !sessionExpired(*sess, time.Now()) && !session.IsStaleWithOwner(*sess, time.Minute, sessionOwnerAlive(*sess)) {
				return fmt.Errorf("refusing to cleanup non-stale active tunnel %s", sess.TunnelID)
			}
			if sess.Mode == "daemon" && !sessionExpired(*sess, time.Now()) {
				return fmt.Errorf("refusing to cleanup daemon-managed active tunnel %s", sess.TunnelID)
			}
			cleanupCtx, cancel := context.WithTimeout(ctx, tunnelCleanupTimeout)
			defer cancel()
			if err := doctorFixCleanupResources(cleanupCtx, *sess); err != nil {
				return err
			}
			return session.Delete(sess.TunnelID)
		})
	default:
		return fmt.Errorf("unknown fix action %q", action.Action)
	}
}

func doctorFixExecutionError(payload *doctorFixPayload) error {
	if payload == nil || payload.DryRun {
		return nil
	}
	failed := 0
	for _, action := range payload.Actions {
		if action.Allowed && action.Error != "" {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("failed to execute %d doctor fix action(s)", failed)
	}
	return nil
}

func printDoctorFix(cmd *cobra.Command, payload *doctorFixPayload) {
	out := cmd.OutOrStdout()
	if payload.DryRun {
		fmt.Fprintln(out, "Sealtun Doctor Fix Plan")
	} else {
		fmt.Fprintln(out, "Sealtun Doctor Fix Results")
	}
	if len(payload.Actions) == 0 {
		fmt.Fprintln(out, "  No conservative automatic fixes are available.")
		return
	}
	for _, action := range payload.Actions {
		status := "planned"
		if !action.Allowed {
			status = "blocked"
		} else if action.Executed {
			status = "executed"
		}
		if action.Error != "" {
			status = "failed"
		}
		target := action.TunnelID
		if target == "" {
			target = "local"
		}
		fmt.Fprintf(out, "  - %s %s: %s\n", action.Action, target, status)
		fmt.Fprintf(out, "    Reason: %s\n", action.Reason)
		if action.Command != "" {
			fmt.Fprintf(out, "    Command: %s\n", action.Command)
		}
		if action.Error != "" {
			fmt.Fprintf(out, "    Error: %s\n", action.Error)
		}
	}
}

func runDoctorRemoteDiagnostics(parent context.Context, payload *doctorPayload, items []listItem, remoteCollector remoteDiagnosticsCollector) {
	if remoteCollector == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, doctorRemoteTimeout)
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
				remote, err := remoteCollector(ctx, *sess)
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

func printTunnelDoctor(cmd *cobra.Command, payload *tunnelDoctorPayload) {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Sealtun Tunnel Doctor")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", payload.TunnelID)
	fmt.Fprintf(out, "  Status: %s\n", payload.Status)
	fmt.Fprintf(out, "  Protocol: %s\n", payload.Protocol)
	fmt.Fprintf(out, "  Endpoint: %s\n", valueOr(payload.Endpoint, "-"))
	fmt.Fprintf(out, "  Local target: %s\n", payload.LocalTarget)
	fmt.Fprintf(out, "  Mode: %s\n", valueOr(payload.Mode, "unknown"))
	fmt.Fprintf(out, "  Region: %s\n", valueOr(payload.Region, "unknown"))
	fmt.Fprintf(out, "  Namespace: %s\n", valueOr(payload.Namespace, "unknown"))
	if payload.LastError != "" {
		fmt.Fprintf(out, "  Last error: %s\n", payload.LastError)
	}

	if len(payload.Checks) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Checks")
		for _, check := range payload.Checks {
			fmt.Fprintf(out, "  - %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Fprintf(out, " (%s)", check.Detail)
			}
			fmt.Fprintln(out)
		}
	}

	if len(payload.Suggestions) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Suggestions")
		for _, suggestion := range payload.Suggestions {
			fmt.Fprintf(out, "  - %s\n", suggestion)
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

func checkStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "warn"
}

func ownerDoctorCheck(sess session.TunnelSession, alive bool) doctorCheck {
	status := checkStatus(alive)
	if sess.ConnectionState == session.ConnectionStateStopped {
		status = "skip"
	}
	return doctorCheck{Name: "owner", Status: status, Detail: ownerCheckDetail(sess, alive)}
}

func ownerCheckDetail(sess session.TunnelSession, alive bool) string {
	if alive {
		return "owner process is alive"
	}
	if sess.ConnectionState == session.ConnectionStateStopped {
		return "tunnel is stopped"
	}
	if sess.Mode == "daemon" {
		return "local daemon is not running"
	}
	return "recorded process is not running"
}

func targetDoctorCheck(sess session.TunnelSession, reachable bool) doctorCheck {
	status := checkStatus(reachable)
	if sess.ConnectionState == session.ConnectionStateStopped {
		status = "skip"
	}
	return doctorCheck{Name: "target", Status: status, Detail: targetCheckDetail(sess, reachable)}
}

func targetCheckDetail(sess session.TunnelSession, reachable bool) string {
	if reachable {
		return "target accepts TCP connections"
	}
	if strings.TrimSpace(sessionTargetURL(sess)) == "" {
		return "target is missing from the session"
	}
	if sess.ConnectionState == session.ConnectionStateStopped {
		return "not checked because the tunnel is stopped"
	}
	return fmt.Sprintf("%s is not reachable", sessionTargetLabel(sess))
}

func remoteDoctorChecks(remote *k8s.TunnelDiagnostics) []doctorCheck {
	if remote == nil {
		return nil
	}
	deploymentStatus := checkStatus(remote.Deployment.Exists && remote.Deployment.ReadyReplicas > 0)
	if remote.Deployment.Exists && remote.Deployment.DesiredReplicas == 0 {
		deploymentStatus = "skip"
	}
	checks := []doctorCheck{
		{Name: "deployment", Status: deploymentStatus, Detail: fmt.Sprintf("%d/%d ready", remote.Deployment.ReadyReplicas, remote.Deployment.DesiredReplicas)},
		{Name: "service", Status: checkStatus(remote.Service.Exists), Detail: valueOr(strings.Join(remote.Service.Ports, ", "), "no ports reported")},
	}
	if remote.Ingress.Exists {
		checks = append(checks, doctorCheck{Name: "ingress", Status: "ok", Detail: strings.Join(remote.Ingress.Hosts, ", ")})
	} else {
		checks = append(checks, doctorCheck{Name: "ingress", Status: "warn", Detail: "missing"})
	}
	if remote.Certificate != nil {
		status := checkStatus(remote.Certificate.Exists && remote.Certificate.Ready)
		checks = append(checks, doctorCheck{Name: "certificate", Status: status, Detail: certificateDoctorDetail(remote.Certificate)})
	}
	return checks
}

func certificateDoctorDetail(cert *k8s.CertificateDiagnostics) string {
	if cert == nil || !cert.Exists {
		return "missing"
	}
	if cert.Ready {
		return "ready"
	}
	return "not ready"
}

func tunnelDoctorSuggestions(sess session.TunnelSession, payload *tunnelDoctorPayload) []string {
	suggestions := []string{}
	if payload.Status == "stopped" {
		suggestions = append(suggestions, fmt.Sprintf("run `sealtun start %s` to resume the tunnel", sess.TunnelID))
		return suggestions
	}
	if !payload.ProcessAlive && sess.Mode == "daemon" {
		suggestions = append(suggestions, "run `sealtun status` to check the daemon, then restart the tunnel if needed")
	}
	if !payload.LocalPortReachable && strings.TrimSpace(sessionTargetURL(sess)) != "" && payload.Status != "stopped" {
		suggestions = append(suggestions, fmt.Sprintf("make target %s reachable from this machine, then rerun `sealtun doctor %s`", sessionTargetLabel(sess), sess.TunnelID))
	}
	if payload.Remote == nil {
		suggestions = append(suggestions, fmt.Sprintf("run `sealtun inspect %s --remote` after login to see Kubernetes resource details", sess.TunnelID))
	}
	if sess.CustomDomain != "" {
		suggestions = append(suggestions, fmt.Sprintf("run `sealtun domain doctor %s` to verify DNS, Ingress, and certificate status", sess.TunnelID))
	}
	if hint := actionableErrorHintText(sess.LastError); hint != "" {
		suggestions = append(suggestions, hint)
	}
	if len(suggestions) == 0 {
		suggestions = append(suggestions, "no immediate action suggested")
	}
	return suggestions
}

func writeTunnelDoctorReport(path string, payload *tunnelDoctorPayload) (string, error) {
	if payload == nil {
		return "", fmt.Errorf("doctor report payload is nil")
	}
	if strings.TrimSpace(path) == "" {
		path = defaultDoctorReportPath(payload.TunnelID)
	}
	if err := writeRegularFileAtomic(path, []byte(renderTunnelDoctorReport(payload)), 0o600, "doctor report"); err != nil {
		return "", fmt.Errorf("write doctor report: %w", err)
	}
	return path, nil
}

func defaultDoctorReportPath(tunnelID string) string {
	safe := doctorReportSafePattern.ReplaceAllString(tunnelID, "-")
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = "tunnel"
	}
	return filepath.Join(".", fmt.Sprintf("sealtun-doctor-%s-%s.md", safe, time.Now().Format("20060102-150405")))
}

func renderTunnelDoctorReport(payload *tunnelDoctorPayload) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# Sealtun Doctor Report")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- Generated at: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Tunnel ID: %s\n", redactSensitiveText(payload.TunnelID))
	fmt.Fprintf(&b, "- Status: %s\n", redactSensitiveText(payload.Status))
	fmt.Fprintf(&b, "- Protocol: %s\n", redactSensitiveText(payload.Protocol))
	fmt.Fprintf(&b, "- Endpoint: %s\n", redactSensitiveText(payload.Endpoint))
	fmt.Fprintf(&b, "- Target: %s\n", redactSensitiveText(payload.LocalTarget))
	fmt.Fprintf(&b, "- Mode: %s\n", redactSensitiveText(payload.Mode))
	fmt.Fprintf(&b, "- Region: %s\n", redactSensitiveText(payload.Region))
	fmt.Fprintf(&b, "- Namespace: %s\n", redactSensitiveText(payload.Namespace))
	fmt.Fprintf(&b, "- Process alive: %s\n", yesNo(payload.ProcessAlive))
	fmt.Fprintf(&b, "- Target reachable: %s\n", yesNo(payload.LocalPortReachable))
	if payload.LastError != "" {
		fmt.Fprintf(&b, "- Last error: %s\n", redactSensitiveText(payload.LastError))
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Checks")
	if len(payload.Checks) == 0 {
		fmt.Fprintln(&b, "- No checks reported.")
	} else {
		fmt.Fprintln(&b, "| Check | Status | Detail |")
		fmt.Fprintln(&b, "| --- | --- | --- |")
		for _, check := range payload.Checks {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", markdownCell(check.Name), markdownCell(check.Status), markdownCell(check.Detail))
		}
	}
	fmt.Fprintln(&b)

	if payload.Remote != nil {
		fmt.Fprintln(&b, "## Remote")
		fmt.Fprintf(&b, "- Deployment: %s (%d/%d ready)\n", yesNo(payload.Remote.Deployment.Exists), payload.Remote.Deployment.ReadyReplicas, payload.Remote.Deployment.DesiredReplicas)
		fmt.Fprintf(&b, "- Service: %s\n", yesNo(payload.Remote.Service.Exists))
		fmt.Fprintf(&b, "- Ingress: %s\n", yesNo(payload.Remote.Ingress.Exists))
		if payload.Remote.Certificate != nil {
			fmt.Fprintf(&b, "- Certificate: %s (ready=%s)\n", yesNo(payload.Remote.Certificate.Exists), yesNo(payload.Remote.Certificate.Ready))
		}
		if len(payload.Remote.Pods) > 0 {
			fmt.Fprintln(&b, "- Pods:")
			for _, pod := range payload.Remote.Pods {
				fmt.Fprintf(&b, "  - %s phase=%s ready=%s restarts=%d\n", redactSensitiveText(pod.Name), redactSensitiveText(pod.Phase), yesNo(pod.Ready), pod.RestartCount)
			}
		}
		if len(payload.Remote.Events) > 0 {
			fmt.Fprintln(&b, "- Recent events:")
			for _, event := range payload.Remote.Events {
				fmt.Fprintf(&b, "  - %s %s %s: %s\n", redactSensitiveText(valueOr(event.LastTimestamp, event.FirstTimestamp)), redactSensitiveText(event.Type), redactSensitiveText(event.Reason), redactSensitiveText(event.Message))
			}
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintln(&b, "## Suggestions")
	if len(payload.Suggestions) == 0 {
		fmt.Fprintln(&b, "- No immediate action suggested.")
	} else {
		for _, suggestion := range payload.Suggestions {
			fmt.Fprintf(&b, "- %s\n", redactSensitiveText(suggestion))
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "## Warnings")
	if len(payload.Warnings) == 0 {
		fmt.Fprintln(&b, "- No warnings.")
	} else {
		for _, warning := range payload.Warnings {
			fmt.Fprintf(&b, "- %s\n", redactSensitiveText(warning))
		}
	}
	return b.String()
}

func markdownCell(value string) string {
	value = redactSensitiveText(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func redactSensitiveText(value string) string {
	if value == "" {
		return ""
	}
	out := value
	for _, rule := range doctorRedactionRules {
		out = rule.pattern.ReplaceAllString(out, rule.repl)
	}
	return out
}
