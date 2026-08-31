package cmd

import (
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
)

// Protocol templates map `up --template <kind>` and guided flow choices to a
// protocol and default port. There is no standalone template command.

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
