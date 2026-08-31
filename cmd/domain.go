package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type domainPayload struct {
	TunnelID     string `json:"tunnelId"`
	PublicHost   string `json:"publicHost"`
	SealosHost   string `json:"sealosHost"`
	CustomDomain string `json:"customDomain,omitempty"`
	CNAME        string `json:"cname,omitempty"`
}

type domainVerifyPayload struct {
	TunnelID          string   `json:"tunnelId"`
	PublicHost        string   `json:"publicHost"`
	SealosHost        string   `json:"sealosHost"`
	CustomDomain      string   `json:"customDomain"`
	DNSCNAME          string   `json:"dnsCname,omitempty"`
	DNSReady          bool     `json:"dnsReady"`
	IngressReady      bool     `json:"ingressReady"`
	CertificateExists bool     `json:"certificateExists"`
	CertificateReady  bool     `json:"certificateReady"`
	CertificateSecret string   `json:"certificateSecret,omitempty"`
	Ready             bool     `json:"ready"`
	Warnings          []string `json:"warnings,omitempty"`
}

type domainStatusPayload struct {
	TotalSessions int                `json:"totalSessions"`
	CustomDomains int                `json:"customDomains"`
	Ready         int                `json:"ready"`
	NotReady      int                `json:"notReady"`
	Items         []domainStatusItem `json:"items"`
	Warnings      []string           `json:"warnings,omitempty"`
}

type domainStatusItem struct {
	TunnelID          string   `json:"tunnelId"`
	PublicHost        string   `json:"publicHost"`
	SealosHost        string   `json:"sealosHost"`
	CustomDomain      string   `json:"customDomain"`
	DNSCNAME          string   `json:"dnsCname,omitempty"`
	DNSReady          bool     `json:"dnsReady"`
	IngressReady      bool     `json:"ingressReady"`
	CertificateExists bool     `json:"certificateExists"`
	CertificateReady  bool     `json:"certificateReady"`
	CertificateSecret string   `json:"certificateSecret,omitempty"`
	Ready             bool     `json:"ready"`
	Warnings          []string `json:"warnings,omitempty"`
}

var domainJSON bool
var domainVerifyWait bool
var domainVerifyTimeout time.Duration
var domainAddWait bool
var domainAddTimeout time.Duration
var domainStatusTimeout = 15 * time.Second
var domainDoctorTimeout = 15 * time.Second
var lookupCNAME = net.DefaultResolver.LookupCNAME

var customDomainLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

const domainStatusConcurrency = 4

var domainCmd = &cobra.Command{
	Use:   "domain",
	Short: "Manage custom domains for Sealtun tunnels",
	Long: `Manage custom domains for Sealtun tunnels.

Sealtun always keeps a Sealos-managed host as the tunnel control endpoint and
CNAME target. Point your custom domain to that Sealos host, then attach the
domain to the tunnel.`,
}

var domainPlanCmd = &cobra.Command{
	Use:          "plan [tunnel-id] [domain]",
	Short:        "Show the DNS and attach plan for a custom domain",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		customDomain, err := validateCustomDomain(args[1])
		if err != nil {
			return err
		}
		if customDomain == "" {
			return fmt.Errorf("custom domain is required")
		}
		payload, err := planSessionCustomDomainWithContext(cmd.Context(), args[0], customDomain)
		if payload != nil {
			if printErr := printDomainPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
		return err
	},
}

var domainAddCmd = &cobra.Command{
	Use:          "add [tunnel-id] [domain]",
	Short:        "Attach a custom domain, optionally waiting for DNS readiness",
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDomainAdd(cmd, args, domainAddWait, domainAddTimeout)
	},
}

func runDomainAdd(cmd *cobra.Command, args []string, wait bool, timeout time.Duration) error {
	customDomain, err := validateCustomDomain(args[1])
	if err != nil {
		return err
	}
	if customDomain == "" {
		return fmt.Errorf("custom domain is required")
	}
	if wait {
		if timeout <= 0 {
			return fmt.Errorf("--timeout must be greater than 0 when --wait is set")
		}
		plan, err := planSessionCustomDomainWithContext(cmd.Context(), args[0], customDomain)
		if plan != nil && !domainJSON {
			if printErr := printDomainPayload(cmd, plan); printErr != nil {
				return printErr
			}
		}
		if err != nil {
			return err
		}
		waitOut := cmd.OutOrStdout()
		if domainJSON {
			waitOut = cmd.ErrOrStderr()
		}
		fmt.Fprintf(waitOut, "Waiting for DNS CNAME readiness (timeout %s)...\n", timeout)
		if err := waitForDomainCNAMEReady(cmd.Context(), customDomain, plan.SealosHost, timeout); err != nil {
			return err
		}
	}
	payload, err := configureSessionCustomDomain(cmd.Context(), args[0], customDomain)
	if payload != nil && !(domainJSON && wait) {
		if printErr := printDomainPayload(cmd, payload); printErr != nil {
			return printErr
		}
	}
	if err != nil {
		return err
	}
	if !wait {
		return nil
	}
	verify, verifyErr := waitForSessionDomain(cmd.Context(), args[0], timeout)
	if verify != nil {
		if printErr := printDomainVerifyPayload(cmd, verify); printErr != nil {
			return printErr
		}
	}
	return domainVerifyResultError(verify, verifyErr)
}

var domainClearCmd = &cobra.Command{
	Use:          "clear [tunnel-id]",
	Short:        "Remove a custom domain from an existing tunnel",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := clearSessionCustomDomain(cmd.Context(), args[0])
		if payload != nil {
			if printErr := printDomainPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
		if err != nil {
			return err
		}
		return nil
	},
}

var domainVerifyCmd = &cobra.Command{
	Use:          "verify [tunnel-id]",
	Short:        "Verify DNS, Ingress, and certificate readiness for a custom domain",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		var payload *domainVerifyPayload
		var err error
		if domainVerifyWait {
			if domainVerifyTimeout <= 0 {
				return fmt.Errorf("--timeout must be greater than 0 when --wait is set")
			}
			payload, err = waitForSessionDomain(cmd.Context(), args[0], domainVerifyTimeout)
		} else {
			payload, err = verifySessionDomain(cmd.Context(), args[0])
		}
		if payload != nil {
			if printErr := printDomainVerifyPayload(cmd, payload); printErr != nil {
				return printErr
			}
		}
		return domainVerifyResultError(payload, err)
	},
}

var domainStatusCmd = &cobra.Command{
	Use:          "status [tunnel-id]",
	Short:        "Show custom domain readiness for one tunnel or all tunnels",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		tunnelID := ""
		if len(args) > 0 {
			tunnelID = args[0]
		}
		payload, err := collectDomainStatus(cmd.Context(), tunnelID, domainStatusTimeout)
		if payload != nil {
			if printErr := printDomainStatusPayload(cmd, payload, false); printErr != nil {
				return printErr
			}
		}
		return err
	},
}

var domainDoctorCmd = &cobra.Command{
	Use:          "doctor [tunnel-id]",
	Short:        "Run custom domain DNS, Ingress, and certificate diagnostics",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		tunnelID := ""
		if len(args) > 0 {
			tunnelID = args[0]
		}
		payload, err := collectDomainStatus(cmd.Context(), tunnelID, domainDoctorTimeout)
		if payload != nil {
			if printErr := printDomainStatusPayload(cmd, payload, true); printErr != nil {
				return printErr
			}
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(domainCmd)
	domainCmd.AddCommand(domainPlanCmd)
	domainCmd.AddCommand(domainAddCmd)
	domainCmd.AddCommand(domainClearCmd)
	domainCmd.AddCommand(domainVerifyCmd)
	domainCmd.AddCommand(domainStatusCmd)
	domainCmd.AddCommand(domainDoctorCmd)
	domainCmd.PersistentFlags().BoolVar(&domainJSON, "json", false, "Output domain details as JSON")
	domainVerifyCmd.Flags().BoolVar(&domainVerifyWait, "wait", false, "Wait until DNS, Ingress, and certificate are ready")
	domainVerifyCmd.Flags().DurationVar(&domainVerifyTimeout, "timeout", 5*time.Minute, "Maximum time to wait for domain readiness")
	domainAddCmd.Flags().BoolVar(&domainAddWait, "wait", false, "Wait for DNS, then attach the domain and wait for certificate readiness")
	domainAddCmd.Flags().DurationVar(&domainAddTimeout, "timeout", 5*time.Minute, "Maximum time to wait for DNS and domain readiness")
	domainStatusCmd.Flags().DurationVar(&domainStatusTimeout, "timeout", 15*time.Second, "Per-domain readiness check timeout")
	domainDoctorCmd.Flags().DurationVar(&domainDoctorTimeout, "timeout", 15*time.Second, "Per-domain diagnostic timeout")
}

func planSessionCustomDomain(tunnelID, customDomain string) (*domainPayload, error) {
	return planSessionCustomDomainWithContext(context.Background(), tunnelID, customDomain)
}

func planSessionCustomDomainWithContext(ctx context.Context, tunnelID, customDomain string) (*domainPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	_ = refreshSessionFromRemote(ctx, sess)
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	sealosHost := sessionSealosHostForDomain(*sess, "")
	if sealosHost == "" {
		client, err := k8sClientForSession(*sess)
		if err != nil {
			return nil, err
		}
		sealosHost = sessionSealosHostForDomain(*sess, client.WithNamespace(sess.Namespace).SealosHost(sess.TunnelID))
	}
	if sealosHost == "" {
		return nil, fmt.Errorf("sealos CNAME target is unavailable for tunnel %s", sess.TunnelID)
	}
	return &domainPayload{
		TunnelID:     sess.TunnelID,
		PublicHost:   valueOr(sess.Host, sealosHost),
		SealosHost:   sealosHost,
		CustomDomain: customDomain,
		CNAME:        fmt.Sprintf("%s -> %s", customDomain, sealosHost),
	}, nil
}

func configureSessionCustomDomain(parent context.Context, tunnelID, customDomain string) (payload *domainPayload, err error) {
	err = withTunnelOperationLock(tunnelID, func() error {
		payload, err = configureSessionCustomDomainLocked(parent, tunnelID, customDomain)
		return err
	})
	return payload, err
}

func configureSessionCustomDomainLocked(parent context.Context, tunnelID, customDomain string) (*domainPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	if err := refreshSessionFromRemoteLocked(parent, sess); err != nil {
		return nil, err
	}
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	original := *sess
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return nil, err
	}

	namespacedClient := client.WithNamespace(sess.Namespace)
	sealosHost := sessionSealosHostForDomain(*sess, namespacedClient.SealosHost(sess.TunnelID))
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if err := requireDomainCNAME(ctx, customDomain, sealosHost); err != nil {
		return nil, err
	}

	hosts, err := namespacedClient.ConfigureCustomDomain(ctx, sess.TunnelID, sealosHost, customDomain)
	if err != nil {
		return nil, fmt.Errorf("configure custom domain for tunnel %s: %w", sess.TunnelID, err)
	}

	sess.Host = hosts.PublicHost
	sess.SealosHost = hosts.SealosHost
	sess.CustomDomain = hosts.CustomDomain
	if err := session.Update(*sess); err != nil {
		if rollbackErr := restoreRemoteCustomDomain(context.Background(), namespacedClient, original); rollbackErr != nil {
			return nil, fmt.Errorf("update local session %s: %w; remote rollback failed: %v", sess.TunnelID, err, rollbackErr)
		}
		return nil, fmt.Errorf("update local session %s: %w", sess.TunnelID, err)
	}
	return domainPayloadFromSession(*sess), nil
}

func clearSessionCustomDomain(parent context.Context, tunnelID string) (payload *domainPayload, err error) {
	err = withTunnelOperationLock(tunnelID, func() error {
		payload, err = clearSessionCustomDomainLocked(parent, tunnelID)
		return err
	})
	return payload, err
}

func clearSessionCustomDomainLocked(parent context.Context, tunnelID string) (*domainPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	if err := refreshSessionFromRemoteLocked(parent, sess); err != nil {
		return nil, err
	}
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	original := *sess
	client, err := k8sClientForSession(*sess)
	if err != nil {
		return nil, err
	}

	namespacedClient := client.WithNamespace(sess.Namespace)
	sealosHost := sessionSealosHostForDomain(*sess, namespacedClient.SealosHost(sess.TunnelID))
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	hosts, err := namespacedClient.ClearCustomDomain(ctx, sess.TunnelID, sealosHost)
	if err != nil && hosts.PublicHost == "" {
		return nil, fmt.Errorf("clear custom domain for tunnel %s: %w", sess.TunnelID, err)
	}

	sess.Host = hosts.PublicHost
	sess.SealosHost = hosts.SealosHost
	sess.CustomDomain = ""
	if err := session.Update(*sess); err != nil {
		if rollbackErr := restoreRemoteCustomDomain(context.Background(), namespacedClient, original); rollbackErr != nil {
			return nil, fmt.Errorf("update local session %s: %w; remote rollback failed: %v", sess.TunnelID, err, rollbackErr)
		}
		return nil, fmt.Errorf("update local session %s: %w", sess.TunnelID, err)
	}
	payload := domainPayloadFromSession(*sess)
	if err != nil {
		return payload, fmt.Errorf("clear custom domain for tunnel %s: %w", sess.TunnelID, err)
	}
	return payload, nil
}

func sessionSupportsCustomDomain(sess session.TunnelSession) bool {
	protocol := strings.TrimSpace(sess.Protocol)
	return protocol == "" || protocol == "https"
}

func restoreRemoteCustomDomain(parent context.Context, client *k8s.Client, sess session.TunnelSession) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	sealosHost := sessionSealosHostForDomain(sess, client.SealosHost(sess.TunnelID))
	if sess.CustomDomain != "" {
		_, err := client.ConfigureCustomDomain(ctx, sess.TunnelID, sealosHost, sess.CustomDomain)
		return err
	}
	_, err := client.ClearCustomDomain(ctx, sess.TunnelID, sealosHost)
	return err
}

func domainPayloadFromSession(sess session.TunnelSession) *domainPayload {
	payload := &domainPayload{
		TunnelID:     sess.TunnelID,
		PublicHost:   sess.Host,
		SealosHost:   sessionSealosHostForDomain(sess, ""),
		CustomDomain: sess.CustomDomain,
	}
	if sess.CustomDomain != "" && payload.SealosHost != "" {
		payload.CNAME = fmt.Sprintf("%s -> %s", sess.CustomDomain, payload.SealosHost)
	}
	return payload
}

func printDomainPayload(cmd *cobra.Command, payload *domainPayload) error {
	if domainJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Tunnel %s domain configuration\n", payload.TunnelID)
	fmt.Fprintf(out, "  Public host: %s\n", payload.PublicHost)
	fmt.Fprintf(out, "  Sealos host: %s\n", payload.SealosHost)
	if payload.CustomDomain != "" {
		fmt.Fprintf(out, "  Custom domain: %s\n", payload.CustomDomain)
		fmt.Fprintf(out, "  DNS: create CNAME %s -> %s\n", payload.CustomDomain, payload.SealosHost)
		fmt.Fprintln(out, "  Note: wait for DNS propagation and certificate issuance before relying on HTTPS.")
	} else {
		fmt.Fprintln(out, "  Custom domain: disabled")
	}
	return nil
}

type domainVerifier func(context.Context, session.TunnelSession) *domainVerifyPayload

func collectDomainStatus(parent context.Context, tunnelID string, timeout time.Duration) (*domainStatusPayload, error) {
	return collectDomainStatusWithVerifier(parent, tunnelID, timeout, verifyDomainForSession)
}

func collectDomainStatusWithVerifier(parent context.Context, tunnelID string, timeout time.Duration, verifier domainVerifier) (*domainStatusPayload, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("domain readiness timeout must be greater than 0")
	}

	var sessions []session.TunnelSession
	if tunnelID != "" {
		sess, err := findSession(tunnelID)
		if err != nil {
			return nil, err
		}
		_ = refreshSessionFromRemote(parent, sess)
		if sess.CustomDomain == "" {
			return nil, fmt.Errorf("tunnel %s has no custom domain configured", sess.TunnelID)
		}
		sessions = []session.TunnelSession{*sess}
	} else {
		list, err := session.List()
		if err != nil {
			return nil, fmt.Errorf("load tunnel sessions: %w", err)
		}
		for i := range list {
			_ = refreshSessionFromRemote(parent, &list[i])
		}
		sessions = list
	}

	return collectDomainStatusFromSessions(parent, sessions, timeout, verifier), nil
}

func collectDomainStatusFromSessions(parent context.Context, sessions []session.TunnelSession, timeout time.Duration, verifier domainVerifier) *domainStatusPayload {
	payload := &domainStatusPayload{TotalSessions: len(sessions)}
	targets := make([]session.TunnelSession, 0, len(sessions))
	for _, sess := range sessions {
		if sess.CustomDomain == "" {
			continue
		}
		targets = append(targets, sess)
	}

	payload.CustomDomains = len(targets)
	payload.Items = collectDomainStatusItems(parent, targets, timeout, verifier)
	for _, item := range payload.Items {
		if item.Ready {
			payload.Ready++
		} else {
			payload.NotReady++
		}
	}
	if payload.CustomDomains == 0 {
		payload.Warnings = append(payload.Warnings, "no custom domains configured")
	}
	return payload
}

type domainStatusJob struct {
	index int
	sess  session.TunnelSession
}

type domainStatusResult struct {
	index int
	item  domainStatusItem
}

func collectDomainStatusItems(parent context.Context, sessions []session.TunnelSession, timeout time.Duration, verifier domainVerifier) []domainStatusItem {
	if len(sessions) == 0 {
		return []domainStatusItem{}
	}

	workerCount := domainStatusConcurrency
	if workerCount > len(sessions) {
		workerCount = len(sessions)
	}

	jobs := make(chan domainStatusJob, len(sessions))
	results := make(chan domainStatusResult, len(sessions))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results <- domainStatusResult{
					index: job.index,
					item:  collectDomainStatusItem(parent, job.sess, timeout, verifier),
				}
			}
		}()
	}

	for i, sess := range sessions {
		jobs <- domainStatusJob{index: i, sess: sess}
	}
	close(jobs)
	wg.Wait()
	close(results)

	items := make([]domainStatusItem, len(sessions))
	for result := range results {
		items[result.index] = result.item
	}
	return items
}

func collectDomainStatusItem(parent context.Context, sess session.TunnelSession, timeout time.Duration, verifier domainVerifier) domainStatusItem {
	checkCtx, cancel := context.WithTimeout(parent, timeout)
	result := verifier(checkCtx, sess)
	cancel()
	if result == nil {
		return domainStatusItem{
			TunnelID:     sess.TunnelID,
			PublicHost:   sess.Host,
			SealosHost:   sessionSealosHostForDomain(sess, ""),
			CustomDomain: sess.CustomDomain,
			Warnings:     []string{"domain diagnostics returned no result"},
		}
	}

	return domainStatusItemFromVerify(result)
}

func domainStatusItemFromVerify(payload *domainVerifyPayload) domainStatusItem {
	return domainStatusItem{
		TunnelID:          payload.TunnelID,
		PublicHost:        payload.PublicHost,
		SealosHost:        payload.SealosHost,
		CustomDomain:      payload.CustomDomain,
		DNSCNAME:          payload.DNSCNAME,
		DNSReady:          payload.DNSReady,
		IngressReady:      payload.IngressReady,
		CertificateExists: payload.CertificateExists,
		CertificateReady:  payload.CertificateReady,
		CertificateSecret: payload.CertificateSecret,
		Ready:             payload.Ready,
		Warnings:          append([]string{}, payload.Warnings...),
	}
}

func printDomainStatusPayload(cmd *cobra.Command, payload *domainStatusPayload, diagnostic bool) error {
	if domainJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	out := cmd.OutOrStdout()
	title := "Sealtun Domain Status"
	if diagnostic {
		title = "Sealtun Domain Doctor"
	}
	fmt.Fprintln(out, title)
	fmt.Fprintf(out, "  Custom domains: %d (%d ready, %d not ready)\n", payload.CustomDomains, payload.Ready, payload.NotReady)
	fmt.Fprintf(out, "  Sessions scanned: %d\n", payload.TotalSessions)

	if len(payload.Items) == 0 {
		printDomainStatusWarnings(cmd, payload, true)
		return nil
	}

	if diagnostic {
		for _, item := range payload.Items {
			fmt.Fprintln(out, "")
			fmt.Fprintf(out, "Tunnel %s\n", item.TunnelID)
			fmt.Fprintf(out, "  Custom domain: %s\n", item.CustomDomain)
			fmt.Fprintf(out, "  Public host: %s\n", valueOr(item.PublicHost, "unknown"))
			fmt.Fprintf(out, "  Sealos CNAME target: %s\n", valueOr(item.SealosHost, "unknown"))
			fmt.Fprintf(out, "  DNS CNAME: %s\n", valueOr(item.DNSCNAME, "unavailable"))
			fmt.Fprintf(out, "  DNS ready: %s\n", yesNo(item.DNSReady))
			fmt.Fprintf(out, "  Ingress ready: %s\n", yesNo(item.IngressReady))
			fmt.Fprintf(out, "  Certificate: exists=%s ready=%s\n", yesNo(item.CertificateExists), yesNo(item.CertificateReady))
			fmt.Fprintf(out, "  Ready: %s\n", yesNo(item.Ready))
			for _, warning := range item.Warnings {
				fmt.Fprintf(out, "  Warning: %s\n", warning)
			}
		}
		printDomainStatusWarnings(cmd, payload, false)
		return nil
	}

	fmt.Fprintln(out, "")
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TUNNEL ID\tCUSTOM DOMAIN\tCNAME TARGET\tDNS\tINGRESS\tCERT\tREADY\tWARNINGS")
	for _, item := range payload.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\n",
			item.TunnelID,
			item.CustomDomain,
			valueOr(item.SealosHost, "-"),
			yesNo(item.DNSReady),
			yesNo(item.IngressReady),
			yesNo(item.CertificateReady),
			yesNo(item.Ready),
			len(item.Warnings),
		)
	}
	_ = w.Flush()
	printDomainStatusWarnings(cmd, payload, true)
	return nil
}

func printDomainStatusWarnings(cmd *cobra.Command, payload *domainStatusPayload, includeItems bool) {
	out := cmd.OutOrStdout()
	warnings := append([]string{}, payload.Warnings...)
	if includeItems {
		for _, item := range payload.Items {
			for _, warning := range item.Warnings {
				warnings = append(warnings, fmt.Sprintf("tunnel %s: %s", item.TunnelID, warning))
			}
		}
	}
	if len(warnings) == 0 {
		return
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Warnings")
	for _, warning := range warnings {
		fmt.Fprintf(out, "  - %s\n", warning)
	}
}

func verifySessionDomain(parent context.Context, tunnelID string) (*domainVerifyPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	_ = refreshSessionFromRemote(parent, sess)
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	if sess.CustomDomain == "" {
		return nil, fmt.Errorf("tunnel %s has no custom domain configured", sess.TunnelID)
	}

	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	return verifyDomainForSession(ctx, *sess), nil
}

func waitForSessionDomain(parent context.Context, tunnelID string, timeout time.Duration) (*domainVerifyPayload, error) {
	sess, err := findSession(tunnelID)
	if err != nil {
		return nil, err
	}
	_ = refreshSessionFromRemote(parent, sess)
	if !sessionSupportsCustomDomain(*sess) {
		return nil, fmt.Errorf("custom domains are only supported for https tunnels")
	}
	if sess.CustomDomain == "" {
		return nil, fmt.Errorf("tunnel %s has no custom domain configured", sess.TunnelID)
	}
	return waitForDomainReady(parent, *sess, timeout)
}

func domainVerifyResultError(payload *domainVerifyPayload, err error) error {
	if err != nil {
		return err
	}
	if payload != nil && !payload.Ready {
		return fmt.Errorf("custom domain %s is not ready", payload.CustomDomain)
	}
	return nil
}

func waitForDomainReady(parent context.Context, sess session.TunnelSession, timeout time.Duration) (*domainVerifyPayload, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("custom domain readiness timeout must be greater than 0")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var last *domainVerifyPayload
	for {
		checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
		payload := verifyDomainForSession(checkCtx, sess)
		checkCancel()
		last = payload
		if payload.Ready {
			return payload, nil
		}

		select {
		case <-ctx.Done():
			return last, fmt.Errorf("custom domain %s is not ready before timeout %s", sess.CustomDomain, timeout)
		case <-ticker.C:
		}
	}
}

func verifyDomainForSession(ctx context.Context, sess session.TunnelSession) *domainVerifyPayload {
	return verifyDomainForSessionWithRemote(ctx, sess, collectRemoteDiagnosticsWithContext)
}

func verifyDomainForSessionWithRemote(ctx context.Context, sess session.TunnelSession, remoteCollector remoteDiagnosticsCollector) *domainVerifyPayload {
	sealosHost := sessionSealosHostForDomain(sess, "")
	payload := &domainVerifyPayload{
		TunnelID:     sess.TunnelID,
		PublicHost:   sess.Host,
		SealosHost:   sealosHost,
		CustomDomain: sess.CustomDomain,
	}

	if payload.SealosHost == "" {
		payload.Warnings = append(payload.Warnings, "Sealos CNAME target is missing from the local session; reconfigure the domain or recreate the tunnel")
	} else {
		cname, dnsReady, dnsWarning := verifyDomainCNAME(ctx, sess.CustomDomain, payload.SealosHost)
		payload.DNSCNAME = cname
		payload.DNSReady = dnsReady
		if dnsWarning != "" {
			payload.Warnings = append(payload.Warnings, dnsWarning)
		}
	}

	if remoteCollector == nil {
		payload.Warnings = append(payload.Warnings, "remote certificate diagnostics unavailable: remote collector is disabled")
	} else if remote, err := remoteCollector(ctx, sess); err != nil {
		payload.Warnings = append(payload.Warnings, fmt.Sprintf("remote certificate diagnostics unavailable: %v", err))
	} else {
		payload.IngressReady = remote.Ingress.Exists &&
			domainListContains(remote.Ingress.Hosts, sess.CustomDomain) &&
			domainListContains(remote.Ingress.TLSHosts, sess.CustomDomain)
		if !payload.IngressReady {
			payload.Warnings = append(payload.Warnings, "remote ingress is not ready for the custom domain")
		}

		if remote.Certificate != nil {
			payload.CertificateExists = remote.Certificate.Exists
			payload.CertificateReady = remote.Certificate.Ready && domainListContains(remote.Certificate.DNSNames, sess.CustomDomain)
			payload.CertificateSecret = remote.Certificate.SecretName
		}
		if remote.Certificate == nil || !remote.Certificate.Exists {
			payload.Warnings = append(payload.Warnings, "custom domain certificate is missing")
		} else if !domainListContains(remote.Certificate.DNSNames, sess.CustomDomain) {
			payload.Warnings = append(payload.Warnings, fmt.Sprintf("custom domain certificate does not include DNS name %s", sess.CustomDomain))
		} else if !remote.Certificate.Ready {
			payload.Warnings = append(payload.Warnings, "custom domain certificate is not ready")
		}
	}

	payload.Ready = payload.DNSReady && payload.IngressReady && payload.CertificateReady
	return payload
}

func verifyDomainCNAME(ctx context.Context, customDomain, sealosHost string) (string, bool, string) {
	cname, err := lookupCNAME(ctx, customDomain)
	if err != nil {
		return "", false, fmt.Sprintf("DNS CNAME lookup failed for %s: %v", customDomain, err)
	}

	got := normalizeDNSName(cname)
	want := normalizeDNSName(sealosHost)
	if got != want {
		return got, false, fmt.Sprintf("DNS CNAME for %s points to %s, expected %s", customDomain, got, want)
	}
	return got, true, ""
}

func requireDomainCNAME(ctx context.Context, customDomain, sealosHost string) error {
	if sealosHost == "" {
		return fmt.Errorf("sealos CNAME target is missing; recreate the tunnel or inspect the session before attaching a custom domain")
	}
	_, ready, warning := verifyDomainCNAME(ctx, customDomain, sealosHost)
	if ready {
		return nil
	}
	if warning == "" {
		warning = fmt.Sprintf("DNS CNAME for %s does not point to %s", customDomain, sealosHost)
	}
	return fmt.Errorf("custom domain DNS is not verified: %s; create CNAME %s -> %s and retry", warning, customDomain, sealosHost)
}

func waitForDomainCNAMEReady(parent context.Context, customDomain, sealosHost string, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("custom domain DNS timeout must be greater than 0")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
		err := requireDomainCNAME(checkCtx, customDomain, sealosHost)
		checkCancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w before timeout %s", lastErr, timeout)
		case <-ticker.C:
		}
	}
}

func normalizeDNSName(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func domainListContains(values []string, want string) bool {
	normalizedWant := normalizeDNSName(want)
	for _, value := range values {
		if normalizeDNSName(value) == normalizedWant {
			return true
		}
	}
	return false
}

func printDomainVerifyPayload(cmd *cobra.Command, payload *domainVerifyPayload) error {
	if domainJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Tunnel %s custom domain verification\n", payload.TunnelID)
	fmt.Fprintf(out, "  Custom domain: %s\n", payload.CustomDomain)
	fmt.Fprintf(out, "  Sealos host: %s\n", payload.SealosHost)
	fmt.Fprintf(out, "  DNS CNAME: %s\n", valueOr(payload.DNSCNAME, "unavailable"))
	fmt.Fprintf(out, "  DNS ready: %s\n", yesNo(payload.DNSReady))
	fmt.Fprintf(out, "  Ingress ready: %s\n", yesNo(payload.IngressReady))
	fmt.Fprintf(out, "  Certificate: exists=%s ready=%s\n", yesNo(payload.CertificateExists), yesNo(payload.CertificateReady))
	fmt.Fprintf(out, "  Ready: %s\n", yesNo(payload.Ready))
	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
	return nil
}

func validateCustomDomain(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/:@") {
		return "", fmt.Errorf("invalid custom domain %q: provide a hostname only, not a URL", value)
	}
	if len(value) > 253 {
		return "", fmt.Errorf("invalid custom domain %q: hostname is too long", value)
	}
	if net.ParseIP(value) != nil {
		return "", fmt.Errorf("invalid custom domain %q: custom domain must be a DNS hostname, not an IP address", value)
	}
	if !strings.Contains(value, ".") {
		return "", fmt.Errorf("invalid custom domain %q: custom domain must contain at least two labels", value)
	}
	for _, label := range strings.Split(value, ".") {
		if !customDomainLabelPattern.MatchString(label) {
			return "", fmt.Errorf("invalid custom domain %q: label %q is not DNS compatible", value, label)
		}
	}
	return value, nil
}
