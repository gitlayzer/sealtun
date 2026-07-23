package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/spf13/cobra"
)

type initOptions struct {
	JSON     bool
	Apply    bool
	Protocol string
	Port     int
	Name     string
	Domain   string
	Limit    int
}

type initPayload struct {
	Status         *statusPayload     `json:"status,omitempty"`
	Discovered     []discoverItem     `json:"discovered,omitempty"`
	Recommendation initRecommendation `json:"recommendation"`
	Results        []applyResult      `json:"results,omitempty"`
	Warnings       []string           `json:"warnings,omitempty"`
}

type initRecommendation struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	Template  string `json:"template"`
	LocalPort int    `json:"localPort"`
	Domain    string `json:"domain,omitempty"`
	Command   string `json:"command"`
	YAML      string `json:"yaml"`
	Applied   bool   `json:"applied,omitempty"`
}

var initOpts initOptions

var initStatusCollector = collectStatus
var initPortDiscoverer portDiscoverer = systemPortDiscoverer{}
var initApplyRunner = runApplyConfig

var initCmd = &cobra.Command{
	Use:          "init",
	Short:        "Guide first-time Sealtun setup and tunnel creation",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := buildInitPayload(cmd.Context(), initOpts)
		if err != nil {
			return err
		}
		if initOpts.Apply {
			config := applyConfigForRecommendation(payload.Recommendation)
			results, err := initApplyRunner(cmd.Context(), config, false)
			if err != nil {
				return err
			}
			payload.Results = results
			payload.Recommendation.Applied = true
		}
		if initOpts.JSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		printInitPayload(cmd, payload)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	markAlpha(initCmd)
	initCmd.Flags().BoolVar(&initOpts.JSON, "json", false, "Output onboarding recommendation as JSON")
	initCmd.Flags().BoolVar(&initOpts.Apply, "apply", false, "Create the recommended tunnel")
	initCmd.Flags().StringVar(&initOpts.Protocol, "protocol", "auto", "Recommended protocol: auto, https, ssh, tcp, mysql, postgres, redis, mongodb, or mqtt")
	initCmd.Flags().IntVar(&initOpts.Port, "port", 0, "Local port to use for the recommendation")
	initCmd.Flags().StringVar(&initOpts.Name, "name", "", "Tunnel name for the generated sealtun.yaml")
	initCmd.Flags().StringVar(&initOpts.Domain, "domain", "", "Custom domain for an HTTPS recommendation")
	initCmd.Flags().IntVar(&initOpts.Limit, "limit", 10, "Maximum discovered local ports to show")
}

func buildInitPayload(ctx context.Context, opts initOptions) (*initPayload, error) {
	if opts.Limit < 1 || opts.Limit > 200 {
		return nil, fmt.Errorf("limit must be between 1 and 200")
	}
	status, err := initStatusCollector()
	if err != nil {
		return nil, err
	}
	ports, err := discoverLocalPorts(ctx, discoverOptions{Limit: opts.Limit, Protocol: "auto"}, initPortDiscoverer)
	if err != nil {
		return nil, err
	}
	rec, err := initRecommendationFromOptions(opts, ports)
	if err != nil {
		return nil, err
	}
	payload := &initPayload{
		Status:         status,
		Discovered:     ports,
		Recommendation: rec,
	}
	if !status.LoggedIn {
		payload.Warnings = append(payload.Warnings, "not logged in; run `sealtun login` before creating a tunnel")
	}
	if !status.Kubeconfig.Present {
		payload.Warnings = append(payload.Warnings, "active kubeconfig is missing; run `sealtun login` before creating cloud resources")
	}
	if len(ports) == 0 && opts.Port == 0 {
		payload.Warnings = append(payload.Warnings, "no local listening ports discovered; defaulted to localhost:"+strconv.Itoa(rec.LocalPort))
	}
	return payload, nil
}

func initRecommendationFromOptions(opts initOptions, ports []discoverItem) (initRecommendation, error) {
	kind := strings.ToLower(strings.TrimSpace(opts.Protocol))
	if kind == "" {
		kind = "auto"
	}
	selected := discoverItem{}
	explicitKind := kind != "auto"
	if opts.Port != 0 {
		if err := validateLocalPort(strconv.Itoa(opts.Port)); err != nil {
			return initRecommendation{}, err
		}
		selected = applyPortHints(discoverItem{Port: opts.Port, Address: "localhost"})
	} else if len(ports) > 0 {
		selected = ports[0]
	}
	if kind == "auto" {
		kind = valueOr(selected.TemplateHint, "https")
	}
	spec, ok := protocolTemplateSpec(kind)
	if !ok {
		return initRecommendation{}, fmt.Errorf("unsupported protocol %q; use auto, https, ssh, tcp, mysql, postgres, redis, mongodb, or mqtt", opts.Protocol)
	}
	templateKind := canonicalInitTemplateKind(kind, spec)
	if explicitKind && opts.Port == 0 {
		selected = discoveredPortForTemplate(templateKind, spec.protocol, ports)
	}
	port := opts.Port
	if port == 0 && selected.Port != 0 {
		port = selected.Port
	}
	if port == 0 {
		port = spec.port
	}
	if err := validateLocalPort(strconv.Itoa(port)); err != nil {
		return initRecommendation{}, err
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = spec.name
	}
	if _, err := applyTunnelID(name); err != nil {
		return initRecommendation{}, err
	}
	domain, err := validateCustomDomain(opts.Domain)
	if err != nil {
		return initRecommendation{}, err
	}
	if domain != "" && spec.protocol != tunnelprotocol.HTTPS {
		return initRecommendation{}, fmt.Errorf("--domain is only supported for https recommendations")
	}
	yaml := protocolTemplateYAML(name, port, spec.protocol, domain)
	command := exposePreviewCommand(port, spec.protocol, domain, domain != "")
	return initRecommendation{
		Name:      name,
		Protocol:  spec.protocol,
		Template:  templateKind,
		LocalPort: port,
		Domain:    domain,
		Command:   command,
		YAML:      yaml,
	}, nil
}

func canonicalInitTemplateKind(kind string, spec templateSpec) string {
	switch kind {
	case "http", "web":
		return "https"
	case "postgresql":
		return "postgres"
	default:
		if kind == "" || kind == "auto" {
			return spec.name
		}
		return kind
	}
}

func discoveredPortForTemplate(templateKind, protocol string, ports []discoverItem) discoverItem {
	if !initTemplateUsesDiscoveredPort(templateKind) {
		return discoverItem{}
	}
	for _, item := range ports {
		item = applyPortHints(item)
		if item.TemplateHint == templateKind {
			return item
		}
	}
	if templateKind == "tcp" {
		for _, item := range ports {
			item = applyPortHints(item)
			if item.ProtocolHint == protocol {
				return item
			}
		}
	}
	if templateKind == "https" {
		for _, item := range ports {
			item = applyPortHints(item)
			if item.ProtocolHint == protocol {
				return item
			}
		}
	}
	return discoverItem{}
}

func initTemplateUsesDiscoveredPort(templateKind string) bool {
	switch templateKind {
	case "https", "ssh", "tcp", "mysql", "postgres", "redis", "mongodb", "mqtt":
		return true
	default:
		return false
	}
}

func applyConfigForRecommendation(rec initRecommendation) *applyFile {
	return &applyFile{
		Version: "v1",
		Tunnels: []applyTunnel{{
			Name:       rec.Name,
			LocalPort:  rec.LocalPort,
			Protocol:   rec.Protocol,
			Domain:     rec.Domain,
			WaitDomain: strings.Contains(rec.YAML, "waitDomain: true"),
		}},
	}
}

func printInitPayload(cmd *cobra.Command, payload *initPayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Init")
	if payload.Status != nil {
		fmt.Fprintf(out, "  Logged in: %s\n", yesNo(payload.Status.LoggedIn))
		fmt.Fprintf(out, "  Active profile: %s\n", valueOr(payload.Status.ActiveProfile, "-"))
		fmt.Fprintf(out, "  Region: %s\n", valueOr(payload.Status.Region, "-"))
		fmt.Fprintf(out, "  Namespace: %s\n", valueOr(payload.Status.Kubeconfig.Namespace, "-"))
	}
	if len(payload.Warnings) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Warnings")
		for _, warning := range payload.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
	if len(payload.Discovered) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Discovered Local Ports")
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PORT\tPROCESS\tPROTOCOL\tTEMPLATE")
		for _, item := range payload.Discovered {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", item.Port, valueOr(item.ProcessName, "-"), item.ProtocolHint, item.TemplateHint)
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Recommended Command")
	fmt.Fprintf(out, "  %s\n", payload.Recommendation.Command)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "sealtun.yaml")
	fmt.Fprintln(out, payload.Recommendation.YAML)
	if payload.Recommendation.Applied {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Applied: yes")
		if len(payload.Results) > 0 {
			printApplyResults(cmd, payload.Results, false)
		}
	} else {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "No resources were created. Add --apply to create the recommended tunnel.")
	}
}
