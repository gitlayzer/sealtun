package cmd

import (
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	daemonstate "github.com/labring/sealtun/pkg/daemon"
	"github.com/labring/sealtun/pkg/session"
)

func TestCollectDoctorPayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:  "abc123",
		Host:      "abc.example.com",
		LocalPort: "3000",
		PID:       0,
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}

	if payload.TotalSessions != 1 {
		t.Fatalf("expected 1 total session, got %d", payload.TotalSessions)
	}
	if payload.StaleSessions != 1 {
		t.Fatalf("expected 1 stale session, got %d", payload.StaleSessions)
	}
	if len(payload.Warnings) == 0 {
		t.Fatal("expected warnings to be present")
	}
}

func TestCollectDoctorPayloadDoesNotRequireDaemonForForegroundSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:  "fg123",
		Host:      "fg.example.com",
		LocalPort: "3000",
		PID:       currentPIDForTest(),
		Mode:      "foreground",
		Namespace: "ns-demo",
		Protocol:  "https",
		CreatedAt: time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}

	for _, warning := range payload.Warnings {
		if warning == "daemon is not running; daemon-managed tunnels will not reconnect until it starts" {
			t.Fatal("foreground-only sessions should not require daemon")
		}
	}
}

func TestCollectDoctorPayloadCountsConnectingDaemonSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := daemonstate.SaveState(os.Getpid()); err != nil {
		t.Fatalf("SaveState returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = daemonstate.DeleteState()
	})

	if err := session.Save(session.TunnelSession{
		TunnelID:        "daemon123",
		Host:            "daemon.example.com",
		LocalPort:       "3000",
		PID:             os.Getpid(),
		Mode:            "daemon",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnecting,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}
	if payload.ConnectingSessions != 1 {
		t.Fatalf("expected 1 connecting session, got %d", payload.ConnectingSessions)
	}
	if payload.StaleSessions != 0 {
		t.Fatalf("expected no stale sessions, got %d", payload.StaleSessions)
	}
}

func TestSessionIsStaleTreatsStoppedDaemonSessionAsCleanupEligible(t *testing.T) {
	if !sessionIsStale(session.TunnelSession{
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		UpdatedAt:       time.Now().Format(time.RFC3339),
	}, time.Minute) {
		t.Fatal("expected stopped daemon session to be cleanup eligible")
	}
}

func TestCollectDoctorPayloadCountsStoppedSeparately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "stopped123",
		Host:            "stopped.example.com",
		LocalPort:       "3000",
		Mode:            "daemon",
		ConnectionState: session.ConnectionStateStopped,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}
	if payload.StoppedSessions != 1 {
		t.Fatalf("expected 1 stopped session, got %d", payload.StoppedSessions)
	}
	if payload.StaleSessions != 0 {
		t.Fatalf("expected no stale sessions, got %d", payload.StaleSessions)
	}
}

func TestCollectDoctorPayloadCountsReachablePortsOnlyForActiveSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	activePort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)

	if err := session.Save(session.TunnelSession{
		TunnelID:        "active-down",
		Host:            "active.example.com",
		LocalPort:       "65534",
		PID:             currentPIDForTest(),
		Mode:            "foreground",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnected,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save active session: %v", err)
	}

	if err := session.Save(session.TunnelSession{
		TunnelID:        "connecting-up",
		Host:            "connecting.example.com",
		LocalPort:       activePort,
		PID:             currentPIDForTest(),
		Mode:            "daemon",
		Namespace:       "ns-demo",
		Protocol:        "https",
		ConnectionState: session.ConnectionStateConnecting,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("save connecting session: %v", err)
	}

	payload, err := collectDoctorPayload()
	if err != nil {
		t.Fatalf("collectDoctorPayload: %v", err)
	}
	if payload.ActiveSessions != 1 {
		t.Fatalf("expected 1 active session, got %d", payload.ActiveSessions)
	}
	if payload.ReachableActivePorts != 0 {
		t.Fatalf("expected no reachable active ports, got %d", payload.ReachableActivePorts)
	}
	if !containsWarning(payload.Warnings, "some active tunnels do not have a reachable local port") {
		t.Fatalf("expected active port warning, got %#v", payload.Warnings)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if warning == want {
			return true
		}
	}
	return false
}
