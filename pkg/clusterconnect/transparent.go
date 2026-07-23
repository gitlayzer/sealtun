package clusterconnect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const defaultRedirectListen = "127.0.0.1:15443"

type TransparentOptions struct {
	Namespace string
	Listen    string
}

type RedirectRule struct {
	Destination string `json:"destination"`
	Port        int32  `json:"port"`
}

type HostEntry struct {
	IP   string   `json:"ip"`
	Host string   `json:"host"`
	Also []string `json:"also,omitempty"`
}

type TransparentPlan struct {
	Namespace string         `json:"namespace"`
	Listen    string         `json:"listen"`
	Rules     []RedirectRule `json:"rules"`
	Hosts     []HostEntry    `json:"hosts"`
}

type TransparentServer struct {
	Env      *Environment
	Options  TransparentOptions
	Resolver *Resolver
	Dialer   PodDialer
	Stdout   io.Writer
	Stderr   io.Writer

	applied *TransparentPlan
	mu      sync.Mutex
}

func CleanupTransparentState(plan *TransparentPlan) error {
	if plan == nil || (len(plan.Rules) == 0 && len(plan.Hosts) == 0) {
		return nil
	}
	return cleanupTransparentPlan(plan)
}

func NewTransparentServer(env *Environment, opts TransparentOptions) *TransparentServer {
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" && env != nil {
		namespace = env.Namespace
	}
	listen := strings.TrimSpace(opts.Listen)
	if listen == "" {
		listen = defaultRedirectListen
	}
	return &TransparentServer{
		Env: env,
		Options: TransparentOptions{
			Namespace: namespace,
			Listen:    listen,
		},
	}
}

func (s *TransparentServer) Plan(ctx context.Context) (*TransparentPlan, error) {
	if s == nil || s.Env == nil || s.Env.Clientset == nil {
		return nil, fmt.Errorf("transparent connect environment is not initialized")
	}
	namespace := strings.TrimSpace(s.Options.Namespace)
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	listen := strings.TrimSpace(s.Options.Listen)
	if listen == "" {
		listen = defaultRedirectListen
	}
	_, listenPort, err := splitListen(listen)
	if err != nil {
		return nil, err
	}

	services, err := s.Env.Clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, err := s.Env.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	seenRules := map[string]struct{}{}
	seenHosts := map[string]struct{}{}
	plan := &TransparentPlan{Namespace: namespace, Listen: listen}
	for i := range services.Items {
		svc := &services.Items[i]
		if !isUsableClusterService(svc) {
			continue
		}
		names := serviceHostnames(svc.Name, svc.Namespace)
		key := svc.Spec.ClusterIP + " " + strings.Join(names, " ")
		if _, ok := seenHosts[key]; !ok {
			seenHosts[key] = struct{}{}
			plan.Hosts = append(plan.Hosts, HostEntry{IP: svc.Spec.ClusterIP, Host: names[0], Also: names[1:]})
		}
		for _, port := range svc.Spec.Ports {
			if port.Protocol != corev1.ProtocolTCP || port.Port <= 0 {
				continue
			}
			key := fmt.Sprintf("%s:%d", svc.Spec.ClusterIP, port.Port)
			if _, ok := seenRules[key]; ok {
				continue
			}
			seenRules[key] = struct{}{}
			plan.Rules = append(plan.Rules, RedirectRule{Destination: svc.Spec.ClusterIP, Port: port.Port})
		}
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil || pod.Status.PodIP == "" {
			continue
		}
		key := pod.Status.PodIP + ":*"
		if _, ok := seenRules[key]; !ok {
			seenRules[key] = struct{}{}
			plan.Rules = append(plan.Rules, RedirectRule{Destination: pod.Status.PodIP})
		}
	}
	if len(plan.Rules) == 0 {
		return nil, fmt.Errorf("no TCP service or pod ports discovered in namespace %s", namespace)
	}
	if listenPort <= 0 {
		return nil, fmt.Errorf("invalid redirect listen port")
	}
	return plan, nil
}

func (s *TransparentServer) Run(ctx context.Context) error {
	plan, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	return s.RunPlan(ctx, plan)
}

func (s *TransparentServer) RunPlan(ctx context.Context, plan *TransparentPlan) error {
	if s == nil || s.Env == nil || s.Env.Clientset == nil {
		return fmt.Errorf("transparent connect environment is not initialized")
	}
	if plan == nil {
		return fmt.Errorf("transparent plan is required")
	}
	if strings.TrimSpace(plan.Listen) == "" {
		return fmt.Errorf("transparent plan listen address is required")
	}
	if len(plan.Rules) == 0 {
		return fmt.Errorf("transparent plan has no TCP routes")
	}
	if s.Resolver == nil {
		s.Resolver = NewResolver(s.Env.Clientset, plan.Namespace)
	}
	if s.Dialer == nil {
		s.Dialer = &PortForwardDialer{RESTConfig: s.Env.RESTConfig, Clientset: s.Env.Clientset}
	}
	if err := requireTransparentPrivileges(); err != nil {
		return err
	}
	if err := applyTransparentPlan(plan); err != nil {
		return err
	}
	s.mu.Lock()
	s.applied = plan
	s.mu.Unlock()
	defer func() {
		if cleanupErr := cleanupTransparentPlan(plan); cleanupErr != nil {
			s.eprintf("transparent connect cleanup failed: %v\n", cleanupErr)
		}
	}()
	s.printf("Transparent connect active for namespace %s\n", plan.Namespace)
	s.printf("  Listening: %s\n", plan.Listen)
	s.printf("  TCP routes: %d\n", len(plan.Rules))
	s.printf("  Host entries: %d\n", len(plan.Hosts))
	s.printf("Press Ctrl-C or run `sealtun connect disconnect` from another terminal to stop.\n")
	return s.listenAndServe(ctx, plan.Listen)
}

func (s *TransparentServer) listenAndServe(ctx context.Context, listen string) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	return s.serveListener(ctx, ln)
}

func (s *TransparentServer) serveListener(ctx context.Context, ln net.Listener) error {
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = ln.Close()
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		<-stopped
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("transparent listener closed unexpectedly: %w", err)
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *TransparentServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	dst, err := originalDestination(conn)
	if err != nil {
		s.eprintf("transparent connect cannot resolve original destination: %v\n", err)
		return
	}
	target, err := s.Resolver.Resolve(ctx, dst.IP.String(), dst.Port)
	if err != nil {
		s.eprintf("transparent connect cannot resolve %s:%d: %v\n", dst.IP.String(), dst.Port, err)
		return
	}
	upstream, err := s.Dialer.DialPod(ctx, target.Namespace, target.PodName, target.PodPort)
	if err != nil {
		s.eprintf("transparent connect port-forward failed for %s/%s:%d: %v\n", target.Namespace, target.PodName, target.PodPort, err)
		return
	}
	defer upstream.Close()
	copyBoth(conn, upstream)
}

func (s *TransparentServer) printf(format string, args ...interface{}) {
	if s.Stdout != nil {
		fmt.Fprintf(s.Stdout, format, args...)
	}
}

func (s *TransparentServer) eprintf(format string, args ...interface{}) {
	if s.Stderr != nil {
		fmt.Fprintf(s.Stderr, format, args...)
	}
}

func isUsableClusterService(svc *corev1.Service) bool {
	return svc != nil &&
		svc.Spec.Type != corev1.ServiceTypeExternalName &&
		svc.Spec.ClusterIP != "" &&
		svc.Spec.ClusterIP != corev1.ClusterIPNone
}

func serviceHostnames(name, namespace string) []string {
	return []string{
		name,
		name + "." + namespace,
		name + "." + namespace + ".svc",
		name + "." + namespace + ".svc.cluster.local",
	}
}

func splitListen(listen string) (string, int, error) {
	host, portText, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid listen port %q", portText)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port, nil
}

func copyBoth(a net.Conn, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		_ = a.SetReadDeadline(time.Now())
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		_ = b.SetReadDeadline(time.Now())
		done <- struct{}{}
	}()
	<-done
}
