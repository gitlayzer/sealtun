package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/labring/sealtun/pkg/clusterconnect"
	"github.com/spf13/cobra"
)

type connectOptions struct {
	Mode      string
	Namespace string
	Listen    string
	Check     bool
	JSON      bool
}

var connectOpts = connectOptions{
	Mode: clusterconnect.ModeAuto,
}

type connectPreflighter interface {
	Preflight(context.Context, clusterconnect.Options) (*clusterconnect.Preflight, error)
}

type connectPlanRunner interface {
	Plan(context.Context) (*clusterconnect.TransparentPlan, error)
	RunPlan(context.Context, *clusterconnect.TransparentPlan) error
}

var (
	connectSaveState          = clusterconnect.SaveState
	connectRemoveState        = clusterconnect.RemoveState
	connectAcquireRuntimeLock = clusterconnect.AcquireRuntimeLock
	connectNotifyContext      = signal.NotifyContext
	connectStopStateProcess   = clusterconnect.StopStateProcess
	connectCleanupState       = clusterconnect.CleanupTransparentState
)

var connectCmd = &cobra.Command{
	Use:          "connect",
	Short:        "Connect local TCP clients to Services or Pods in the active namespace",
	SilenceUsage: true,
	Long: `Connect local TCP clients to Services or Pods in the active Kubernetes
namespace using the current Sealtun login and kubeconfig.

Linux transparent mode updates local iptables and /etc/hosts so clients can
directly use Service FQDNs, Service ClusterIPs, and Pod IPs without SOCKS or
per-client proxy configuration. ICMP/ping and UDP are not supported.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if connectOpts.Check {
			return runConnectCheck(cmd, connectOpts)
		}
		return runConnect(cmd, connectOpts)
	},
}

var connectStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current cluster connect state",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runConnectStatus(cmd, connectOpts.JSON)
	},
}

var disconnectCmd = newDisconnectCommand()

var connectDisconnectCmd = newDisconnectCommand()

func newDisconnectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect",
		Short: "Stop the current cluster connect session",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDisconnect(cmd)
		},
	}
}

func init() {
	rootCmd.AddCommand(connectCmd)
	rootCmd.AddCommand(disconnectCmd)
	connectCmd.AddCommand(connectStatusCmd)
	connectCmd.AddCommand(connectDisconnectCmd)
	connectCmd.Flags().StringVar(&connectOpts.Mode, "mode", clusterconnect.ModeAuto, "Connect mode: auto or tun")
	connectCmd.Flags().StringVar(&connectOpts.Namespace, "namespace", "", "Kubernetes namespace to access; defaults to active kubeconfig namespace")
	connectCmd.Flags().StringVar(&connectOpts.Listen, "listen", "", "Local redirect listener address; defaults to 127.0.0.1:15443")
	connectCmd.Flags().BoolVar(&connectOpts.Check, "check", false, "Only probe capabilities and print the selected mode")
	connectCmd.Flags().BoolVar(&connectOpts.JSON, "json", false, "Output preflight/status as JSON")
	connectStatusCmd.Flags().BoolVar(&connectOpts.JSON, "json", false, "Output status as JSON")
	markAlphaTree(connectCmd)
	markDeprecated(disconnectCmd, "sealtun connect disconnect")
}

func runConnectCheck(cmd *cobra.Command, opts connectOptions) error {
	env, err := clusterconnect.NewActiveEnvironment()
	if err != nil {
		return err
	}
	return runConnectCheckWithEnvironment(cmd, opts, env)
}

func runConnectCheckWithEnvironment(cmd *cobra.Command, opts connectOptions, env connectPreflighter) error {
	ctx := connectCommandContext(cmd)
	preflight, err := env.Preflight(ctx, clusterconnect.Options{
		Mode:      opts.Mode,
		Namespace: opts.Namespace,
	})
	if opts.JSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if encodeErr := enc.Encode(preflight); encodeErr != nil {
			return encodeErr
		}
		return err
	}
	printConnectPreflight(cmd, preflight)
	return err
}

func runConnect(cmd *cobra.Command, opts connectOptions) error {
	env, err := clusterconnect.NewActiveEnvironment()
	if err != nil {
		return err
	}
	return runConnectWithEnvironment(cmd, opts, env, func(options clusterconnect.TransparentOptions) connectPlanRunner {
		return clusterconnect.NewTransparentServer(env, options)
	})
}

func runConnectWithEnvironment(cmd *cobra.Command, opts connectOptions, env connectPreflighter, newServer func(clusterconnect.TransparentOptions) connectPlanRunner) error {
	releaseRuntime, err := connectAcquireRuntimeLock()
	if err != nil {
		return fmt.Errorf("another sealtun connect session is already running: %w", err)
	}
	defer releaseRuntime()

	ctx := connectCommandContext(cmd)
	preflight, err := env.Preflight(ctx, clusterconnect.Options{
		Mode:      opts.Mode,
		Namespace: opts.Namespace,
	})
	if err != nil {
		if preflight != nil && !opts.JSON {
			printConnectPreflight(cmd, preflight)
		}
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(preflight); err != nil {
			return err
		}
	} else {
		printConnectPreflight(cmd, preflight)
	}

	server := newServer(clusterconnect.TransparentOptions{
		Namespace: opts.Namespace,
		Listen:    opts.Listen,
	})
	plan, err := server.Plan(ctx)
	if err != nil {
		return err
	}
	state := clusterconnect.State{
		Mode:       preflight.SelectedMode,
		Namespace:  preflight.Namespace,
		Region:     preflight.Region,
		Profile:    preflight.ActiveProfile,
		Listen:     plan.Listen,
		RouteCount: len(plan.Rules),
		HostCount:  len(plan.Hosts),
		Rules:      plan.Rules,
		Hosts:      plan.Hosts,
		PID:        os.Getpid(),
	}
	if err := connectSaveState(state); err != nil {
		return err
	}
	defer connectRemoveState()

	connectCtx, stopSignals := connectNotifyContext(ctx, signalCleanupSignals()...)
	defer stopSignals()
	err = server.RunPlan(connectCtx, plan)
	if err == nil || err == context.Canceled {
		return nil
	}
	return err
}

func connectCommandContext(cmd *cobra.Command) context.Context {
	if cmd == nil {
		return context.Background()
	}
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func runConnectStatus(cmd *cobra.Command, asJSON bool) error {
	state, path, err := clusterconnect.LoadState()
	if err != nil {
		if os.IsNotExist(err) {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{"running": false})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "No active cluster connect session.")
			return nil
		}
		return err
	}
	payload := map[string]interface{}{
		"running":            state.Alive(),
		"statePath":          path,
		"mode":               state.Mode,
		"namespace":          state.Namespace,
		"region":             state.Region,
		"profile":            state.Profile,
		"pid":                state.PID,
		"startedAt":          state.StartedAt,
		"listen":             state.Listen,
		"routeCount":         state.RouteCount,
		"hostCount":          state.HostCount,
		"transparentRouting": true,
	}
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Cluster connect: %s\n", runningLabel(state.Alive()))
	fmt.Fprintf(cmd.OutOrStdout(), "  Mode: %s\n", state.Mode)
	fmt.Fprintf(cmd.OutOrStdout(), "  Namespace: %s\n", state.Namespace)
	fmt.Fprintf(cmd.OutOrStdout(), "  Profile: %s\n", valueOr(state.Profile, "-"))
	fmt.Fprintf(cmd.OutOrStdout(), "  Region: %s\n", valueOr(state.Region, "-"))
	fmt.Fprintf(cmd.OutOrStdout(), "  Listen: %s\n", valueOr(state.Listen, "-"))
	fmt.Fprintf(cmd.OutOrStdout(), "  TCP routes: %d\n", state.RouteCount)
	fmt.Fprintf(cmd.OutOrStdout(), "  Host entries: %d\n", state.HostCount)
	fmt.Fprintf(cmd.OutOrStdout(), "  PID: %d\n", state.PID)
	return nil
}

func runDisconnect(cmd *cobra.Command) error {
	state, _, err := clusterconnect.LoadState()
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "No active cluster connect session.")
			return nil
		}
		return err
	}
	stopErr := connectStopStateProcess(*state)
	if stopErr != nil {
		return stopErr
	}
	plan := &clusterconnect.TransparentPlan{
		Namespace: state.Namespace,
		Listen:    state.Listen,
		Rules:     state.Rules,
		Hosts:     state.Hosts,
	}
	cleanupErr := connectCleanupState(plan)
	if cleanupErr != nil {
		return cleanupErr
	}
	if err := clusterconnect.RemoveState(); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Cluster connect stopped.")
	return nil
}

func printConnectPreflight(cmd *cobra.Command, preflight *clusterconnect.Preflight) {
	if preflight == nil {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Cluster connect preflight")
	fmt.Fprintf(out, "  Profile: %s\n", valueOr(preflight.ActiveProfile, "-"))
	fmt.Fprintf(out, "  Region: %s\n", valueOr(preflight.Region, "-"))
	fmt.Fprintf(out, "  Namespace: %s\n", valueOr(preflight.Namespace, "-"))
	fmt.Fprintf(out, "  Requested mode: %s\n", preflight.Mode)
	fmt.Fprintf(out, "  Selected mode: %s\n", valueOr(preflight.SelectedMode, "-"))
	fmt.Fprintln(out, "  Modes:")
	for _, mode := range preflight.Modes {
		selected := ""
		if mode.Selected {
			selected = " (selected)"
		}
		fmt.Fprintf(out, "    - %s: %s%s", mode.Name, yesNo(mode.Available), selected)
		if mode.Reason != "" {
			fmt.Fprintf(out, " - %s", mode.Reason)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "  Capabilities:")
	for _, cap := range preflight.Capabilities {
		fmt.Fprintf(out, "    - %s: %s", cap.Name, yesNo(cap.Allowed))
		if cap.Error != "" {
			fmt.Fprintf(out, " (%s)", cap.Error)
		} else if cap.Reason != "" && !cap.Allowed {
			fmt.Fprintf(out, " (%s)", cap.Reason)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "  Scope: active Sealtun login and active kubeconfig only.")
	fmt.Fprintln(out, "  Current status: Linux transparent TCP mode supports Service FQDN, Service ClusterIP, and Pod IP access.")
	fmt.Fprintln(out, "  Limits: TCP only; ICMP/ping and UDP are not supported.")
}

func runningLabel(running bool) string {
	if running {
		return "running"
	}
	return "not running"
}
