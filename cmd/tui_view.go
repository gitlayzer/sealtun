package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m tuiModel) View() string {
	if m.width == 0 {
		return "Loading Sealtun TUI [Alpha]..."
	}
	header := tuiHeader(m.status)
	body := ""
	switch m.view {
	case tuiViewCreate:
		body = m.createView()
	case tuiViewOperations:
		body = m.operationsView()
	case tuiViewDiagnostics:
		body = m.diagnosticsView()
	case tuiViewTunnelActions:
		body = m.tunnelActionsView()
	case tuiViewDetails:
		body = m.detailsView()
	case tuiViewConfirm:
		body = m.confirmView()
	case tuiViewPrompt:
		body = m.promptView()
	default:
		body = m.tunnelsView()
	}
	footer := tuiFooter(m)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m tuiModel) tunnelsView() string {
	help := "enter inspect  o actions  d doctor  m metrics  e events  s stop/start  x cleanup"
	if len(m.items) == 0 {
		help = "No tunnels yet. Press tab to create one from a local port, or tab again for global tools."
	}
	return tuiLayout(m.menu.View(), lipgloss.JoinVertical(lipgloss.Left, tuiSection("Tunnels", help), m.tunnels.View()))
}

func (m tuiModel) createView() string {
	help := "enter create selected port  r refresh  tab switch"
	if len(m.disco) == 0 {
		help = "No listening ports found. Use sealtun up <port> or sealtun expose <port>."
	}
	return tuiLayout(m.menu.View(), lipgloss.JoinVertical(lipgloss.Left, tuiSection("Create Tunnel", help), m.ports.View()))
}

func (m tuiModel) operationsView() string {
	return tuiLayout(m.menu.View(), lipgloss.JoinVertical(lipgloss.Left, tuiSection("Tools", "global tools only  enter run/open  esc tunnels"), m.ops.View()))
}

func (m tuiModel) tunnelActionsView() string {
	item, ok := m.selectedTunnelItem()
	if !ok {
		body := tuiCard(tuiSection("Tunnel Actions", "No tunnel selected.") + "\n\nGo back to Tunnels, select one, then press o.")
		return tuiLayout(m.menu.View(), body)
	}
	help := fmt.Sprintf("selected %s  enter run/open  esc tunnels", item.value)
	return tuiLayout(m.menu.View(), lipgloss.JoinVertical(lipgloss.Left, tuiSection("Tunnel Actions", help), m.actions.View()))
}

func (m tuiModel) diagnosticsView() string {
	var b strings.Builder
	b.WriteString(tuiSection("Status", "Global health summary. Press r to refresh."))
	if m.status == nil {
		b.WriteString("\nNo status data loaded.")
		return tuiLayout(m.menu.View(), b.String())
	}
	fmt.Fprintf(&b, "\nLogged in: %s\n", yesNo(m.status.LoggedIn))
	fmt.Fprintf(&b, "Daemon: %s\n", runningLabel(m.status.DaemonRunning))
	fmt.Fprintf(&b, "Region: %s\n", valueOr(m.status.Region, "-"))
	fmt.Fprintf(&b, "Profile: %s\n", valueOr(m.status.ActiveProfile, "-"))
	fmt.Fprintf(&b, "Namespace: %s\n", valueOr(m.status.Kubeconfig.Namespace, "-"))
	fmt.Fprintf(&b, "Tunnels: %d\n", len(m.items))
	if len(m.status.Warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, warning := range m.status.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	return tuiLayout(m.menu.View(), b.String())
}

func (m tuiModel) detailsView() string {
	var b strings.Builder
	b.WriteString(tuiSection("Details", "esc back  r refresh"))
	if m.detailTitle != "" || m.detailText != "" {
		if m.detailTitle != "" {
			fmt.Fprintf(&b, "\n%s\n", m.detailTitle)
		}
		if strings.TrimSpace(m.detailText) == "" {
			b.WriteString("No output.\n")
		} else {
			b.WriteString(m.detailText)
			b.WriteString("\n")
		}
		return tuiLayout(m.menu.View(), tuiCard(b.String()))
	}
	if m.inspect != nil {
		writeInspectSummary(&b, m.inspect)
	}
	if m.doctor != nil {
		writeDoctorSummary(&b, m.doctor)
	}
	if m.metrics != nil {
		writeMetricsSummary(&b, m.metrics)
	}
	if m.events != nil {
		writeEventsSummary(&b, m.events)
	}
	if m.inspect == nil && m.doctor == nil && m.metrics == nil && m.events == nil {
		b.WriteString("\nNo detail payload loaded.")
	}
	return tuiLayout(m.menu.View(), tuiCard(b.String()))
}

func (m tuiModel) confirmView() string {
	title := fmt.Sprintf("Confirm %s", m.confirmAction)
	command := ""
	if m.confirmAction == tuiActionCreate {
		command = m.confirmCommand
	} else if m.confirmAction == tuiActionCommand {
		command = m.confirmCommand
	} else {
		command = commandForTunnelAction(string(m.confirmAction), m.confirmTarget)
	}
	target := valueOr(m.confirmTarget, "global")
	body := fmt.Sprintf("%s\n\nTarget: %s\nCommand: %s\n\nPress enter to confirm, esc to cancel.", tuiSection(title, "operation requires confirmation"), target, command)
	return tuiLayout(m.menu.View(), tuiCard(body))
}

func (m tuiModel) promptView() string {
	body := fmt.Sprintf("%s\n\n%s\n\n%s\n\nPress enter to continue, esc to cancel.", tuiSection(m.promptTitle, m.promptHelp), m.prompt.View(), "Command will be shown before it runs.")
	return tuiLayout(m.menu.View(), tuiCard(body))
}

func tuiHeader(status *statusPayload) string {
	region, namespace, profile := "-", "-", "-"
	daemon := "unknown"
	if status != nil {
		region = valueOr(status.Region, "-")
		namespace = valueOr(status.Kubeconfig.Namespace, "-")
		profile = valueOr(status.ActiveProfile, "-")
		daemon = runningLabel(status.DaemonRunning)
	}
	return tuiHeaderStyle.Render(fmt.Sprintf("Sealtun TUI [Alpha]  region=%s  namespace=%s  profile=%s  daemon=%s", region, namespace, profile, daemon))
}

func tuiFooter(m tuiModel) string {
	parts := []string{"q quit", "tab focus", "enter open", "r refresh", "focus=" + m.focusLabel()}
	if m.loading {
		parts = append(parts, m.spin.View()+" loading")
	}
	if m.message != "" {
		parts = append(parts, m.message)
	}
	if m.err != nil {
		parts = append(parts, "error: "+m.err.Error())
	}
	return tuiFooterStyle.Render(strings.Join(parts, "  |  "))
}

func (m tuiModel) focusLabel() string {
	if m.view == tuiViewConfirm || m.view == tuiViewDetails || m.view == tuiViewPrompt || m.view == tuiViewTunnelActions {
		return "content"
	}
	if m.focus == tuiFocusMenu {
		return "menu"
	}
	return "content"
}

func tuiLayout(left, right string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, tuiSidebarStyle.Render(left), tuiContentStyle.Render(right))
}

func tuiSection(title, help string) string {
	return tuiTitleStyle.Render(title) + "\n" + tuiHelpStyle.Render(help)
}

func tuiCard(value string) string {
	return tuiCardStyle.Render(value)
}
