package cmd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUILoadsStatusTunnelsAndPorts(t *testing.T) {
	source := &fakeTUISource{
		status: &statusPayload{
			LoggedIn:      true,
			Region:        "https://gzg.sealos.run",
			ActiveProfile: "gzg",
			Kubeconfig:    kubeconfigStatus{Namespace: "ns-test", Present: true},
		},
		tunnels: []listItem{{
			TunnelID:  "tun123",
			Status:    "active",
			Protocol:  "https",
			Endpoint:  "https://tun.example.com",
			TargetURL: "localhost:3000",
			Namespace: "ns-test",
		}},
		ports: []discoverItem{{Port: 3000, Address: "127.0.0.1", ProtocolHint: "https", TemplateHint: "https"}},
	}
	model := newTUIModel(source, true)

	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	updated := next.(tuiModel)

	if updated.status == nil || updated.status.Region != "https://gzg.sealos.run" {
		t.Fatalf("status was not loaded: %#v", updated.status)
	}
	if len(updated.items) != 1 || len(updated.disco) != 1 {
		t.Fatalf("expected loaded tunnels and ports, got %s", encodeTUIViewForTest(updated))
	}
	if !source.listCheck {
		t.Fatalf("expected local check option to be passed to source")
	}
	if !source.discoverHasDeadline {
		t.Fatal("expected TUI port discovery to have a deadline")
	}
}

func TestTUICreateFromDiscoveredTCPPortUsesProtocol(t *testing.T) {
	source := &fakeTUISource{
		status:  &statusPayload{},
		tunnels: nil,
		ports:   []discoverItem{{Port: 6379, Address: "127.0.0.1"}},
	}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)
	model.view = tuiViewCreate
	model.focus = tuiFocusContent

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(tuiModel)
	if model.view != tuiViewConfirm || model.confirmAction != tuiActionCreate {
		t.Fatalf("expected create confirmation, got view=%v action=%s", model.view, model.confirmAction)
	}
	if want := []string{"6379", "--protocol", "tcp"}; !reflect.DeepEqual(model.confirmArgs, want) {
		t.Fatalf("unexpected create args: got %v want %v", model.confirmArgs, want)
	}

	msg := runTeaCmd(t, model.runAction(model.confirmAction, model.confirmTarget)).(tuiActionMsg)
	if msg.err != nil {
		t.Fatalf("create action returned error: %v", msg.err)
	}
	if want := []string{"6379", "--protocol", "tcp"}; !reflect.DeepEqual(source.createdArgs, want) {
		t.Fatalf("source saw create args %v want %v", source.createdArgs, want)
	}
}

func TestTUIStopStartActionDependsOnSelectedStatus(t *testing.T) {
	source := &fakeTUISource{
		status: &statusPayload{},
		tunnels: []listItem{{
			TunnelID: "stopped1",
			Status:   "stopped",
			Protocol: "https",
		}},
	}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)
	model.view = tuiViewTunnels

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	model = next.(tuiModel)
	if model.confirmAction != tuiActionStart || model.confirmTarget != "stopped1" {
		t.Fatalf("expected start confirmation, got action=%s target=%s", model.confirmAction, model.confirmTarget)
	}
}

func TestTUIOperationsIncludeHTTPSManagement(t *testing.T) {
	items := tuiOperationItems(listItem{TunnelID: "tun123", Status: "active", Protocol: "https"})
	values := map[string]bool{}
	for _, item := range items {
		got := item.(tuiListItem)
		values[got.value] = true
	}
	for _, want := range []string{
		"logs",
		"resources",
		"export",
		"doctor-fix",
		"domain-plan",
		"domain-add",
		"policy-show",
		"policy-rate",
		"policy-clear-rate",
		"policy-audit-enable",
		"policy-audit-disable",
		"share-create",
		"rotate-server-secret",
	} {
		if !values[want] {
			t.Fatalf("expected operation %q in HTTPS operations", want)
		}
	}
	for _, deprecated := range []string{"repair", "domain-set"} {
		if values[deprecated] {
			t.Fatalf("deprecated operation %q should not be shown in the TUI", deprecated)
		}
	}
}

func TestTUIOperationsFilterHTTPOnlyActionsForTCP(t *testing.T) {
	items := tuiOperationItems(listItem{TunnelID: "tun123", Status: "active", Protocol: "tcp"})
	for _, item := range items {
		got := item.(tuiListItem)
		switch got.value {
		case "domain-plan", "policy-rate", "share-create":
			t.Fatalf("operation %q should not be shown for TCP tunnels", got.value)
		}
	}
}

func TestTUIGlobalOperationsAvailableWithoutTunnel(t *testing.T) {
	source := &fakeTUISource{status: &statusPayload{}}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)
	model.view = tuiViewOperations
	model.focus = tuiFocusContent

	if got := len(model.ops.Items()); got == 0 {
		t.Fatal("expected global operations without a selected tunnel")
	}
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(tuiModel)
	if cmd == nil {
		t.Fatal("expected selected global operation to run a command")
	}
	msg := runTeaCmd(t, cmd).(tuiTextMsg)
	if msg.err != nil {
		t.Fatalf("global operation returned error: %v", msg.err)
	}
	if !reflect.DeepEqual(source.commandArgs, []string{"status"}) {
		t.Fatalf("global status args = %v", source.commandArgs)
	}
	if msg.title != "Status" {
		t.Fatalf("global status title = %q", msg.title)
	}
}

func TestTUIMenuFocusMovesBetweenSections(t *testing.T) {
	source := &fakeTUISource{status: &statusPayload{}}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)

	if model.focus != tuiFocusMenu {
		t.Fatalf("expected initial menu focus, got %v", model.focus)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(tuiModel)
	if model.view != tuiViewCreate || model.menu.Index() != 1 {
		t.Fatalf("down should select Create in menu, got view=%v index=%d", model.view, model.menu.Index())
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(tuiModel)
	if model.view != tuiViewOperations || model.menu.Index() != 2 {
		t.Fatalf("down should select Tools in menu, got view=%v index=%d", model.view, model.menu.Index())
	}
}

func TestTUIEnterMovesFromMenuToContent(t *testing.T) {
	source := &fakeTUISource{status: &statusPayload{}}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(tuiModel)
	if model.focus != tuiFocusContent || model.view != tuiViewTunnels {
		t.Fatalf("enter should move focus to content, got focus=%v view=%v", model.focus, model.view)
	}
}

func TestTUIToolsStayGlobalWithSelectedTunnel(t *testing.T) {
	source := &fakeTUISource{
		status: &statusPayload{},
		tunnels: []listItem{{
			TunnelID: "tun123",
			Status:   "active",
			Protocol: "https",
		}},
	}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)
	model.view = tuiViewOperations
	model.focus = tuiFocusContent

	first, ok := model.ops.Items()[0].(tuiListItem)
	if !ok || first.value != "global-status" {
		t.Fatalf("tools should stay global, got %#v", model.ops.Items()[0])
	}
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(tuiModel)
	if cmd == nil {
		t.Fatal("expected global operation command")
	}
	msg := runTeaCmd(t, cmd).(tuiTextMsg)
	if msg.err != nil {
		t.Fatalf("global command returned error: %v", msg.err)
	}
	if !reflect.DeepEqual(source.commandArgs, []string{"status"}) {
		t.Fatalf("tools ran args %v, want status", source.commandArgs)
	}
}

func TestTUIOpenTunnelActionsFromSelectedTunnel(t *testing.T) {
	source := &fakeTUISource{
		status: &statusPayload{},
		tunnels: []listItem{{
			TunnelID: "tun123",
			Status:   "active",
			Protocol: "https",
		}},
	}
	model := newTUIModel(source, true)
	loaded := runTeaCmd(t, model.load()).(tuiLoadedMsg)
	next, _ := model.Update(loaded)
	model = next.(tuiModel)
	model.view = tuiViewTunnels
	model.focus = tuiFocusContent

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	model = next.(tuiModel)
	if model.view != tuiViewTunnelActions {
		t.Fatalf("expected tunnel actions view, got %v", model.view)
	}
	if got := len(model.actions.Items()); got == 0 {
		t.Fatal("expected tunnel action items")
	}
	first, ok := model.actions.Items()[0].(tuiListItem)
	if !ok || first.value != "inspect" {
		t.Fatalf("expected inspect as first tunnel action, got %#v", model.actions.Items()[0])
	}
}

func TestTUIGlobalCommandMapping(t *testing.T) {
	tests := []struct {
		operation string
		title     string
		args      []string
	}{
		{operation: "global-status", title: "Status", args: []string{"status"}},
		{operation: "global-connect-check", title: "Connect Check", args: []string{"connect", "--check"}},
		{operation: "global-doctor-fix-dry-run", title: "Doctor Fix Dry Run", args: []string{"doctor", "--fix", "--dry-run"}},
		{operation: "global-export-all", title: "Export All", args: []string{"export", "--all"}},
	}
	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			title, args, err := tuiGlobalCommand(tt.operation)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if title != tt.title || !reflect.DeepEqual(args, tt.args) {
				t.Fatalf("got title=%q args=%v, want title=%q args=%v", title, args, tt.title, tt.args)
			}
		})
	}
}

func TestTUIPromptCommandArgs(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		input     string
		want      []string
		wantErr   string
	}{
		{name: "domain plan", operation: "domain-plan", input: "app.example.com", want: []string{"domain", "plan", "tun123", "app.example.com"}},
		{name: "share create", operation: "share-create", input: "review 30m", want: []string{"share", "create", "tun123", "--name", "review", "--ttl", "30m"}},
		{name: "share rotate default ttl", operation: "share-rotate", input: "review", want: []string{"share", "rotate", "tun123", "review", "--ttl", "1h"}},
		{name: "policy clear", operation: "policy-rate", input: "", want: []string{"policy", "set", "tun123", "--clear-rate-limit"}},
		{name: "resources set", operation: "resources-set", input: "10m 32Mi 200m 128Mi", want: []string{"resources", "set", "tun123", "--request-cpu", "10m", "--request-memory", "32Mi", "--limit-cpu", "200m", "--limit-memory", "128Mi"}},
		{name: "apply dry run", operation: "global-apply-dry-run", input: "sealtun.yaml", want: []string{"apply", "-f", "sealtun.yaml", "--dry-run"}},
		{name: "diff", operation: "global-diff", input: "prod.yaml", want: []string{"diff", "-f", "prod.yaml"}},
		{name: "apply", operation: "global-apply", input: "sealtun.yaml", want: []string{"apply", "-f", "sealtun.yaml"}},
		{name: "bad resources", operation: "resources-set", input: "10m 32Mi", wantErr: "4 values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tuiPromptCommandArgs(tt.operation, "tun123", tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestTUICommandActionUsesSourceCommand(t *testing.T) {
	source := &fakeTUISource{}
	model := newTUIModel(source, true)
	model.confirmArgs = []string{"resources", "tun123"}

	msg := runTeaCmd(t, model.runAction(tuiActionCommand, "tun123")).(tuiActionMsg)
	if msg.err != nil {
		t.Fatalf("command action returned error: %v", msg.err)
	}
	if !reflect.DeepEqual(source.commandArgs, []string{"resources", "tun123"}) {
		t.Fatalf("command args = %v", source.commandArgs)
	}
	if msg.text != "command output" {
		t.Fatalf("command output = %q", msg.text)
	}
}

func TestParseTUIExposeArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantPort     string
		wantProtocol string
		wantErr      string
	}{
		{name: "default https", args: []string{"3000"}, wantPort: "3000", wantProtocol: "https"},
		{name: "tcp protocol", args: []string{"6379", "--protocol", "tcp"}, wantPort: "6379", wantProtocol: "tcp"},
		{name: "bad port", args: []string{"70000"}, wantErr: "must be between 1 and 65535"},
		{name: "missing protocol", args: []string{"3000", "--protocol"}, wantErr: "requires a value"},
		{name: "unknown flag", args: []string{"3000", "--domain", "example.com"}, wantErr: "unsupported TUI create argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, proto, err := parseTUIExposeArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if port != tt.wantPort || proto != tt.wantProtocol {
				t.Fatalf("got port=%q protocol=%q, want port=%q protocol=%q", port, proto, tt.wantPort, tt.wantProtocol)
			}
		})
	}
}

func TestRunTUIRejectsNonTerminalIO(t *testing.T) {
	cmd := newTestConnectCommand()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)

	err := runTUI(cmd, tuiOptions{})
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("expected interactive terminal error, got %v", err)
	}
}

func runTeaCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected tea command")
	}
	return cmd()
}

type fakeTUISource struct {
	status              *statusPayload
	tunnels             []listItem
	ports               []discoverItem
	listCheck           bool
	createdArgs         []string
	commandArgs         []string
	discoverHasDeadline bool
	err                 error
}

func (f *fakeTUISource) Status() (*statusPayload, error) {
	return f.status, f.err
}

func (f *fakeTUISource) ListTunnels(check bool) ([]listItem, error) {
	f.listCheck = check
	return f.tunnels, nil
}

func (f *fakeTUISource) DiscoverPorts(ctx context.Context) ([]discoverItem, error) {
	_, f.discoverHasDeadline = ctx.Deadline()
	return f.ports, nil
}

func (f *fakeTUISource) Inspect(context.Context, string) (*inspectPayload, error) {
	return &inspectPayload{}, nil
}

func (f *fakeTUISource) Doctor(context.Context, string) (*tunnelDoctorPayload, error) {
	return &tunnelDoctorPayload{}, nil
}

func (f *fakeTUISource) Metrics(context.Context, string) (*metricsPayload, error) {
	return &metricsPayload{}, nil
}

func (f *fakeTUISource) Events(context.Context, string) (*eventsPayload, error) {
	return &eventsPayload{}, nil
}

func (f *fakeTUISource) Start(context.Context, string) error {
	return nil
}

func (f *fakeTUISource) Stop(context.Context, string) error {
	return nil
}

func (f *fakeTUISource) Cleanup(context.Context, string) error {
	return nil
}

func (f *fakeTUISource) Create(_ context.Context, args []string) error {
	f.createdArgs = append([]string{}, args...)
	if len(args) == 0 {
		return errors.New("missing create args")
	}
	return nil
}

func (f *fakeTUISource) Command(_ context.Context, args ...string) (string, error) {
	f.commandArgs = append([]string{}, args...)
	return "command output", nil
}
