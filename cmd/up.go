package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labring/sealtun/pkg/auth"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const projectStateDirName = ".sealtun"
const projectStateFileName = "state.json"

type upOptions struct {
	Template string
	Yes      bool
	JSON     bool
	Guided   bool
}

type projectTunnelState struct {
	TunnelID    string `json:"tunnelId"`
	Name        string `json:"name,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	LocalPort   string `json:"localPort,omitempty"`
	TargetURL   string `json:"targetUrl,omitempty"`
	PublicURL   string `json:"publicUrl,omitempty"`
	PublicHost  string `json:"publicHost,omitempty"`
	PublicPort  int32  `json:"publicPort,omitempty"`
	Region      string `json:"region,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
	LastCommand string `json:"lastCommand,omitempty"`
}

type upPlan struct {
	Source      string       `json:"source"`
	TunnelID    string       `json:"tunnelId,omitempty"`
	Login       *upLoginPlan `json:"login,omitempty"`
	Name        string       `json:"name,omitempty"`
	Protocol    string       `json:"protocol"`
	LocalPort   string       `json:"localPort,omitempty"`
	TargetURL   string       `json:"targetUrl,omitempty"`
	Template    string       `json:"template,omitempty"`
	CommandArgs []string     `json:"commandArgs,omitempty"`
	SaveConfig  bool         `json:"saveConfig,omitempty"`
	ConfigPath  string       `json:"configPath,omitempty"`
	Existing    bool         `json:"existing"`
	Discovered  discoverItem `json:"discovered,omitempty"`
}

type upLoginPlan struct {
	LoggedIn  bool   `json:"loggedIn"`
	Region    string `json:"region,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Message   string `json:"message,omitempty"`
}

var upOpts upOptions
var upPortDiscoverer portDiscoverer = systemPortDiscoverer{}
var upStatusCollector = collectStatus
var upCommandInteractive = defaultUpCommandInteractive
var upConfigFileName = "sealtun.yaml"

var upCmd = &cobra.Command{
	Use:   "up [port]",
	Short: "Guide, reuse, or create a Sealtun tunnel with the expose engine",
	Long: `Discover, reuse, or create a Sealtun tunnel for the current project.

Without arguments, up first reuses the current project tunnel recorded in
.sealtun/state.json. If no project tunnel exists, interactive up runs a guided
wizard: login check, port selection, protocol, optional auth, rate limit, domain,
config save, and final confirmation. Explicit ports or --target skip the wizard
and use the same creation engine as sealtun expose.`,
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUp(cmd, args, upOpts)
	},
}

func init() {
	rootCmd.AddCommand(upCmd)
	registerExposeFlags(upCmd, true)
	upCmd.Flags().StringVar(&upOpts.Template, "template", "", "Protocol template hint: https, ssh, tcp, mysql, postgres, redis, mongodb, or mqtt")
	upCmd.Flags().BoolVar(&upOpts.Yes, "yes", false, "Skip confirmation only when the selected service is explicit or uniquely discovered")
	upCmd.Flags().BoolVar(&upOpts.JSON, "json", false, "Output the up plan or reused project tunnel as JSON")
	upCmd.Flags().BoolVar(&upOpts.Guided, "guided", false, "Run the full interactive setup wizard")
}

func runUp(cmd *cobra.Command, args []string, opts upOptions) error {
	explicitInput := opts.Guided || len(args) > 0 || strings.TrimSpace(exposeTarget) != "" || strings.TrimSpace(opts.Template) != ""
	if !explicitInput {
		if state, err := loadProjectTunnelState(); err == nil && strings.TrimSpace(state.TunnelID) != "" {
			if sess, sessionErr := session.Get(state.TunnelID); sessionErr == nil {
				current := stateFromSession(*sess, state.LastCommand)
				return printProjectTunnelState(cmd, &current, opts.JSON)
			}
			_ = removeProjectTunnelStateIfMatches(state.TunnelID)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	plan, err := buildUpPlan(cmd, args, opts, explicitInput)
	if err != nil {
		return err
	}
	if err := validateUpPlan(plan); err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}
	if plan.Source == "guided" {
		if err := writeUpPlanConfigIfRequested(plan); err != nil {
			return err
		}
	} else if plan.Source == "discovered" && !opts.Yes {
		if err := confirmUpPlan(cmd, plan); err != nil {
			return err
		}
	}

	existing := existingSessionIDs()
	if err := runExpose(cmd, plan.CommandArgs); err != nil {
		return err
	}
	sess, err := newestSessionNotIn(existing)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "[!] Tunnel created, but project state was not updated: %v\n", err)
		return nil
	}
	if err := saveProjectTunnelState(stateFromSession(*sess, "sealtun up "+strings.Join(os.Args[2:], " "))); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "[!] Tunnel created, but project state was not saved: %v\n", err)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "[+] Project state saved to %s\n", filepath.Join(projectStateDirName, projectStateFileName))
	return nil
}

func buildUpPlan(cmd *cobra.Command, args []string, opts upOptions, explicitInput bool) (*upPlan, error) {
	if opts.Guided || (!explicitInput && upCommandInteractive(cmd)) {
		return buildGuidedUpPlan(cmd, args, opts)
	}
	templateKind := ""
	if strings.TrimSpace(opts.Template) != "" {
		kind, err := applyUpTemplate(opts.Template, args)
		if err != nil {
			return nil, err
		}
		templateKind = kind
	}
	if len(args) > 0 || strings.TrimSpace(exposeTarget) != "" {
		localPort, targetURL, err := resolveExposeTarget(args, exposeTarget)
		if err != nil {
			return nil, err
		}
		return &upPlan{
			Source:      "explicit",
			Protocol:    tunnelprotocol.Normalize(protocol),
			LocalPort:   localPort,
			TargetURL:   targetURL,
			Template:    strings.TrimSpace(opts.Template),
			CommandArgs: args,
		}, nil
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := discoverLocalPorts(ctx, discoverOptions{Limit: 30, Protocol: "auto"}, upPortDiscoverer)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no local listening ports discovered; run `sealtun up 3000` or `sealtun up --target http://host:port`")
	}
	if templateKind != "" {
		spec, _ := protocolTemplateSpec(templateKind)
		selected := discoveredPortForTemplate(templateKind, spec.protocol, withPortHints(items))
		if selected.Port == 0 {
			return nil, fmt.Errorf("no local service matching template %q was discovered; pass an explicit port, for example `sealtun up %d --template %s`", templateKind, spec.port, templateKind)
		}
		selected = applyPortHints(selected)
		args = []string{strconv.Itoa(selected.Port)}
		protocol = spec.protocol
		return &upPlan{
			Source:      "discovered",
			Protocol:    tunnelprotocol.Normalize(protocol),
			LocalPort:   strconv.Itoa(selected.Port),
			TargetURL:   defaultLocalTargetURL(strconv.Itoa(selected.Port)),
			Template:    templateKind,
			CommandArgs: args,
			Discovered:  selected,
		}, nil
	}
	if explicitInput {
		return nil, fmt.Errorf("up needs an explicit port or target; run `sealtun up` interactively or pass a port")
	}
	selected, err := selectUpDiscoveredPort(cmd, items, opts.Yes)
	if err != nil {
		return nil, err
	}
	args = []string{strconv.Itoa(selected.Port)}
	if protocol == "" || protocol == tunnelprotocol.HTTPS {
		protocol = upProtocolForDiscoveredPort(selected)
	}
	return &upPlan{
		Source:      "discovered",
		Protocol:    tunnelprotocol.Normalize(protocol),
		LocalPort:   strconv.Itoa(selected.Port),
		TargetURL:   defaultLocalTargetURL(strconv.Itoa(selected.Port)),
		CommandArgs: args,
		Discovered:  selected,
	}, nil
}

func buildGuidedUpPlan(cmd *cobra.Command, args []string, opts upOptions) (*upPlan, error) {
	if !upCommandInteractive(cmd) {
		return nil, fmt.Errorf("guided up requires an interactive terminal; pass an explicit port/target for non-interactive use")
	}
	in := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Guided Up")

	login := collectUpLoginPlan()
	printUpLoginPlan(out, login)
	if !login.LoggedIn {
		return nil, fmt.Errorf("not logged in; run `sealtun login` first, then rerun `sealtun up --guided`")
	}

	targetURL := strings.TrimSpace(exposeTarget)
	selectedPort := ""
	discovered := discoverItem{}
	if targetURL != "" {
		localPort, normalizedTarget, err := resolveExposeTarget(args, targetURL)
		if err != nil {
			return nil, err
		}
		selectedPort = localPort
		targetURL = normalizedTarget
	} else {
		item, err := promptGuidedPort(cmd, in, out, args)
		if err != nil {
			return nil, err
		}
		discovered = item
		selectedPort = strconv.Itoa(item.Port)
		targetURL = defaultLocalTargetURL(selectedPort)
	}

	protocolChoice := tunnelprotocol.HTTPS
	templateKind := "https"
	if strings.TrimSpace(exposeTarget) != "" {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "3. Protocol")
		fmt.Fprintln(out, "  Remote HTTP upstream targets use the HTTPS public tunnel protocol.")
	} else {
		var err error
		protocolChoice, templateKind, err = promptGuidedProtocol(in, out, selectedPort, discovered, opts.Template)
		if err != nil {
			return nil, err
		}
	}
	protocol = protocolChoice

	name, err := promptString(in, out, "Tunnel name", defaultGuidedTunnelName(templateKind, selectedPort))
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name != "" {
		if _, err := applyTunnelID(name); err != nil {
			return nil, err
		}
	}

	if tunnelprotocol.IsHTTP(protocolChoice) {
		if err := promptGuidedHTTPSOptions(in, out); err != nil {
			return nil, err
		}
	} else {
		fmt.Fprintln(out, "HTTPS access controls and custom domains are skipped for SSH/TCP tunnels.")
	}

	saveConfig, err := promptYesNo(in, out, "Save this tunnel to sealtun.yaml?", false)
	if err != nil {
		return nil, err
	}

	plan := &upPlan{
		Source:      "guided",
		Login:       login,
		Name:        name,
		Protocol:    tunnelprotocol.Normalize(protocolChoice),
		LocalPort:   selectedPort,
		TargetURL:   targetURL,
		Template:    templateKind,
		CommandArgs: []string{selectedPort},
		SaveConfig:  saveConfig,
		ConfigPath:  upConfigFileName,
		Discovered:  discovered,
	}
	if strings.TrimSpace(exposeTarget) != "" {
		plan.CommandArgs = args
	}
	printGuidedUpSummary(out, plan)
	ok, err := promptYesNo(in, out, "Create this tunnel now?", true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("up canceled")
	}
	return plan, nil
}

func applyUpTemplate(template string, args []string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(template))
	spec, ok := protocolTemplateSpec(kind)
	if !ok {
		return "", fmt.Errorf("unsupported template %q; use https, ssh, tcp, mysql, postgres, redis, mongodb, or mqtt", template)
	}
	if len(args) == 0 && strings.TrimSpace(exposeTarget) == "" {
		return canonicalInitTemplateKind(kind, spec), nil
	}
	if protocol == "" || protocol == tunnelprotocol.HTTPS {
		protocol = spec.protocol
	}
	return canonicalInitTemplateKind(kind, spec), nil
}

func collectUpLoginPlan() *upLoginPlan {
	status, err := upStatusCollector()
	if err != nil {
		return &upLoginPlan{LoggedIn: false, Message: err.Error()}
	}
	plan := &upLoginPlan{
		LoggedIn:  status.LoggedIn && status.Kubeconfig.Present,
		Region:    status.Region,
		Namespace: status.Kubeconfig.Namespace,
	}
	if !status.LoggedIn {
		plan.Message = "not logged in"
	} else if !status.Kubeconfig.Present {
		plan.Message = "active kubeconfig is missing"
	}
	return plan
}

func printUpLoginPlan(out io.Writer, login *upLoginPlan) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "1. Login")
	fmt.Fprintf(out, "  Logged in: %s\n", yesNo(login != nil && login.LoggedIn))
	if login != nil {
		if login.Region != "" {
			fmt.Fprintf(out, "  Region: %s\n", login.Region)
		}
		if login.Namespace != "" {
			fmt.Fprintf(out, "  Namespace: %s\n", login.Namespace)
		}
		if login.Message != "" {
			fmt.Fprintf(out, "  Note: %s\n", login.Message)
		}
	}
}

func promptGuidedPort(cmd *cobra.Command, in *bufio.Reader, out io.Writer, args []string) (discoverItem, error) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "2. Local service")
	if len(args) > 0 {
		if err := validateLocalPort(args[0]); err != nil {
			return discoverItem{}, err
		}
		port, _ := strconv.Atoi(args[0])
		item := applyPortHints(discoverItem{Port: port, Address: "localhost"})
		fmt.Fprintf(out, "  Using explicit local port: %d\n", item.Port)
		return item, nil
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := discoverLocalPorts(ctx, discoverOptions{Limit: 30, Protocol: "auto"}, upPortDiscoverer)
	if err != nil {
		return discoverItem{}, err
	}
	if len(items) == 0 {
		value, err := promptStringRequired(in, out, "No local ports were discovered. Enter local port")
		if err != nil {
			return discoverItem{}, err
		}
		if err := validateLocalPort(value); err != nil {
			return discoverItem{}, err
		}
		port, _ := strconv.Atoi(value)
		return applyPortHints(discoverItem{Port: port, Address: "localhost"}), nil
	}
	if len(items) == 1 {
		item := applyPortHints(items[0])
		fmt.Fprintf(out, "  Found one local service: %d  %s  %s/%s\n", item.Port, valueOr(item.ProcessName, "-"), item.ProtocolHint, item.TemplateHint)
		ok, err := promptYesNo(in, out, fmt.Sprintf("Expose local port %d?", item.Port), true)
		if err != nil {
			return discoverItem{}, err
		}
		if ok {
			return item, nil
		}
		value, err := promptStringRequired(in, out, "Enter local port")
		if err != nil {
			return discoverItem{}, err
		}
		if err := validateLocalPort(value); err != nil {
			return discoverItem{}, err
		}
		port, _ := strconv.Atoi(value)
		return applyPortHints(discoverItem{Port: port, Address: "localhost"}), nil
	}
	fmt.Fprintln(out, "  Discovered local services:")
	for i, item := range items {
		item = applyPortHints(item)
		fmt.Fprintf(out, "    %d. %d  %s  %s/%s\n", i+1, item.Port, valueOr(item.ProcessName, "-"), item.ProtocolHint, item.TemplateHint)
	}
	choice, err := promptInt(in, out, fmt.Sprintf("Choose service [1-%d]", len(items)), 1, 1, len(items))
	if err != nil {
		return discoverItem{}, err
	}
	return applyPortHints(items[choice-1]), nil
}

func promptGuidedProtocol(in *bufio.Reader, out io.Writer, port string, item discoverItem, template string) (string, string, error) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "3. Protocol")
	if strings.TrimSpace(template) != "" {
		kind, err := applyUpTemplate(template, []string{port})
		if err != nil {
			return "", "", err
		}
		spec, _ := protocolTemplateSpec(kind)
		fmt.Fprintf(out, "  Using template %s -> protocol %s\n", kind, spec.protocol)
		return spec.protocol, kind, nil
	}
	item = applyPortHints(item)
	defaultKind := valueOr(item.TemplateHint, "https")
	choices := []string{"https", "ssh", "tcp", "mysql", "postgres", "redis", "mongodb", "mqtt"}
	fmt.Fprintln(out, "  1. https   Browser URL / webhook / HTTP target")
	fmt.Fprintln(out, "  2. ssh     Public SSH TCP endpoint")
	fmt.Fprintln(out, "  3. tcp     Generic raw TCP endpoint")
	fmt.Fprintln(out, "  4. mysql   MySQL over raw TCP")
	fmt.Fprintln(out, "  5. postgres PostgreSQL over raw TCP")
	fmt.Fprintln(out, "  6. redis   Redis over raw TCP")
	fmt.Fprintln(out, "  7. mongodb MongoDB over raw TCP")
	fmt.Fprintln(out, "  8. mqtt    MQTT over raw TCP")
	defaultIndex := indexOfString(choices, defaultKind)
	if defaultIndex < 0 {
		defaultIndex = 0
	}
	choice, err := promptInt(in, out, "Choose protocol", defaultIndex+1, 1, len(choices))
	if err != nil {
		return "", "", err
	}
	kind := choices[choice-1]
	spec, _ := protocolTemplateSpec(kind)
	return spec.protocol, canonicalInitTemplateKind(kind, spec), nil
}

func promptGuidedHTTPSOptions(in *bufio.Reader, out io.Writer) error {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "4. Public access")
	enableAuth, err := promptYesNo(in, out, "Enable Basic Auth?", false)
	if err != nil {
		return err
	}
	if enableAuth {
		user, err := promptString(in, out, "Basic Auth username", "admin")
		if err != nil {
			return err
		}
		envName, err := promptString(in, out, "Password environment variable", "SEALTUN_BASIC_AUTH_PASSWORD")
		if err != nil {
			return err
		}
		basicAuthUser = strings.TrimSpace(user)
		basicAuthPasswordEnv = strings.TrimSpace(envName)
		fmt.Fprintf(out, "  Set %s before creating/applying if it is not already exported.\n", basicAuthPasswordEnv)
	}

	enableRateLimit, err := promptYesNo(in, out, "Enable rate limit?", false)
	if err != nil {
		return err
	}
	if enableRateLimit {
		value, err := promptString(in, out, "Rate limit", "60/m")
		if err != nil {
			return err
		}
		accessRateLimit = strings.TrimSpace(value)
		enableAudit, err := promptYesNo(in, out, "Enable access audit?", true)
		if err != nil {
			return err
		}
		accessAuditEnabled = enableAudit
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "5. Domain")
	useDomain, err := promptYesNo(in, out, "Use a custom domain now?", false)
	if err != nil {
		return err
	}
	if useDomain {
		domain, err := promptStringRequired(in, out, "Custom domain")
		if err != nil {
			return err
		}
		normalized, err := validateCustomDomain(domain)
		if err != nil {
			return err
		}
		customDomain = normalized
		wait, err := promptYesNo(in, out, "Wait for DNS/certificate readiness?", false)
		if err != nil {
			return err
		}
		waitDomain = wait
	}
	return nil
}

func selectUpDiscoveredPort(cmd *cobra.Command, items []discoverItem, allowUniqueYes bool) (discoverItem, error) {
	if len(items) == 1 {
		item := applyPortHints(items[0])
		if !upCommandInteractive(cmd) && !allowUniqueYes {
			return discoverItem{}, fmt.Errorf("one local service was discovered on port %d, but non-interactive up requires an explicit port or --yes", item.Port)
		}
		return item, nil
	}
	if !upCommandInteractive(cmd) {
		return discoverItem{}, fmt.Errorf("%d local services were discovered; run `sealtun discover` and then `sealtun up <port>`", len(items))
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Select a local service to expose:")
	for i, item := range items {
		item = applyPortHints(item)
		fmt.Fprintf(out, "  %d. %d  %s  %s/%s\n", i+1, item.Port, valueOr(item.ProcessName, "-"), item.ProtocolHint, item.TemplateHint)
	}
	fmt.Fprintf(out, "Choose [1-%d]: ", len(items))
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return discoverItem{}, err
	}
	choice, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || choice < 1 || choice > len(items) {
		return discoverItem{}, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return applyPortHints(items[choice-1]), nil
}

func validateUpPlan(plan *upPlan) error {
	if plan == nil {
		return fmt.Errorf("up plan is required")
	}
	if plan.Protocol == "" {
		plan.Protocol = tunnelprotocol.HTTPS
	}
	if err := validateProtocol(plan.Protocol); err != nil {
		return err
	}
	if strings.TrimSpace(plan.TargetURL) != "" && !tunnelprotocol.IsHTTP(plan.Protocol) {
		return fmt.Errorf("--target is only supported for https tunnels")
	}
	if targetTLSInsecureSkipVerify && strings.TrimSpace(plan.TargetURL) == "" {
		return fmt.Errorf("--target-insecure-skip-verify requires --target with an https URL")
	}
	if tunnelprotocol.IsHTTP(plan.Protocol) {
		if _, err := resolveBasicAuth(basicAuthInput{
			Credential:  basicAuthCredential,
			Username:    basicAuthUser,
			Password:    basicAuthPassword,
			PasswordEnv: basicAuthPasswordEnv,
		}, getenv); err != nil {
			return err
		}
		if _, err := resolveAccessPolicy(accessPolicyInput{
			BearerToken:       bearerToken,
			BearerTokenEnv:    bearerTokenEnv,
			IPAllowlist:       ipAllowlist,
			IPDenylist:        ipDenylist,
			TemporaryToken:    temporaryAccessToken,
			TemporaryTokenEnv: temporaryAccessTokenEnv,
			TemporaryTTL:      temporaryAccessTTL,
			TemporaryName:     "default",
			RateLimit:         accessRateLimit,
			AuditEnabled:      accessAuditEnabled,
		}, time.Now(), getenv); err != nil {
			return err
		}
	} else if basicAuthCredential != "" || basicAuthUser != "" || basicAuthPassword != "" || basicAuthPasswordEnv != "" || bearerToken != "" || bearerTokenEnv != "" || len(ipAllowlist) > 0 || len(ipDenylist) > 0 || temporaryAccessToken != "" || temporaryAccessTokenEnv != "" || accessRateLimit != "" || accessAuditEnabled {
		return fmt.Errorf("access policy flags are only supported for https tunnels")
	}
	return nil
}

func confirmUpPlan(cmd *cobra.Command, plan *upPlan) error {
	if !upCommandInteractive(cmd) {
		return fmt.Errorf("confirmation is required; pass an explicit port/target or run in an interactive terminal")
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Up Plan")
	fmt.Fprintf(out, "  Source: %s\n", plan.Source)
	fmt.Fprintf(out, "  Protocol: %s\n", plan.Protocol)
	fmt.Fprintf(out, "  Target: %s\n", valueOr(plan.TargetURL, "localhost:"+plan.LocalPort))
	if plan.Template != "" {
		fmt.Fprintf(out, "  Template: %s\n", plan.Template)
	}
	fmt.Fprint(out, "Create this tunnel? [Y/n]: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" || answer == "y" || answer == "yes" {
		return nil
	}
	return fmt.Errorf("up canceled")
}

func printGuidedUpSummary(out io.Writer, plan *upPlan) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "6. Review")
	fmt.Fprintf(out, "  Name: %s\n", valueOr(plan.Name, "-"))
	fmt.Fprintf(out, "  Protocol: %s\n", plan.Protocol)
	fmt.Fprintf(out, "  Target: %s\n", valueOr(plan.TargetURL, "localhost:"+plan.LocalPort))
	if basicAuthUser != "" || basicAuthPasswordEnv != "" {
		fmt.Fprintf(out, "  Basic Auth: enabled user=%s passwordEnv=%s\n", valueOr(basicAuthUser, "-"), valueOr(basicAuthPasswordEnv, "-"))
	} else {
		fmt.Fprintln(out, "  Basic Auth: skipped")
	}
	if accessRateLimit != "" {
		fmt.Fprintf(out, "  Rate limit: %s\n", accessRateLimit)
		fmt.Fprintf(out, "  Audit: %s\n", yesNo(accessAuditEnabled))
	} else {
		fmt.Fprintln(out, "  Rate limit: skipped")
	}
	if customDomain != "" {
		fmt.Fprintf(out, "  Domain: %s wait=%s\n", customDomain, yesNo(waitDomain))
	} else {
		fmt.Fprintln(out, "  Domain: skipped")
	}
	if plan.SaveConfig {
		fmt.Fprintf(out, "  Config: save to %s\n", plan.ConfigPath)
	} else {
		fmt.Fprintln(out, "  Config: not saved")
	}
}

func upProtocolForDiscoveredPort(item discoverItem) string {
	item = applyPortHints(item)
	switch item.TemplateHint {
	case "ssh":
		return tunnelprotocol.SSH
	case "mysql", "postgres", "redis", "mongodb", "mqtt":
		return tunnelprotocol.TCP
	default:
		if item.ProtocolHint == tunnelprotocol.SSH || item.ProtocolHint == tunnelprotocol.TCP {
			return item.ProtocolHint
		}
		return tunnelprotocol.HTTPS
	}
}

func withPortHints(items []discoverItem) []discoverItem {
	out := make([]discoverItem, 0, len(items))
	for _, item := range items {
		out = append(out, applyPortHints(item))
	}
	return out
}

func promptString(in *bufio.Reader, out io.Writer, label, fallback string) (string, error) {
	if fallback != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, fallback)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func promptStringRequired(in *bufio.Reader, out io.Writer, label string) (string, error) {
	value, err := promptString(in, out, label, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return value, nil
}

func promptYesNo(in *bufio.Reader, out io.Writer, label string, fallback bool) (bool, error) {
	suffix := "y/N"
	if fallback {
		suffix = "Y/n"
	}
	fmt.Fprintf(out, "%s [%s]: ", label, suffix)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	value := strings.ToLower(strings.TrimSpace(line))
	if value == "" {
		return fallback, nil
	}
	switch value {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid yes/no answer %q", value)
	}
}

func promptInt(in *bufio.Reader, out io.Writer, label string, fallback, min, max int) (int, error) {
	fmt.Fprintf(out, "%s [%d]: ", label, fallback)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("%s must be between %d and %d", label, min, max)
	}
	return parsed, nil
}

func indexOfString(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func defaultUpCommandInteractive(cmd *cobra.Command) bool {
	file, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func projectStatePath(create bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cwd, projectStateDirName)
	if create {
		if _, err := auth.EnsurePrivateDir(dir, "project state directory"); err != nil {
			return "", err
		}
	} else if info, statErr := os.Lstat(dir); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("project state directory %s is a symlink", dir)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("project state path %s is not a directory", dir)
		}
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	return filepath.Join(dir, projectStateFileName), nil
}

func writeUpPlanConfigIfRequested(plan *upPlan) error {
	if plan == nil || !plan.SaveConfig {
		return nil
	}
	config := upPlanApplyFile(plan)
	data, err := marshalApplyFileYAML(config)
	if err != nil {
		return err
	}
	return writeRegularFileAtomic(plan.ConfigPath, data, 0o600, "up config")
}

func upPlanApplyFile(plan *upPlan) *applyFile {
	name := strings.TrimSpace(plan.Name)
	if name == "" {
		name = defaultGuidedTunnelName(plan.Template, plan.LocalPort)
	}
	port, _ := strconv.Atoi(plan.LocalPort)
	item := applyTunnel{
		Name:       name,
		LocalPort:  port,
		Target:     strings.TrimSpace(exposeTarget),
		Protocol:   plan.Protocol,
		Domain:     customDomain,
		WaitDomain: waitDomain,
	}
	if item.Target == "" && strings.TrimSpace(plan.TargetURL) != defaultLocalTargetURL(plan.LocalPort) {
		item.Target = strings.TrimSpace(plan.TargetURL)
	}
	if targetTLSInsecureSkipVerify {
		item.TargetTLS = &applyTargetTLS{InsecureSkipVerify: true}
	}
	if basicAuthUser != "" || basicAuthPasswordEnv != "" {
		item.BasicAuth = &applyBasicAuth{Username: basicAuthUser, PasswordEnv: basicAuthPasswordEnv}
	}
	if accessRateLimit != "" || accessAuditEnabled {
		item.AccessPolicy = &applyAccessPolicy{RateLimit: accessRateLimit}
		if accessAuditEnabled {
			item.AccessPolicy.Audit = &applyAuditConfig{Enabled: true}
		}
	}
	return &applyFile{Version: "v1", Tunnels: []applyTunnel{item}}
}

func marshalApplyFileYAML(config *applyFile) ([]byte, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(config); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func defaultGuidedTunnelName(template, port string) string {
	template = strings.TrimSpace(template)
	if template == "" || template == "https" {
		template = "web"
	}
	name := strings.ToLower(template)
	name = strings.ReplaceAll(name, "_", "-")
	if _, err := applyTunnelID(name); err == nil {
		return name
	}
	return "web-" + port
}

func loadProjectTunnelState() (*projectTunnelState, error) {
	path, err := projectStatePath(false)
	if err != nil {
		return nil, err
	}
	data, err := readRegularFile(path, "project state file")
	if err != nil {
		return nil, err
	}
	var state projectTunnelState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &state, nil
}

func saveProjectTunnelState(state projectTunnelState) error {
	path, err := projectStatePath(true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.UpdatedAt) == "" {
		state.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeRegularFileAtomic(path, append(data, '\n'), 0o600, "project state")
}

func removeProjectTunnelStateIfMatches(tunnelID string) error {
	state, err := loadProjectTunnelState()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.TunnelID != tunnelID {
		return nil
	}
	path, err := projectStatePath(false)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Dir(path))
	return nil
}

func printProjectTunnelState(cmd *cobra.Command, state *projectTunnelState, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(state)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun project tunnel")
	fmt.Fprintf(out, "  Tunnel ID: %s\n", state.TunnelID)
	fmt.Fprintf(out, "  Protocol: %s\n", valueOr(state.Protocol, "-"))
	fmt.Fprintf(out, "  Target: %s\n", valueOr(state.TargetURL, "localhost:"+state.LocalPort))
	if state.PublicPort != 0 && (state.Protocol == tunnelprotocol.SSH || state.Protocol == tunnelprotocol.TCP) {
		fmt.Fprintf(out, "  Endpoint: %s:%d\n", valueOr(state.PublicHost, "-"), state.PublicPort)
	} else {
		fmt.Fprintf(out, "  URL: %s\n", valueOr(state.PublicURL, "-"))
	}
	fmt.Fprintf(out, "  Namespace: %s\n", valueOr(state.Namespace, "-"))
	fmt.Fprintf(out, "  Next: sealtun inspect %s\n", state.TunnelID)
	return nil
}

func stateFromSession(sess session.TunnelSession, command string) projectTunnelState {
	return projectTunnelState{
		TunnelID:    sess.TunnelID,
		Protocol:    sessionProtocol(sess),
		LocalPort:   sess.LocalPort,
		TargetURL:   sessionTargetURL(sess),
		PublicURL:   endpointLabel(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort),
		PublicHost:  endpointDisplay(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort).Host,
		PublicPort:  sess.PublicPort,
		Region:      sess.Region,
		Namespace:   sess.Namespace,
		UpdatedAt:   time.Now().Format(time.RFC3339),
		LastCommand: strings.TrimSpace(command),
	}
}

func existingSessionIDs() map[string]bool {
	sessions, err := session.List()
	if err != nil {
		return map[string]bool{}
	}
	ids := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		ids[sess.TunnelID] = true
	}
	return ids
}

func newestSessionNotIn(existing map[string]bool) (*session.TunnelSession, error) {
	sessions, err := session.List()
	if err != nil {
		return nil, err
	}
	var best *session.TunnelSession
	var bestTime time.Time
	for i := range sessions {
		if existing[sessions[i].TunnelID] {
			continue
		}
		created, err := time.Parse(time.RFC3339, sessions[i].CreatedAt)
		if err != nil {
			continue
		}
		if best == nil || created.After(bestTime) {
			copy := sessions[i]
			best = &copy
			bestTime = created
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no newly created tunnel session found")
	}
	return best, nil
}
