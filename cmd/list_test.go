package cmd

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

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
}

func currentPIDForTest() int {
	return sessionTestCurrentPID()
}
