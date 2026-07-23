package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tunnelprotocol "github.com/labring/sealtun/pkg/protocol"
	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type tuiOptions struct {
	CheckLocal bool
}

const tuiDiscoverTimeout = 8 * time.Second

var tuiOpts tuiOptions

var tuiCmd = &cobra.Command{
	Use:          "tui",
	Aliases:      []string{"console"},
	Short:        "Open an interactive terminal UI for Sealtun",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUI(cmd, tuiOpts)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	markAlpha(tuiCmd)
	tuiCmd.Flags().BoolVar(&tuiOpts.CheckLocal, "check", true, "Probe local targets when listing tunnels")
}

type tuiView int

const (
	tuiViewTunnels tuiView = iota
	tuiViewCreate
	tuiViewOperations
	tuiViewDiagnostics
	tuiViewTunnelActions
	tuiViewDetails
	tuiViewConfirm
	tuiViewPrompt
)

type tuiFocus int

const (
	tuiFocusMenu tuiFocus = iota
	tuiFocusContent
)

type tuiAction string

const (
	tuiActionStart   tuiAction = "start"
	tuiActionStop    tuiAction = "stop"
	tuiActionCleanup tuiAction = "cleanup"
	tuiActionCreate  tuiAction = "create"
	tuiActionCommand tuiAction = "command"
)

type tuiDataSource interface {
	Status() (*statusPayload, error)
	ListTunnels(bool) ([]listItem, error)
	DiscoverPorts(context.Context) ([]discoverItem, error)
	Inspect(context.Context, string) (*inspectPayload, error)
	Doctor(context.Context, string) (*tunnelDoctorPayload, error)
	Metrics(context.Context, string) (*metricsPayload, error)
	Events(context.Context, string) (*eventsPayload, error)
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Cleanup(context.Context, string) error
	Create(context.Context, []string) error
	Command(context.Context, ...string) (string, error)
}

type tuiCLIDataSource struct{}

func (tuiCLIDataSource) Status() (*statusPayload, error) {
	return collectStatus()
}

func (tuiCLIDataSource) ListTunnels(check bool) ([]listItem, error) {
	return collectListItemsWithLocalCheck(check)
}

func (tuiCLIDataSource) DiscoverPorts(ctx context.Context) ([]discoverItem, error) {
	return discoverLocalPorts(ctx, discoverOptions{Limit: 30, Protocol: "auto"}, systemPortDiscoverer{})
}

func (tuiCLIDataSource) Inspect(ctx context.Context, tunnelID string) (*inspectPayload, error) {
	return collectInspectPayloadWithContext(ctx, tunnelID)
}

func (tuiCLIDataSource) Doctor(ctx context.Context, tunnelID string) (*tunnelDoctorPayload, error) {
	return collectTunnelDoctorPayload(ctx, tunnelID)
}

func (tuiCLIDataSource) Metrics(ctx context.Context, tunnelID string) (*metricsPayload, error) {
	return collectMetricsPayloadWithContext(ctx, tunnelID)
}

func (tuiCLIDataSource) Events(ctx context.Context, tunnelID string) (*eventsPayload, error) {
	return collectEventsPayloadWithContext(ctx, tunnelID, 8*time.Second)
}

func (tuiCLIDataSource) Start(ctx context.Context, tunnelID string) error {
	sess, err := findSession(tunnelID)
	if err != nil {
		return err
	}
	return startTunnelSession(ctx, sess)
}

func (tuiCLIDataSource) Stop(ctx context.Context, tunnelID string) error {
	sess, err := findSession(tunnelID)
	if err != nil {
		return err
	}
	_, err = stopTunnelSession(ctx, sess)
	return err
}

func (tuiCLIDataSource) Cleanup(ctx context.Context, tunnelID string) error {
	sess, err := findSession(tunnelID)
	if err != nil {
		return err
	}
	if !sessionCleanupEligible(*sess, time.Minute) {
		return fmt.Errorf("tunnel %s is not stopped, expired, stale, or error", sess.TunnelID)
	}
	if err := cleanupSessionResources(ctx, *sess); err != nil {
		return err
	}
	return session.Delete(sess.TunnelID)
}

func (tuiCLIDataSource) Create(ctx context.Context, args []string) error {
	localPort, nextProtocol, err := parseTUIExposeArgs(args)
	if err != nil {
		return err
	}
	previousProtocol := protocol
	protocol = nextProtocol
	defer func() {
		protocol = previousProtocol
	}()

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return runExpose(cmd, []string{localPort})
}

func (tuiCLIDataSource) Command(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("command requires arguments")
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// #nosec G204 -- TUI invokes the current Sealtun binary directly with
	// structured arguments from fixed operation specs. It does not use a shell.
	child := exec.CommandContext(ctx, exe, args...)
	child.Env = os.Environ()
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	err = child.Run()
	return output.String(), err
}

type tuiLoadedMsg struct {
	status  *statusPayload
	tunnels []listItem
	ports   []discoverItem
	err     error
}

type tuiRefreshMsg tuiLoadedMsg

type tuiInspectMsg struct {
	payload *inspectPayload
	err     error
}

type tuiDoctorMsg struct {
	payload *tunnelDoctorPayload
	err     error
}

type tuiMetricsMsg struct {
	payload *metricsPayload
	err     error
}

type tuiEventsMsg struct {
	payload *eventsPayload
	err     error
}

type tuiTextMsg struct {
	title string
	text  string
	err   error
}

type tuiActionMsg struct {
	action tuiAction
	target string
	text   string
	err    error
}

type tuiListItem struct {
	title string
	desc  string
	value string
}

func (i tuiListItem) Title() string       { return i.title }
func (i tuiListItem) Description() string { return i.desc }
func (i tuiListItem) FilterValue() string { return i.title + " " + i.desc + " " + i.value }

type tuiModel struct {
	source     tuiDataSource
	checkLocal bool

	width      int
	height     int
	view       tuiView
	returnView tuiView
	focus      tuiFocus

	menu    list.Model
	tunnels list.Model
	ports   list.Model
	ops     list.Model
	actions list.Model
	prompt  textinput.Model
	spin    spinner.Model

	status      *statusPayload
	items       []listItem
	disco       []discoverItem
	inspect     *inspectPayload
	doctor      *tunnelDoctorPayload
	metrics     *metricsPayload
	events      *eventsPayload
	detailTitle string
	detailText  string

	loading bool
	message string
	err     error

	confirmAction   tuiAction
	confirmTarget   string
	confirmArgs     []string
	confirmCommand  string
	promptOperation string
	promptTarget    string
	promptTitle     string
	promptHelp      string
}

func runTUI(cmd *cobra.Command, opts tuiOptions) error {
	if !isTerminalIO(cmd.InOrStdin(), cmd.OutOrStdout()) {
		return fmt.Errorf("sealtun tui requires an interactive terminal")
	}
	model := newTUIModel(tuiCLIDataSource{}, opts.CheckLocal)
	program := tea.NewProgram(model, tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func newTUIModel(source tuiDataSource, checkLocal bool) tuiModel {
	menu := list.New([]list.Item{
		tuiListItem{title: "Tunnels", desc: "Select a tunnel, inspect it, then press o for actions"},
		tuiListItem{title: "Create", desc: "Discover local ports and create a basic tunnel"},
		tuiListItem{title: "Tools", desc: "Run global status, templates, YAML, and maintenance tools"},
		tuiListItem{title: "Status", desc: "View login, daemon, region, namespace, and warnings"},
	}, list.NewDefaultDelegate(), 28, 12)
	menu.Title = "Sealtun"
	menu.SetShowStatusBar(false)
	menu.SetFilteringEnabled(false)

	tunnels := list.New(nil, list.NewDefaultDelegate(), 70, 18)
	tunnels.Title = "Tunnels"
	tunnels.SetShowStatusBar(false)

	ports := list.New(nil, list.NewDefaultDelegate(), 70, 18)
	ports.Title = "Discovered Local Ports"
	ports.SetShowStatusBar(false)

	ops := list.New(nil, list.NewDefaultDelegate(), 70, 18)
	ops.Title = "Tools"
	ops.SetShowStatusBar(false)

	actions := list.New(nil, list.NewDefaultDelegate(), 70, 18)
	actions.Title = "Tunnel Actions"
	actions.SetShowStatusBar(false)

	prompt := textinput.New()
	prompt.CharLimit = 256
	prompt.Width = 64

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return tuiModel{
		source:     source,
		checkLocal: checkLocal,
		view:       tuiViewTunnels,
		focus:      tuiFocusMenu,
		menu:       menu,
		tunnels:    tunnels,
		ports:      ports,
		ops:        ops,
		actions:    actions,
		prompt:     prompt,
		spin:       sp,
		loading:    true,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.load())
}

func (m tuiModel) load() tea.Cmd {
	return func() tea.Msg {
		status, statusErr := m.source.Status()
		tunnels, listErr := m.source.ListTunnels(m.checkLocal)
		ctx, cancel := context.WithTimeout(context.Background(), tuiDiscoverTimeout)
		ports, discoverErr := m.source.DiscoverPorts(ctx)
		cancel()
		return tuiLoadedMsg{
			status:  status,
			tunnels: tunnels,
			ports:   ports,
			err:     firstError(statusErr, listErr, discoverErr),
		}
	}
}

func (m tuiModel) refresh() tea.Cmd {
	return func() tea.Msg {
		status, statusErr := m.source.Status()
		tunnels, listErr := m.source.ListTunnels(m.checkLocal)
		ctx, cancel := context.WithTimeout(context.Background(), tuiDiscoverTimeout)
		ports, discoverErr := m.source.DiscoverPorts(ctx)
		cancel()
		return tuiRefreshMsg{
			status:  status,
			tunnels: tunnels,
			ports:   ports,
			err:     firstError(statusErr, listErr, discoverErr),
		}
	}
}

func (m tuiModel) loadInspect(tunnelID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		payload, err := m.source.Inspect(ctx, tunnelID)
		return tuiInspectMsg{payload: payload, err: err}
	}
}

func (m tuiModel) loadDoctor(tunnelID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		payload, err := m.source.Doctor(ctx, tunnelID)
		return tuiDoctorMsg{payload: payload, err: err}
	}
}

func (m tuiModel) loadMetrics(tunnelID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		payload, err := m.source.Metrics(ctx, tunnelID)
		return tuiMetricsMsg{payload: payload, err: err}
	}
}

func (m tuiModel) loadEvents(tunnelID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		payload, err := m.source.Events(ctx, tunnelID)
		return tuiEventsMsg{payload: payload, err: err}
	}
}

func (m tuiModel) loadCommand(title string, args ...string) tea.Cmd {
	copied := append([]string{}, args...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		text, err := m.source.Command(ctx, copied...)
		return tuiTextMsg{title: title, text: strings.TrimRight(text, "\n"), err: err}
	}
}

func (m tuiModel) runAction(action tuiAction, target string) tea.Cmd {
	args := append([]string{}, m.confirmArgs...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var err error
		switch action {
		case tuiActionStart:
			err = m.source.Start(ctx, target)
		case tuiActionStop:
			err = m.source.Stop(ctx, target)
		case tuiActionCleanup:
			err = m.source.Cleanup(ctx, target)
		case tuiActionCreate:
			if len(args) == 0 {
				args = []string{target}
			}
			err = m.source.Create(ctx, args)
		case tuiActionCommand:
			var text string
			text, err = m.source.Command(ctx, args...)
			return tuiActionMsg{action: action, target: target, text: strings.TrimRight(text, "\n"), err: err}
		default:
			err = fmt.Errorf("unknown action %q", action)
		}
		return tuiActionMsg{action: action, target: target, err: err}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeLists()
	case spinner.TickMsg:
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tuiLoadedMsg:
		m.loading = false
		m.status = msg.status
		m.items = msg.tunnels
		m.disco = msg.ports
		m.err = msg.err
		m.tunnels.SetItems(tunnelListItems(msg.tunnels))
		m.ports.SetItems(portListItems(msg.ports))
		m.refreshOperations()
		m.refreshTunnelActions()
	case tuiRefreshMsg:
		m.status = msg.status
		m.items = msg.tunnels
		m.disco = msg.ports
		m.tunnels.SetItems(tunnelListItems(msg.tunnels))
		m.ports.SetItems(portListItems(msg.ports))
		m.refreshOperations()
		m.refreshTunnelActions()
		if msg.err != nil && m.err == nil {
			m.err = msg.err
		}
	case tuiInspectMsg:
		m.loading = false
		m.clearStructuredDetail()
		m.clearTextDetail()
		m.inspect = msg.payload
		m.err = msg.err
		m.view = tuiViewDetails
	case tuiDoctorMsg:
		m.loading = false
		m.clearStructuredDetail()
		m.clearTextDetail()
		m.doctor = msg.payload
		m.err = msg.err
		m.view = tuiViewDetails
	case tuiMetricsMsg:
		m.loading = false
		m.clearStructuredDetail()
		m.clearTextDetail()
		m.metrics = msg.payload
		m.err = msg.err
		m.view = tuiViewDetails
	case tuiEventsMsg:
		m.loading = false
		m.clearStructuredDetail()
		m.clearTextDetail()
		m.events = msg.payload
		m.err = msg.err
		m.view = tuiViewDetails
	case tuiTextMsg:
		m.loading = false
		m.clearStructuredDetail()
		m.detailTitle = msg.title
		m.detailText = msg.text
		m.err = msg.err
		m.view = tuiViewDetails
	case tuiActionMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			if strings.TrimSpace(msg.text) != "" {
				m.clearStructuredDetail()
				m.detailTitle = "Command Output"
				m.detailText = msg.text
				m.view = tuiViewDetails
			}
			m.message = ""
			return m, nil
		}
		if msg.action == tuiActionCommand && strings.TrimSpace(msg.text) != "" {
			m.clearStructuredDetail()
			m.detailTitle = "Command Output"
			m.detailText = msg.text
			m.view = tuiViewDetails
		} else {
			m.view = tuiViewTunnels
		}
		m.message = fmt.Sprintf("%s %s completed", msg.action, msg.target)
		m.err = nil
		return m, m.refresh()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.view == tuiViewPrompt {
		return m.handlePromptKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		m.loading = true
		m.message = "Refreshing..."
		m.err = nil
		return m, m.load()
	case "esc":
		if m.view == tuiViewConfirm || m.view == tuiViewDetails {
			m.view = m.backView()
			m.focus = tuiFocusContent
			m.resetPending()
			return m, nil
		}
		if m.view == tuiViewTunnelActions {
			m.view = tuiViewTunnels
			m.focus = tuiFocusContent
			m.resetPending()
			return m, nil
		}
		if m.view == tuiViewOperations {
			m.focus = tuiFocusMenu
			m.syncMenuSelection()
			m.resetPending()
			return m, nil
		}
		if m.focus == tuiFocusContent {
			m.focus = tuiFocusMenu
			return m, nil
		}
	case "tab":
		if m.view == tuiViewTunnels || m.view == tuiViewCreate || m.view == tuiViewOperations || m.view == tuiViewDiagnostics {
			m.toggleFocus()
			return m, nil
		}
	case "right", "l":
		if m.view == tuiViewTunnels || m.view == tuiViewCreate || m.view == tuiViewOperations || m.view == tuiViewDiagnostics {
			m.focus = tuiFocusContent
			return m, nil
		}
	case "left", "h":
		if m.view == tuiViewTunnels || m.view == tuiViewCreate || m.view == tuiViewOperations || m.view == tuiViewDiagnostics || m.view == tuiViewTunnelActions {
			m.focus = tuiFocusMenu
			m.syncMenuSelection()
			return m, nil
		}
	case "enter":
		if m.view == tuiViewConfirm {
			action, target := m.confirmAction, m.confirmTarget
			m.loading = true
			m.message = fmt.Sprintf("Running %s...", m.confirmCommand)
			return m, m.runAction(action, target)
		}
		if m.focus == tuiFocusMenu {
			m.view = tuiViewFromMenuIndex(m.menu.Index())
			m.focus = tuiFocusContent
			if m.view == tuiViewOperations {
				m.refreshOperations()
			}
			return m, nil
		}
		if m.view == tuiViewTunnels {
			if item, ok := m.selectedTunnelItem(); ok {
				m.loading = true
				m.message = "Loading tunnel details..."
				m.returnView = tuiViewTunnels
				return m, m.loadInspect(item.value)
			}
		}
		if m.view == tuiViewCreate {
			if item, ok := m.selectedPortItem(); ok {
				m.confirmAction = tuiActionCreate
				m.confirmTarget = item.value
				m.confirmArgs = exposeArgsForDiscoveredPortItem(m.disco, item.value)
				m.confirmCommand = commandForExposeArgs(m.confirmArgs)
				m.returnView = tuiViewCreate
				m.view = tuiViewConfirm
				return m, nil
			}
		}
		if m.view == tuiViewOperations {
			if item, ok := m.selectedOperationItem(); ok {
				return m.runSelectedGlobalOperation(item.value)
			}
		}
		if m.view == tuiViewTunnelActions {
			if item, ok := m.selectedTunnelActionItem(); ok {
				return m.runSelectedTunnelOperation(item.value)
			}
		}
	case "o":
		if m.focus == tuiFocusMenu {
			m.view = tuiViewFromMenuIndex(m.menu.Index())
			m.focus = tuiFocusContent
			if m.view == tuiViewOperations {
				m.refreshOperations()
			}
			return m, nil
		}
		if item, ok := m.selectedTunnelItem(); ok {
			m.view = tuiViewTunnelActions
			m.returnView = tuiViewTunnelActions
			m.focus = tuiFocusContent
			m.message = "Actions for " + item.value
			m.refreshTunnelActions()
			return m, nil
		}
		m.err = fmt.Errorf("select a tunnel first")
		return m, nil
	case "d":
		if item, ok := m.selectedTunnelItem(); ok {
			m.loading = true
			m.message = "Running doctor..."
			m.returnView = tuiViewTunnels
			return m, m.loadDoctor(item.value)
		}
	case "m":
		if item, ok := m.selectedTunnelItem(); ok {
			m.loading = true
			m.message = "Loading metrics..."
			m.returnView = tuiViewTunnels
			return m, m.loadMetrics(item.value)
		}
	case "e":
		if item, ok := m.selectedTunnelItem(); ok {
			m.loading = true
			m.message = "Loading events..."
			m.returnView = tuiViewTunnels
			return m, m.loadEvents(item.value)
		}
	case "s":
		if item, ok := m.selectedTunnelItem(); ok {
			m.confirmAction = tuiActionStop
			if tunnelByID(m.items, item.value).Status == "stopped" {
				m.confirmAction = tuiActionStart
			}
			m.confirmTarget = item.value
			m.confirmCommand = commandForTunnelAction(string(m.confirmAction), item.value)
			m.returnView = tuiViewTunnels
			m.view = tuiViewConfirm
			return m, nil
		}
	case "x":
		if item, ok := m.selectedTunnelItem(); ok {
			m.confirmAction = tuiActionCleanup
			m.confirmTarget = item.value
			m.confirmCommand = commandForTunnelAction(string(m.confirmAction), item.value)
			m.returnView = tuiViewTunnels
			m.view = tuiViewConfirm
			return m, nil
		}
	}

	if m.focus == tuiFocusMenu && (m.view == tuiViewTunnels || m.view == tuiViewCreate || m.view == tuiViewOperations || m.view == tuiViewDiagnostics) {
		m.menu, _ = m.menu.Update(msg)
		m.view = tuiViewFromMenuIndex(m.menu.Index())
		if m.view == tuiViewOperations {
			m.refreshOperations()
		}
		return m, nil
	}

	switch m.view {
	case tuiViewTunnels:
		m.tunnels, _ = m.tunnels.Update(msg)
	case tuiViewCreate:
		m.ports, _ = m.ports.Update(msg)
	case tuiViewOperations:
		m.ops, _ = m.ops.Update(msg)
	case tuiViewTunnelActions:
		m.actions, _ = m.actions.Update(msg)
	}
	return m, nil
}

func (m tuiModel) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = m.backView()
		m.resetPending()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.prompt.Value())
		args, err := tuiPromptCommandArgs(m.promptOperation, m.promptTarget, value)
		if err != nil {
			m.err = err
			return m, nil
		}
		m.confirmAction = tuiActionCommand
		m.confirmTarget = m.promptTarget
		m.confirmArgs = args
		m.confirmCommand = commandForArgs(args)
		m.view = tuiViewConfirm
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

func (m *tuiModel) resizeLists() {
	sidebarWidth := 30
	contentWidth := m.width - sidebarWidth - 6
	if contentWidth < 40 {
		contentWidth = 40
	}
	height := m.height - 8
	if height < 10 {
		height = 10
	}
	m.menu.SetSize(sidebarWidth, height)
	m.tunnels.SetSize(contentWidth, height)
	m.ports.SetSize(contentWidth, height)
	m.ops.SetSize(contentWidth, height)
	m.actions.SetSize(contentWidth, height)
	if contentWidth-8 > 20 {
		m.prompt.Width = contentWidth - 8
	}
}

func (m tuiModel) selectedTunnelItem() (tuiListItem, bool) {
	item, ok := m.tunnels.SelectedItem().(tuiListItem)
	return item, ok && strings.TrimSpace(item.value) != ""
}

func (m tuiModel) selectedPortItem() (tuiListItem, bool) {
	item, ok := m.ports.SelectedItem().(tuiListItem)
	return item, ok && strings.TrimSpace(item.value) != ""
}

func (m tuiModel) selectedOperationItem() (tuiListItem, bool) {
	item, ok := m.ops.SelectedItem().(tuiListItem)
	return item, ok && strings.TrimSpace(item.value) != ""
}

func (m tuiModel) selectedTunnelActionItem() (tuiListItem, bool) {
	item, ok := m.actions.SelectedItem().(tuiListItem)
	return item, ok && strings.TrimSpace(item.value) != ""
}

func (m tuiModel) backView() tuiView {
	if m.returnView == tuiViewDetails || m.returnView == tuiViewConfirm || m.returnView == tuiViewPrompt {
		return tuiViewTunnels
	}
	return m.returnView
}

func (m *tuiModel) refreshOperations() {
	m.ops.SetItems(tuiGlobalOperationItems())
}

func (m *tuiModel) refreshTunnelActions() {
	item, ok := m.selectedTunnelItem()
	if !ok {
		m.actions.SetItems(nil)
		return
	}
	tunnel := tunnelByID(m.items, item.value)
	m.actions.SetItems(tuiOperationItems(tunnel))
}

func (m *tuiModel) syncMenuSelection() {
	switch m.view {
	case tuiViewTunnels, tuiViewTunnelActions:
		m.menu.Select(0)
	case tuiViewCreate:
		m.menu.Select(1)
	case tuiViewOperations:
		m.menu.Select(2)
	case tuiViewDiagnostics:
		m.menu.Select(3)
	}
}

func (m *tuiModel) toggleFocus() {
	if m.focus == tuiFocusMenu {
		m.focus = tuiFocusContent
		return
	}
	m.focus = tuiFocusMenu
	m.syncMenuSelection()
}

func (m *tuiModel) clearStructuredDetail() {
	m.inspect = nil
	m.doctor = nil
	m.metrics = nil
	m.events = nil
}

func (m *tuiModel) clearTextDetail() {
	m.detailTitle = ""
	m.detailText = ""
}

func (m *tuiModel) resetPending() {
	m.confirmAction = ""
	m.confirmTarget = ""
	m.confirmArgs = nil
	m.confirmCommand = ""
	m.promptOperation = ""
	m.promptTarget = ""
	m.promptTitle = ""
	m.promptHelp = ""
	m.prompt.SetValue("")
	m.prompt.Blur()
}

func (m tuiModel) runSelectedGlobalOperation(operation string) (tea.Model, tea.Cmd) {
	if !strings.HasPrefix(operation, "global-") {
		m.err = fmt.Errorf("unknown global operation %q", operation)
		return m, nil
	}
	if _, ok := tuiPromptSpec(operation); ok {
		return m.startPrompt(operation, "")
	}
	return m.runGlobalOperation(operation)
}

func (m tuiModel) runSelectedTunnelOperation(operation string) (tea.Model, tea.Cmd) {
	item, ok := m.selectedTunnelItem()
	if !ok {
		m.err = fmt.Errorf("select a tunnel first")
		return m, nil
	}
	tunnelID := item.value
	switch operation {
	case "inspect":
		m.loading = true
		m.message = "Loading inspect..."
		return m, m.loadInspect(tunnelID)
	case "doctor":
		m.loading = true
		m.message = "Running doctor..."
		return m, m.loadDoctor(tunnelID)
	case "metrics":
		m.loading = true
		m.message = "Loading metrics..."
		return m, m.loadMetrics(tunnelID)
	case "events":
		m.loading = true
		m.message = "Loading events..."
		return m, m.loadEvents(tunnelID)
	case "logs":
		m.loading = true
		m.message = "Loading logs..."
		return m, m.loadCommand("Logs", "logs", tunnelID, "--tail", "120")
	case "resources":
		m.loading = true
		m.message = "Loading resources..."
		return m, m.loadCommand("Resources", "resources", tunnelID)
	case "export":
		m.loading = true
		m.message = "Exporting YAML..."
		return m, m.loadCommand("Export YAML", "export", tunnelID)
	case "domain-status":
		m.loading = true
		m.message = "Loading domain status..."
		return m, m.loadCommand("Domain Status", "domain", "status", tunnelID)
	case "domain-verify":
		m.loading = true
		m.message = "Verifying domain..."
		return m, m.loadCommand("Domain Verify", "domain", "verify", tunnelID)
	case "domain-doctor":
		m.loading = true
		m.message = "Running domain doctor..."
		return m, m.loadCommand("Domain Doctor", "domain", "doctor", tunnelID)
	case "policy-show":
		m.loading = true
		m.message = "Loading policy..."
		return m, m.loadCommand("Policy", "policy", "show", tunnelID)
	case "policy-audit":
		m.loading = true
		m.message = "Loading audit..."
		return m, m.loadCommand("Policy Audit", "policy", "audit", tunnelID, "--since", "10m", "--limit", "100")
	case "share-list":
		m.loading = true
		m.message = "Loading share links..."
		return m, m.loadCommand("Share Links", "share", "list", tunnelID)
	case "start", "stop", "cleanup":
		return m.confirmSimpleTunnelOperation(operation, tunnelID)
	case "doctor-fix":
		return m.confirmCommandOperation(tunnelID, []string{"doctor", tunnelID, "--fix"}, "Run conservative repair actions")
	case "policy-clear-rate":
		return m.confirmCommandOperation(tunnelID, []string{"policy", "set", tunnelID, "--clear-rate-limit"}, "Clear HTTPS rate limit")
	case "policy-audit-enable":
		return m.confirmCommandOperation(tunnelID, []string{"policy", "set", tunnelID, "--audit"}, "Enable HTTPS access audit")
	case "policy-audit-disable":
		return m.confirmCommandOperation(tunnelID, []string{"policy", "set", tunnelID, "--no-audit"}, "Disable HTTPS access audit")
	case "resources-unset":
		return m.confirmCommandOperation(tunnelID, []string{"resources", "unset", tunnelID}, "Reset pod resources to Sealtun defaults")
	case "domain-clear":
		return m.confirmCommandOperation(tunnelID, []string{"domain", "clear", tunnelID}, "Remove the custom domain from this tunnel")
	case "rotate-server-secret":
		return m.confirmCommandOperation(tunnelID, []string{"rotate", tunnelID, "--server-secret"}, "Rotate the server secret; the new secret is shown once")
	case "domain-plan", "domain-add", "share-create", "share-revoke", "share-rotate", "policy-rate", "resources-set":
		return m.startPrompt(operation, tunnelID)
	default:
		m.err = fmt.Errorf("unknown operation %q", operation)
		return m, nil
	}
}

func (m tuiModel) runGlobalOperation(operation string) (tea.Model, tea.Cmd) {
	title, args, err := tuiGlobalCommand(operation)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.loading = true
	m.message = "Running " + commandForArgs(args)
	m.returnView = tuiViewOperations
	return m, m.loadCommand(title, args...)
}

func (m tuiModel) confirmSimpleTunnelOperation(operation, tunnelID string) (tea.Model, tea.Cmd) {
	action := tuiAction(operation)
	m.confirmAction = action
	m.confirmTarget = tunnelID
	m.confirmArgs = nil
	m.confirmCommand = commandForTunnelAction(operation, tunnelID)
	m.returnView = tuiViewTunnelActions
	m.view = tuiViewConfirm
	return m, nil
}

func (m tuiModel) confirmCommandOperation(tunnelID string, args []string, help string) (tea.Model, tea.Cmd) {
	m.confirmAction = tuiActionCommand
	m.confirmTarget = tunnelID
	m.confirmArgs = append([]string{}, args...)
	m.confirmCommand = commandForArgs(args)
	m.message = help
	m.returnView = tuiViewTunnelActions
	m.view = tuiViewConfirm
	return m, nil
}

func (m tuiModel) startPrompt(operation, tunnelID string) (tea.Model, tea.Cmd) {
	spec, ok := tuiPromptSpec(operation)
	if !ok {
		m.err = fmt.Errorf("operation %q does not accept input", operation)
		return m, nil
	}
	m.promptOperation = operation
	m.promptTarget = tunnelID
	m.promptTitle = spec.title
	m.promptHelp = spec.help
	m.prompt.SetValue(spec.value)
	m.prompt.Placeholder = spec.placeholder
	m.prompt.Focus()
	m.err = nil
	if tunnelID == "" {
		m.returnView = tuiViewOperations
	} else {
		m.returnView = tuiViewTunnelActions
	}
	m.view = tuiViewPrompt
	return m, textinput.Blink
}

func tuiGlobalOperationItems() []list.Item {
	return []list.Item{
		tuiListItem{title: "Status", desc: "Show login, daemon, region, and kubeconfig state", value: "global-status"},
		tuiListItem{title: "Discover Ports", desc: "Scan local listening ports", value: "global-discover"},
		tuiListItem{title: "Init Plan", desc: "Print first-use recommendation without creating resources", value: "global-init"},
		tuiListItem{title: "Template HTTPS", desc: "Show HTTPS expose command and YAML template", value: "global-template-https"},
		tuiListItem{title: "Template SSH", desc: "Show SSH TCP expose command and YAML template", value: "global-template-ssh"},
		tuiListItem{title: "Template TCP", desc: "Show generic TCP expose command and YAML template", value: "global-template-tcp"},
		tuiListItem{title: "Connect Check", desc: "Check cluster transparent-connect prerequisites", value: "global-connect-check"},
		tuiListItem{title: "Domain Status", desc: "Scan all custom domains", value: "global-domain-status"},
		tuiListItem{title: "Doctor Fix Dry Run", desc: "Plan conservative repairs without executing them", value: "global-doctor-fix-dry-run"},
		tuiListItem{title: "Export All", desc: "Export all local sessions as sealtun.yaml", value: "global-export-all"},
		tuiListItem{title: "Apply Dry Run", desc: "Validate a sealtun.yaml without changing resources", value: "global-apply-dry-run"},
		tuiListItem{title: "Diff YAML", desc: "Show declarative changes for a sealtun.yaml", value: "global-diff"},
		tuiListItem{title: "Apply YAML", desc: "Apply a sealtun.yaml after confirmation", value: "global-apply"},
	}
}

func tuiGlobalCommand(operation string) (string, []string, error) {
	switch operation {
	case "global-status":
		return "Status", []string{"status"}, nil
	case "global-discover":
		return "Discover Ports", []string{"discover", "--limit", "30"}, nil
	case "global-init":
		return "Init Plan", []string{"init"}, nil
	case "global-template-https":
		return "Template HTTPS", []string{"template", "https"}, nil
	case "global-template-ssh":
		return "Template SSH", []string{"template", "ssh"}, nil
	case "global-template-tcp":
		return "Template TCP", []string{"template", "tcp"}, nil
	case "global-connect-check":
		return "Connect Check", []string{"connect", "--check"}, nil
	case "global-domain-status":
		return "Domain Status", []string{"domain", "status"}, nil
	case "global-doctor-fix-dry-run":
		return "Doctor Fix Dry Run", []string{"doctor", "--fix", "--dry-run"}, nil
	case "global-export-all":
		return "Export All", []string{"export", "--all"}, nil
	default:
		return "", nil, fmt.Errorf("unknown global operation %q", operation)
	}
}

func tuiOperationItems(tunnel listItem) []list.Item {
	statusAction := "Stop tunnel"
	statusOp := "stop"
	if tunnel.Status == "stopped" {
		statusAction = "Start tunnel"
		statusOp = "start"
	}
	items := []list.Item{
		tuiListItem{title: "Inspect", desc: "Show local, remote, endpoint, policy, and warning summary", value: "inspect"},
		tuiListItem{title: "Doctor", desc: "Run tunnel diagnostics and suggestions", value: "doctor"},
		tuiListItem{title: "Logs", desc: "Show recent remote tunnel pod logs", value: "logs"},
		tuiListItem{title: "Metrics", desc: "Show local and remote runtime metrics", value: "metrics"},
		tuiListItem{title: "Events", desc: "Show recent Kubernetes events", value: "events"},
		tuiListItem{title: "Resources", desc: "Show Deployment, Pod, Service, Ingress, and resource hints", value: "resources"},
		tuiListItem{title: "Export YAML", desc: "Export this tunnel as sealtun.yaml", value: "export"},
		tuiListItem{title: statusAction, desc: "Confirmed lifecycle operation", value: statusOp},
		tuiListItem{title: "Cleanup", desc: "Delete eligible stopped, expired, stale, or error tunnel resources", value: "cleanup"},
		tuiListItem{title: "Doctor Fix", desc: "Run conservative repair actions after confirmation", value: "doctor-fix"},
		tuiListItem{title: "Set Resources", desc: "Update pod CPU and memory requests/limits", value: "resources-set"},
		tuiListItem{title: "Reset Resources", desc: "Reset pod resources to Sealtun defaults", value: "resources-unset"},
		tuiListItem{title: "Rotate Server Secret", desc: "Rotate tunnel server secret; output is shown once", value: "rotate-server-secret"},
	}
	if tunnelprotocol.IsHTTP(tunnel.Protocol) || strings.TrimSpace(tunnel.Protocol) == "" {
		items = append(items,
			tuiListItem{title: "Domain Status", desc: "Show custom domain readiness", value: "domain-status"},
			tuiListItem{title: "Domain Plan", desc: "Show CNAME target for a custom domain", value: "domain-plan"},
			tuiListItem{title: "Domain Add", desc: "Attach a verified custom domain", value: "domain-add"},
			tuiListItem{title: "Domain Verify", desc: "Verify DNS, Ingress, and certificate readiness", value: "domain-verify"},
			tuiListItem{title: "Domain Doctor", desc: "Run custom domain diagnostics", value: "domain-doctor"},
			tuiListItem{title: "Domain Clear", desc: "Remove custom domain from this tunnel", value: "domain-clear"},
			tuiListItem{title: "Policy Show", desc: "Show HTTPS access policy without secrets", value: "policy-show"},
			tuiListItem{title: "Set Rate Limit", desc: "Set HTTPS public traffic rate limit, e.g. 60/m", value: "policy-rate"},
			tuiListItem{title: "Clear Rate Limit", desc: "Remove HTTPS public traffic rate limit", value: "policy-clear-rate"},
			tuiListItem{title: "Enable Audit", desc: "Record HTTPS allow/deny audit metadata", value: "policy-audit-enable"},
			tuiListItem{title: "Disable Audit", desc: "Stop recording HTTPS access audit metadata", value: "policy-audit-disable"},
			tuiListItem{title: "Policy Audit", desc: "Show recent allow/deny audit events", value: "policy-audit"},
			tuiListItem{title: "Share List", desc: "List temporary access links without tokens", value: "share-list"},
			tuiListItem{title: "Share Create", desc: "Create a one-time temporary access link", value: "share-create"},
			tuiListItem{title: "Share Revoke", desc: "Revoke a temporary access link by name", value: "share-revoke"},
			tuiListItem{title: "Share Rotate", desc: "Rotate a temporary access link token", value: "share-rotate"},
		)
	}
	return items
}

type tuiPromptDefinition struct {
	title       string
	help        string
	placeholder string
	value       string
}

func tuiPromptSpec(operation string) (tuiPromptDefinition, bool) {
	switch operation {
	case "domain-plan":
		return tuiPromptDefinition{title: "Domain Plan", help: "Enter the custom domain to plan.", placeholder: "app.example.com"}, true
	case "domain-add":
		return tuiPromptDefinition{title: "Domain Add", help: "Enter a verified custom domain to attach.", placeholder: "app.example.com"}, true
	case "share-create":
		return tuiPromptDefinition{title: "Share Create", help: "Enter share name and TTL separated by space.", placeholder: "review 1h", value: "review 1h"}, true
	case "share-revoke":
		return tuiPromptDefinition{title: "Share Revoke", help: "Enter share name to revoke.", placeholder: "review"}, true
	case "share-rotate":
		return tuiPromptDefinition{title: "Share Rotate", help: "Enter share name and optional TTL separated by space.", placeholder: "review 1h", value: "review 1h"}, true
	case "policy-rate":
		return tuiPromptDefinition{title: "Set Rate Limit", help: "Enter rate limit, or leave empty to clear it.", placeholder: "60/m", value: "60/m"}, true
	case "resources-set":
		return tuiPromptDefinition{title: "Set Resources", help: "Enter requestCPU requestMemory limitCPU limitMemory.", placeholder: "10m 32Mi 200m 128Mi", value: "10m 32Mi 200m 128Mi"}, true
	case "global-apply-dry-run":
		return tuiPromptDefinition{title: "Apply Dry Run", help: "Enter path to sealtun.yaml.", placeholder: "sealtun.yaml", value: "sealtun.yaml"}, true
	case "global-diff":
		return tuiPromptDefinition{title: "Diff YAML", help: "Enter path to sealtun.yaml.", placeholder: "sealtun.yaml", value: "sealtun.yaml"}, true
	case "global-apply":
		return tuiPromptDefinition{title: "Apply YAML", help: "Enter path to sealtun.yaml. This will mutate tunnel resources after confirmation.", placeholder: "sealtun.yaml", value: "sealtun.yaml"}, true
	default:
		return tuiPromptDefinition{}, false
	}
}

func tuiPromptCommandArgs(operation, tunnelID, input string) ([]string, error) {
	switch operation {
	case "domain-plan":
		if input == "" {
			return nil, fmt.Errorf("domain is required")
		}
		return []string{"domain", "plan", tunnelID, input}, nil
	case "domain-add":
		if input == "" {
			return nil, fmt.Errorf("domain is required")
		}
		return []string{"domain", "add", tunnelID, input}, nil
	case "share-create":
		name, ttl, err := parseTUINameTTL(input, "review", "1h")
		if err != nil {
			return nil, err
		}
		return []string{"share", "create", tunnelID, "--name", name, "--ttl", ttl}, nil
	case "share-revoke":
		if input == "" {
			return nil, fmt.Errorf("share name is required")
		}
		return []string{"share", "revoke", tunnelID, input}, nil
	case "share-rotate":
		name, ttl, err := parseTUINameTTL(input, "", "1h")
		if err != nil {
			return nil, err
		}
		return []string{"share", "rotate", tunnelID, name, "--ttl", ttl}, nil
	case "policy-rate":
		if input == "" {
			return []string{"policy", "set", tunnelID, "--clear-rate-limit"}, nil
		}
		return []string{"policy", "set", tunnelID, "--rate-limit", input}, nil
	case "resources-set":
		fields := strings.Fields(input)
		if len(fields) != 4 {
			return nil, fmt.Errorf("resources require 4 values: requestCPU requestMemory limitCPU limitMemory")
		}
		return []string{
			"resources", "set", tunnelID,
			"--request-cpu", fields[0],
			"--request-memory", fields[1],
			"--limit-cpu", fields[2],
			"--limit-memory", fields[3],
		}, nil
	case "global-apply-dry-run":
		path := valueOr(input, "sealtun.yaml")
		return []string{"apply", "-f", path, "--dry-run"}, nil
	case "global-diff":
		path := valueOr(input, "sealtun.yaml")
		return []string{"diff", "-f", path}, nil
	case "global-apply":
		path := valueOr(input, "sealtun.yaml")
		return []string{"apply", "-f", path}, nil
	default:
		return nil, fmt.Errorf("unknown prompt operation %q", operation)
	}
}

func parseTUINameTTL(input, defaultName, defaultTTL string) (string, string, error) {
	fields := strings.Fields(input)
	name := defaultName
	ttl := defaultTTL
	if len(fields) > 0 {
		name = fields[0]
	}
	if len(fields) > 1 {
		ttl = fields[1]
	}
	if len(fields) > 2 {
		return "", "", fmt.Errorf("expected name and optional ttl")
	}
	if name == "" {
		return "", "", fmt.Errorf("name is required")
	}
	if ttl == "" {
		return "", "", fmt.Errorf("ttl is required")
	}
	if _, err := time.ParseDuration(ttl); err != nil {
		return "", "", fmt.Errorf("ttl: %w", err)
	}
	return name, ttl, nil
}

var (
	tuiHeaderStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Padding(0, 1)
	tuiFooterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Padding(0, 1)
	tuiSidebarStyle = lipgloss.NewStyle().Width(30).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8")).Padding(1, 1)
	tuiContentStyle = lipgloss.NewStyle().Padding(1, 2)
	tuiTitleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	tuiHelpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tuiCardStyle    = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("12")).Padding(1, 2)
)

func tuiViewFromMenuIndex(index int) tuiView {
	switch index {
	case 1:
		return tuiViewCreate
	case 2:
		return tuiViewOperations
	case 3:
		return tuiViewDiagnostics
	default:
		return tuiViewTunnels
	}
}

func tunnelListItems(items []listItem) []list.Item {
	out := make([]list.Item, 0, len(items))
	for _, item := range items {
		title := fmt.Sprintf("%s  %s  %s", item.TunnelID, item.Status, strings.ToUpper(valueOr(item.Protocol, "-")))
		desc := fmt.Sprintf("%s -> %s  ns=%s", valueOr(item.Endpoint, "-"), valueOr(item.TargetURL, "-"), valueOr(item.Namespace, "-"))
		out = append(out, tuiListItem{title: title, desc: desc, value: item.TunnelID})
	}
	return out
}

func portListItems(items []discoverItem) []list.Item {
	out := make([]list.Item, 0, len(items))
	for _, item := range items {
		title := fmt.Sprintf("%d  %s/%s", item.Port, item.ProtocolHint, item.TemplateHint)
		desc := fmt.Sprintf("%s  pid=%s  process=%s", valueOr(item.Address, "-"), intOrDash(item.PID), valueOr(item.ProcessName, "-"))
		out = append(out, tuiListItem{title: title, desc: desc, value: strconv.Itoa(item.Port)})
	}
	return out
}

func intOrDash(value int) string {
	if value == 0 {
		return "-"
	}
	return strconv.Itoa(value)
}

func tunnelByID(items []listItem, tunnelID string) listItem {
	for _, item := range items {
		if item.TunnelID == tunnelID {
			return item
		}
	}
	return listItem{}
}

func writeInspectSummary(w io.Writer, payload *inspectPayload) {
	fmt.Fprintf(w, "\nTunnel: %s\n", payload.TunnelID)
	fmt.Fprintf(w, "Status: %s\n", payload.Status)
	fmt.Fprintf(w, "Protocol: %s\n", valueOr(payload.Protocol, "-"))
	fmt.Fprintf(w, "Endpoint: %s\n", endpointLabel(payload.Protocol, payload.Host, payload.SealosHost, payload.PublicPort))
	fmt.Fprintf(w, "Target: %s\n", valueOr(payload.TargetURL, "-"))
	fmt.Fprintf(w, "Mode: %s\n", valueOr(payload.Mode, "-"))
	fmt.Fprintf(w, "Namespace: %s\n", valueOr(payload.Namespace, "-"))
	fmt.Fprintf(w, "Process alive: %s\n", yesNo(payload.ProcessAlive))
	fmt.Fprintf(w, "Target reachable: %s\n", yesNo(payload.LocalPortReachable))
	if payload.BasicAuth != nil && payload.BasicAuth.Enabled {
		fmt.Fprintf(w, "Basic Auth: enabled user=%s\n", valueOr(payload.BasicAuth.Username, "-"))
	}
	if payload.AccessPolicy != nil {
		fmt.Fprintf(w, "Access policy: enabled")
		if payload.AccessPolicy.RateLimit != "" {
			fmt.Fprintf(w, " rate=%s", payload.AccessPolicy.RateLimit)
		}
		fmt.Fprintln(w)
	}
	writeWarnings(w, payload.Warnings)
}

func writeDoctorSummary(w io.Writer, payload *tunnelDoctorPayload) {
	fmt.Fprintf(w, "\nDoctor: %s\n", payload.TunnelID)
	fmt.Fprintf(w, "Status: %s\n", payload.Status)
	for _, check := range payload.Checks {
		fmt.Fprintf(w, "- %s: %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Fprintf(w, " (%s)", check.Detail)
		}
		fmt.Fprintln(w)
	}
	if len(payload.Suggestions) > 0 {
		fmt.Fprintln(w, "\nSuggestions")
		for _, suggestion := range payload.Suggestions {
			fmt.Fprintf(w, "- %s\n", suggestion)
		}
	}
	writeWarnings(w, payload.Warnings)
}

func writeMetricsSummary(w io.Writer, payload *metricsPayload) {
	fmt.Fprintf(w, "\nMetrics: %s\n", payload.TunnelID)
	fmt.Fprintf(w, "Status: %s\n", payload.Status)
	fmt.Fprintf(w, "Process alive: %s\n", yesNo(payload.ProcessAlive))
	fmt.Fprintf(w, "Target reachable: %s\n", yesNo(payload.LocalReachable))
	if payload.Remote != nil {
		fmt.Fprintf(w, "Deployment ready: %s\n", payload.Remote.DeploymentReady)
		fmt.Fprintf(w, "Pods: %d total, %d ready, %d restarts\n", payload.Remote.PodCount, payload.Remote.ReadyPods, payload.Remote.RestartCount)
	}
	if len(payload.Server) > 0 {
		if value, ok := payload.Server["totalRequests"]; ok {
			fmt.Fprintf(w, "Total requests: %v\n", value)
		}
		if value, ok := payload.Server["totalTCPConnections"]; ok {
			fmt.Fprintf(w, "Total TCP connections: %v\n", value)
		}
	}
	writeWarnings(w, payload.Warnings)
}

func writeEventsSummary(w io.Writer, payload *eventsPayload) {
	fmt.Fprintf(w, "\nEvents: %s\n", payload.TunnelID)
	if len(payload.Events) == 0 {
		fmt.Fprintln(w, "No recent remote events found.")
	}
	for _, event := range payload.Events {
		fmt.Fprintf(w, "- %s %s/%s %s\n", valueOr(event.LastTimestamp, event.FirstTimestamp), valueOr(event.Type, "Normal"), valueOr(event.Reason, "Event"), event.Message)
	}
	writeWarnings(w, payload.Warnings)
}

func writeWarnings(w io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(w, "\nWarnings")
	for _, warning := range warnings {
		fmt.Fprintf(w, "- %s\n", warning)
	}
}

func encodeTUIViewForTest(model tuiModel) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	_ = enc.Encode(map[string]interface{}{
		"view":    model.view,
		"message": model.message,
		"error":   errorString(model.err),
		"items":   len(model.items),
		"ports":   len(model.disco),
	})
	return strings.TrimSpace(buf.String())
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

type fdValue interface {
	Fd() uintptr
}

func isTerminalIO(r io.Reader, w io.Writer) bool {
	rf, ok := r.(fdValue)
	if !ok {
		return false
	}
	wf, ok := w.(fdValue)
	if !ok {
		return false
	}
	return term.IsTerminal(int(rf.Fd())) && term.IsTerminal(int(wf.Fd()))
}

func exposeArgsForDiscoveredPortItem(items []discoverItem, port string) []string {
	args := []string{port}
	for _, item := range items {
		if strconv.Itoa(item.Port) != port {
			continue
		}
		item = applyPortHints(item)
		if item.ProtocolHint != "" && item.ProtocolHint != tunnelprotocol.HTTPS {
			args = append(args, "--protocol", item.ProtocolHint)
		}
		return args
	}
	return args
}

func commandForExposeArgs(args []string) string {
	parts := append([]string{"sealtun", "expose"}, args...)
	for i := range parts {
		parts[i] = shellQuoteArg(parts[i])
	}
	return strings.Join(parts, " ")
}

func commandForArgs(args []string) string {
	parts := append([]string{"sealtun"}, args...)
	for i := range parts {
		parts[i] = shellQuoteArg(parts[i])
	}
	return strings.Join(parts, " ")
}

func parseTUIExposeArgs(args []string) (string, string, error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("create requires a local port")
	}
	localPort := strings.TrimSpace(args[0])
	if err := validateLocalPort(localPort); err != nil {
		return "", "", err
	}
	nextProtocol := tunnelprotocol.HTTPS
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--protocol":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("--protocol requires a value")
			}
			nextProtocol = tunnelprotocol.Normalize(args[i+1])
			i++
		default:
			return "", "", fmt.Errorf("unsupported TUI create argument %q", args[i])
		}
	}
	if err := validateProtocol(nextProtocol); err != nil {
		return "", "", err
	}
	return localPort, nextProtocol, nil
}
