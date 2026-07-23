package clusterconnect

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

type fakeTransparentListener struct {
	acceptErr error
	closed    chan struct{}
	once      sync.Once
}

func (l *fakeTransparentListener) Accept() (net.Conn, error) {
	if l.acceptErr != nil {
		return nil, l.acceptErr
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *fakeTransparentListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *fakeTransparentListener) Addr() net.Addr {
	return &net.TCPAddr{}
}

func TestTransparentPlanIncludesServiceAndPodRoutes(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "demo"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.0.12",
				Ports: []corev1.ServicePort{{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       8080,
					TargetPort: intstr.FromInt32(3000),
				}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "demo"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "web",
				Ports: []corev1.ContainerPort{{Name: "http", Protocol: corev1.ProtocolTCP, ContainerPort: 3000}},
			}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.0.22"},
		},
	)
	env := &Environment{Namespace: "demo", RESTConfig: &rest.Config{Host: "https://kubernetes.example"}, Clientset: client}
	server := NewTransparentServer(env, TransparentOptions{Listen: "127.0.0.1:15443"})
	plan, err := server.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(plan, "10.96.0.12", 8080) {
		t.Fatalf("expected service route, got %#v", plan.Rules)
	}
	if !hasRule(plan, "10.244.0.22", 0) {
		t.Fatalf("expected pod route, got %#v", plan.Rules)
	}
	if len(plan.Hosts) != 1 || plan.Hosts[0].Host != "web" {
		t.Fatalf("expected service hosts, got %#v", plan.Hosts)
	}
	if !contains(plan.Hosts[0].Also, "web.demo.svc.cluster.local") {
		t.Fatalf("expected fqdn host entry, got %#v", plan.Hosts[0])
	}
}

func TestTransparentServerRunPlanValidatesPlan(t *testing.T) {
	server := NewTransparentServer(&Environment{Clientset: fake.NewSimpleClientset()}, TransparentOptions{})
	tests := []struct {
		name string
		plan *TransparentPlan
		want string
	}{
		{name: "nil", plan: nil, want: "transparent plan is required"},
		{name: "missing listen", plan: &TransparentPlan{Namespace: "demo", Rules: []RedirectRule{{Destination: "10.244.0.22"}}}, want: "listen address is required"},
		{name: "no routes", plan: &TransparentPlan{Namespace: "demo", Listen: "127.0.0.1:15443"}, want: "no TCP routes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := server.RunPlan(context.Background(), tt.plan)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestTransparentServerServeListenerReturnsUnexpectedAcceptError(t *testing.T) {
	want := errors.New("connection closed by platform")
	listener := &fakeTransparentListener{acceptErr: want, closed: make(chan struct{})}
	server := &TransparentServer{}

	done := make(chan error, 1)
	go func() {
		done <- server.serveListener(context.Background(), listener)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("serveListener error = %v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("serveListener did not stop its context watcher after accept failed")
	}
}

func TestTransparentServerServeListenerStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listener := &fakeTransparentListener{closed: make(chan struct{})}
	server := &TransparentServer{}
	done := make(chan error, 1)
	go func() {
		done <- server.serveListener(ctx, listener)
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serveListener error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveListener did not stop after context cancellation")
	}
}

func hasRule(plan *TransparentPlan, dest string, port int32) bool {
	for _, rule := range plan.Rules {
		if rule.Destination == dest && rule.Port == port {
			return true
		}
	}
	return false
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestRemoveHostsBlock(t *testing.T) {
	input := "127.0.0.1 localhost\n\n# BEGIN SEALTUN CONNECT\n10.96.0.12 web web.demo.svc.cluster.local\n# END SEALTUN CONNECT\n"
	got := removeHostsBlock(input)
	if strings.Contains(got, "SEALTUN") || strings.Contains(got, "10.96.0.12") {
		t.Fatalf("hosts block was not removed: %q", got)
	}
	if !strings.Contains(got, "127.0.0.1 localhost") {
		t.Fatalf("base hosts content missing: %q", got)
	}
}
