package cmd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/labring/sealtun/pkg/clusterconnect"
	"github.com/spf13/cobra"
)

func TestRunConnectSavesStateBeforeRunAndRemovesAfterRun(t *testing.T) {
	previousSave := connectSaveState
	previousRemove := connectRemoveState
	t.Cleanup(func() {
		connectSaveState = previousSave
		connectRemoveState = previousRemove
	})

	events := []string{}
	var saved clusterconnect.State
	connectSaveState = func(state clusterconnect.State) error {
		events = append(events, "save")
		saved = state
		return nil
	}
	connectRemoveState = func() error {
		events = append(events, "remove")
		return nil
	}

	plan := &clusterconnect.TransparentPlan{
		Namespace: "ns-test",
		Listen:    "127.0.0.1:15443",
		Rules:     []clusterconnect.RedirectRule{{Destination: "10.96.0.12", Port: 80}},
		Hosts:     []clusterconnect.HostEntry{{IP: "10.96.0.12", Host: "web.ns-test.svc.cluster.local"}},
	}
	server := &fakeConnectServer{plan: plan, events: &events}
	env := &fakeConnectEnv{preflight: &clusterconnect.Preflight{
		SelectedMode:  clusterconnect.ModeTun,
		Namespace:     "ns-test",
		Region:        "https://gzg.sealos.run",
		ActiveProfile: "gzg",
	}}
	cmd := newTestConnectCommand()

	err := runConnectWithEnvironment(cmd, connectOptions{
		Mode:      clusterconnect.ModeTun,
		Namespace: "ns-test",
		Listen:    "127.0.0.1:15443",
	}, env, func(options clusterconnect.TransparentOptions) connectPlanRunner {
		if options.Namespace != "ns-test" || options.Listen != "127.0.0.1:15443" {
			t.Fatalf("unexpected transparent options: %#v", options)
		}
		return server
	})
	if err != nil {
		t.Fatalf("runConnectWithEnvironment returned error: %v", err)
	}
	if want := []string{"plan", "save", "run", "remove"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected event order: got %v want %v", events, want)
	}
	if server.runPlan != plan {
		t.Fatalf("RunPlan received wrong plan: %#v", server.runPlan)
	}
	if saved.Mode != clusterconnect.ModeTun || saved.Namespace != "ns-test" || saved.Region != "https://gzg.sealos.run" || saved.Profile != "gzg" {
		t.Fatalf("unexpected saved state: %#v", saved)
	}
	if saved.Listen != plan.Listen || saved.RouteCount != 1 || saved.HostCount != 1 {
		t.Fatalf("saved state does not reflect plan: %#v", saved)
	}
	if saved.PID == 0 {
		t.Fatalf("expected saved state PID to be set")
	}
}

func TestRunConnectDoesNotSaveStateWhenPlanFails(t *testing.T) {
	previousSave := connectSaveState
	previousRemove := connectRemoveState
	t.Cleanup(func() {
		connectSaveState = previousSave
		connectRemoveState = previousRemove
	})

	events := []string{}
	connectSaveState = func(clusterconnect.State) error {
		events = append(events, "save")
		return nil
	}
	connectRemoveState = func() error {
		events = append(events, "remove")
		return nil
	}
	planErr := errors.New("plan failed")
	server := &fakeConnectServer{planErr: planErr, events: &events}
	env := &fakeConnectEnv{preflight: &clusterconnect.Preflight{
		SelectedMode:  clusterconnect.ModeTun,
		Namespace:     "ns-test",
		Region:        "https://gzg.sealos.run",
		ActiveProfile: "gzg",
	}}

	err := runConnectWithEnvironment(newTestConnectCommand(), connectOptions{}, env, func(clusterconnect.TransparentOptions) connectPlanRunner {
		return server
	})
	if !errors.Is(err, planErr) {
		t.Fatalf("expected plan error, got %v", err)
	}
	if want := []string{"plan"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected event order: got %v want %v", events, want)
	}
}

type fakeConnectEnv struct {
	preflight *clusterconnect.Preflight
	err       error
	options   clusterconnect.Options
}

func (f *fakeConnectEnv) Preflight(ctx context.Context, opts clusterconnect.Options) (*clusterconnect.Preflight, error) {
	f.options = opts
	return f.preflight, f.err
}

type fakeConnectServer struct {
	plan    *clusterconnect.TransparentPlan
	planErr error
	runErr  error
	runPlan *clusterconnect.TransparentPlan
	events  *[]string
}

func (s *fakeConnectServer) Plan(context.Context) (*clusterconnect.TransparentPlan, error) {
	*s.events = append(*s.events, "plan")
	return s.plan, s.planErr
}

func (s *fakeConnectServer) RunPlan(ctx context.Context, plan *clusterconnect.TransparentPlan) error {
	*s.events = append(*s.events, "run")
	s.runPlan = plan
	return s.runErr
}

func newTestConnectCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd
}
