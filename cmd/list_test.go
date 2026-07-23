package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/k8s"
	"github.com/labring/sealtun/pkg/session"
)

func BenchmarkCollectListItemsRemoteRefresh(b *testing.B) {
	home := b.TempDir()
	b.Setenv("HOME", home)
	for i := 0; i < 8; i++ {
		if err := session.Save(session.TunnelSession{
			TunnelID:  fmt.Sprintf("bench-%02d", i),
			Region:    "https://gzg.sealos.run",
			Namespace: "ns-bench",
			Protocol:  "https",
			LocalPort: "3000",
		}); err != nil {
			b.Fatalf("save session %d: %v", i, err)
		}
	}
	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		select {
		case <-time.After(time.Millisecond):
			return &k8s.TunnelRemoteState{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.Cleanup(func() { collectSessionRemoteState = originalCollector })

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := collectListItemsWithLocalCheck(false)
		if err != nil {
			b.Fatal(err)
		}
		if len(items) != 8 {
			b.Fatalf("got %d items, want 8", len(items))
		}
	}
}

func TestCollectListItems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	now := time.Now().Format(time.RFC3339)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "active123",
		Host:      "active.example.com",
		LocalPort: "3000",
		PID:       0,
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("save active session: %v", err)
	}
	if err := session.Save(session.TunnelSession{
		TunnelID:  "self123",
		Host:      "self.example.com",
		LocalPort: "65534",
		PID:       currentPIDForTest(),
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("save self session: %v", err)
	}

	items, err := collectListItems()
	if err != nil {
		t.Fatalf("collectListItems returned error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].TunnelID != "active123" {
		t.Fatalf("unexpected first tunnel id: %s", items[0].TunnelID)
	}
	if items[0].Status != "stale" {
		t.Fatalf("expected stale status, got %s", items[0].Status)
	}
	if items[1].Status != "running" {
		t.Fatalf("expected running status, got %s", items[1].Status)
	}
	if items[0].Endpoint != "https://active.example.com" {
		t.Fatalf("unexpected https endpoint: %s", items[0].Endpoint)
	}
}

func TestCollectListItemsShowsSSHEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:   "sshdev",
		Host:       "ssh.example.com",
		SealosHost: "control.example.com",
		PublicPort: 32022,
		LocalPort:  "22",
		Namespace:  "ns-demo",
		Protocol:   "ssh",
		CreatedAt:  time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save ssh session: %v", err)
	}

	items, err := collectListItems()
	if err != nil {
		t.Fatalf("collectListItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Endpoint != "ssh <user>@ssh.example.com -p 32022" {
		t.Fatalf("unexpected ssh endpoint: %s", items[0].Endpoint)
	}
	if items[0].TargetURL != "localhost:22" {
		t.Fatalf("unexpected ssh target: %s", items[0].TargetURL)
	}
}

func TestCollectListItemsShowsTCPEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:   "postgres",
		Host:       "db.example.com",
		SealosHost: "control.example.com",
		PublicPort: 35432,
		LocalPort:  "5432",
		Namespace:  "ns-demo",
		Protocol:   "tcp",
		CreatedAt:  time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save tcp session: %v", err)
	}

	items, err := collectListItems()
	if err != nil {
		t.Fatalf("collectListItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Endpoint != "db.example.com:35432" {
		t.Fatalf("unexpected tcp endpoint: %s", items[0].Endpoint)
	}
	if items[0].TargetURL != "localhost:5432" {
		t.Fatalf("unexpected tcp target: %s", items[0].TargetURL)
	}
}

func TestCollectListItemsWithLocalCheckDegradesForegroundTunnelWhenLocalPortIsDown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "fg-down",
		Host:            "fg.example.com",
		LocalPort:       "65534",
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	items, err := collectListItemsWithLocalCheck(true)
	if err != nil {
		t.Fatalf("collectListItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != "degraded" {
		t.Fatalf("expected degraded status, got %s", items[0].Status)
	}
}

func TestCollectListItemsWithLocalCheckKeepsReachableForegroundTunnelActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "fg-up",
		Host:            "fg.example.com",
		LocalPort:       port,
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	items, err := collectListItemsWithLocalCheck(true)
	if err != nil {
		t.Fatalf("collectListItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != "active" {
		t.Fatalf("expected active status, got %s", items[0].Status)
	}
}

func TestPrintListTableEmpty(t *testing.T) {
	var output strings.Builder
	listCmd.SetOut(&output)
	t.Cleanup(func() { listCmd.SetOut(nil) })

	printListTable(listCmd, nil)

	if !strings.Contains(output.String(), "No local Sealtun tunnel sessions found.") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestListJSONShape(t *testing.T) {
	items := []listItem{{
		TunnelID:     "abc123",
		Status:       "active",
		Host:         "abc.example.com",
		SealosHost:   "sealtun-abc123-ns-demo.sealosgzg.site",
		CustomDomain: "abc.example.com",
		LocalPort:    "3000",
		PID:          123,
		Mode:         "foreground",
		Namespace:    "ns-demo",
		Protocol:     "https",
		Endpoint:     "https://abc.example.com",
		CreatedAt:    "2026-04-23T10:00:00+08:00",
	}}

	data, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	jsonText := string(data)
	if !strings.Contains(jsonText, `"status":"active"`) {
		t.Fatalf("missing status field: %s", jsonText)
	}
	if !strings.Contains(jsonText, `"host":"abc.example.com"`) {
		t.Fatalf("missing host field: %s", jsonText)
	}
	if !strings.Contains(jsonText, `"sealosHost":"sealtun-abc123-ns-demo.sealosgzg.site"`) {
		t.Fatalf("missing sealosHost field: %s", jsonText)
	}
	if !strings.Contains(jsonText, `"customDomain":"abc.example.com"`) {
		t.Fatalf("missing customDomain field: %s", jsonText)
	}
	if !strings.Contains(jsonText, `"endpoint":"https://abc.example.com"`) {
		t.Fatalf("missing endpoint field: %s", jsonText)
	}
}

func currentPIDForTest() int {
	return sessionTestCurrentPID()
}

func TestCollectListItemsRefreshesRemoteHTTPSState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return &k8s.TunnelRemoteState{
			PublicHost:   "ai-gateway.code05.com",
			SealosHost:   "sealtun-abc123-ns-demo.bja.sealos.run",
			CustomDomain: "ai-gateway.code05.com",
		}, nil
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	now := time.Now().Format(time.RFC3339)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "old.example.com",
		LocalPort: "3000",
		PID:       0,
		Region:    "https://bja.sealos.run",
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	items, err := collectListItems()
	if err != nil {
		t.Fatalf("collectListItems returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Host != "ai-gateway.code05.com" || items[0].CustomDomain != "ai-gateway.code05.com" {
		t.Fatalf("expected refreshed public host/custom domain, got %#v", items[0])
	}
	if items[0].SealosHost != "sealtun-abc123-ns-demo.bja.sealos.run" {
		t.Fatalf("expected refreshed sealos host, got %#v", items[0])
	}
}

func TestRefreshSessionFromRemoteClearsRemovedPublicPort(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return &k8s.TunnelRemoteState{
			Protocol:     "tcp",
			LocalPort:    "5432",
			DeploymentOK: true,
			PublicPort:   0,
		}, nil
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	sess := session.TunnelSession{
		TunnelID:   "removed-port",
		Namespace:  "ns-demo",
		Protocol:   "tcp",
		LocalPort:  "5432",
		PublicPort: 35432,
	}
	if err := session.Save(sess); err != nil {
		t.Fatal(err)
	}
	if err := refreshSessionFromRemoteLocked(context.Background(), &sess); err != nil {
		t.Fatalf("refreshSessionFromRemoteLocked returned error: %v", err)
	}
	if sess.PublicPort != 0 {
		t.Fatalf("stale public port was retained after remote removal: %d", sess.PublicPort)
	}
}

func TestFindSessionSyncedPropagatesRemoteRefreshFailure(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:  "sync-failure",
		Region:    "https://gzg.sealos.run",
		Namespace: "ns-demo",
		Protocol:  "https",
	}); err != nil {
		t.Fatal(err)
	}

	want := errors.New("remote state unavailable")
	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(context.Context, session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		return nil, want
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	_, err := findSessionSyncedLocked(context.Background(), "sync-failure")
	if !errors.Is(err, want) {
		t.Fatalf("sync error = %v, want %v", err, want)
	}
}

func TestReadRefreshRereadsSessionAfterTunnelOperationLock(t *testing.T) {
	t.Setenv("SEALTUN_HOME", t.TempDir())
	if err := session.Save(session.TunnelSession{
		TunnelID:        "refreshlocked",
		Region:          "https://gzg.sealos.run",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnected,
	}); err != nil {
		t.Fatal(err)
	}
	stale, err := session.Get("refreshlocked")
	if err != nil {
		t.Fatal(err)
	}

	releaseLock := holdTunnelOperationLock(t, "refreshlocked")
	defer releaseLock()
	remoteCalled := make(chan struct{}, 1)
	var collectorErr error
	previousCollect := collectSessionRemoteState
	collectSessionRemoteState = func(context.Context, session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		remoteCalled <- struct{}{}
		current, err := session.Get("refreshlocked")
		if err != nil {
			collectorErr = err
			return nil, err
		}
		current.LastError = "daemon update during remote query"
		collectorErr = session.Update(*current)
		if collectorErr != nil {
			return nil, collectorErr
		}
		return &k8s.TunnelRemoteState{DeploymentOK: true, Protocol: "https"}, nil
	}
	t.Cleanup(func() { collectSessionRemoteState = previousCollect })

	done := make(chan error, 1)
	go func() {
		done <- refreshSessionFromRemote(context.Background(), stale)
	}()
	assertOperationBlocked(t, remoteCalled)

	current, err := session.Get("refreshlocked")
	if err != nil {
		t.Fatal(err)
	}
	current.ConnectionState = session.ConnectionStateStopped
	if err := session.Update(*current); err != nil {
		t.Fatal(err)
	}
	releaseLock()
	if err := <-done; err != nil {
		t.Fatalf("refreshSessionFromRemote returned error: %v", err)
	}
	if collectorErr != nil {
		t.Fatalf("collector update failed: %v", collectorErr)
	}
	if stale.ConnectionState != session.ConnectionStateStopped {
		t.Fatalf("refresh overwrote newer local state: %s", stale.ConnectionState)
	}
	if stale.LastError != "daemon update during remote query" {
		t.Fatalf("refresh overwrote daemon state: %q", stale.LastError)
	}
	persisted, err := session.Get("refreshlocked")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ConnectionState != session.ConnectionStateStopped {
		t.Fatalf("persisted state was overwritten by stale refresh: %s", persisted.ConnectionState)
	}
	if persisted.LastError != "daemon update during remote query" {
		t.Fatalf("persisted daemon state was overwritten: %q", persisted.LastError)
	}
}

func TestCollectListItemsPropagatesContextCancellation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := session.Save(session.TunnelSession{
		TunnelID:  "cancel-list",
		Region:    "https://gzg.sealos.run",
		Namespace: "ns-demo",
		Protocol:  "https",
	}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	originalCollector := collectSessionRemoteState
	collectSessionRemoteState = func(ctx context.Context, sess session.TunnelSession) (*k8s.TunnelRemoteState, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { collectSessionRemoteState = originalCollector })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := collectListItemsWithContext(ctx, false)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
