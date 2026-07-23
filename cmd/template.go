package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/spf13/cobra"
)

type protocolTemplate struct {
	Protocol    string   `json:"protocol"`
	Name        string   `json:"name"`
	LocalPort   int      `json:"localPort"`
	Description string   `json:"description"`
	Command     string   `json:"command"`
	Config      string   `json:"config"`
	Notes       []string `json:"notes,omitempty"`
}

var templateJSON bool
var templateName string
var templatePort int
var templateDomain string

var templateCmd = &cobra.Command{
	Use:          "template [https|ssh|tcp|mysql|postgres|redis|mongodb|mqtt]",
	Short:        "Generate protocol-specific Sealtun command and YAML templates",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		tmpl, err := buildProtocolTemplate(args[0], templateName, templatePort, templateDomain)
		if err != nil {
			return err
		}
		if templateJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(tmpl)
		}
		printProtocolTemplate(cmd, tmpl)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(templateCmd)
	markAlpha(templateCmd)
	templateCmd.Flags().BoolVar(&templateJSON, "json", false, "Output template as JSON")
	templateCmd.Flags().StringVar(&templateName, "name", "", "Tunnel name for the YAML template")
	templateCmd.Flags().IntVar(&templatePort, "port", 0, "Local port for the template")
	templateCmd.Flags().StringVar(&templateDomain, "domain", "", "Custom domain for HTTPS templates")
}

func buildProtocolTemplate(kind, name string, port int, domain string) (*protocolTemplate, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	spec, ok := protocolTemplateSpec(kind)
	if !ok {
		return nil, fmt.Errorf("unsupported template %q; use https, ssh, tcp, mysql, postgres, redis, mongodb, or mqtt", kind)
	}
	if name == "" {
		name = spec.name
	}
	if _, err := applyTunnelID(name); err != nil {
		return nil, err
	}
	if port == 0 {
		port = spec.port
	}
	if err := validateLocalPort(fmt.Sprint(port)); err != nil {
		return nil, err
	}
	normalizedDomain, err := validateCustomDomain(domain)
	if err != nil {
		return nil, err
	}
	if normalizedDomain != "" && spec.protocol != tunnelprotocol.HTTPS {
		return nil, fmt.Errorf("--domain is only supported for https templates")
	}

	tmpl := &protocolTemplate{
		Protocol:    spec.protocol,
		Name:        name,
		LocalPort:   port,
		Description: spec.description,
		Command:     fmt.Sprintf("sealtun expose %d --protocol %s", port, spec.protocol),
		Notes:       append([]string{}, spec.notes...),
	}
	if spec.protocol == tunnelprotocol.HTTPS && normalizedDomain != "" {
		tmpl.Command += fmt.Sprintf(" --domain %s --wait-domain", normalizedDomain)
	}
	tmpl.Config = protocolTemplateYAML(name, port, spec.protocol, normalizedDomain)
	return tmpl, nil
}

type templateSpec struct {
	name        string
	port        int
	protocol    string
	description string
	notes       []string
}

func protocolTemplateSpec(kind string) (templateSpec, bool) {
	switch kind {
	case "https", "http", "web":
		return templateSpec{
			name:        "web",
			port:        3000,
			protocol:    tunnelprotocol.HTTPS,
			description: "Expose a local HTTP service through a public HTTPS URL.",
			notes:       []string{"HTTPS templates support custom domains and public access policies."},
		}, true
	case "ssh":
		return templateSpec{
			name:        "ssh",
			port:        22,
			protocol:    tunnelprotocol.SSH,
			description: "Expose local SSH through a public TCP NodePort endpoint.",
			notes:       []string{"SSH uses raw TCP only; Basic Auth, Bearer tokens, and custom domains do not apply."},
		}, true
	case "tcp":
		return templateSpec{
			name:        "tcp",
			port:        9000,
			protocol:    tunnelprotocol.TCP,
			description: "Expose a generic local TCP service through a public host and port.",
			notes:       []string{"TCP uses raw TCP NodePort and does not support HTTPS access policies."},
		}, true
	case "mysql":
		return templateSpec{name: "mysql", port: 3306, protocol: tunnelprotocol.TCP, description: "Expose a local MySQL service over raw TCP."}, true
	case "postgres", "postgresql":
		return templateSpec{name: "postgres", port: 5432, protocol: tunnelprotocol.TCP, description: "Expose a local PostgreSQL service over raw TCP."}, true
	case "redis":
		return templateSpec{name: "redis", port: 6379, protocol: tunnelprotocol.TCP, description: "Expose a local Redis service over raw TCP."}, true
	case "mongodb":
		return templateSpec{name: "mongodb", port: 27017, protocol: tunnelprotocol.TCP, description: "Expose a local MongoDB service over raw TCP."}, true
	case "mqtt":
		return templateSpec{name: "mqtt", port: 1883, protocol: tunnelprotocol.TCP, description: "Expose a local MQTT broker over raw TCP."}, true
	default:
		return templateSpec{}, false
	}
}

func protocolTemplateYAML(name string, port int, protocol string, domain string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "version: v1")
	fmt.Fprintln(&b, "tunnels:")
	fmt.Fprintf(&b, "  - name: %s\n", name)
	fmt.Fprintf(&b, "    protocol: %s\n", protocol)
	fmt.Fprintf(&b, "    localPort: %d\n", port)
	if domain != "" {
		fmt.Fprintf(&b, "    domain: %s\n", domain)
		fmt.Fprintln(&b, "    waitDomain: true")
	}
	return strings.TrimRight(b.String(), "\n")
}

func printProtocolTemplate(cmd *cobra.Command, tmpl *protocolTemplate) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sealtun %s template\n", strings.ToUpper(tmpl.Protocol))
	fmt.Fprintf(out, "  %s\n", tmpl.Description)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Command")
	fmt.Fprintf(out, "  %s\n", tmpl.Command)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "sealtun.yaml")
	fmt.Fprintln(out, tmpl.Config)
	if len(tmpl.Notes) > 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Notes")
		for _, note := range tmpl.Notes {
			fmt.Fprintf(out, "  - %s\n", note)
		}
	}
}
