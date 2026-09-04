package cmd

import (
	"testing"
	"time"

	"github.com/labring/sealtun/pkg/session"
)

func TestSessionNeedsAutomaticRecoverySparesDaemonSessions(t *testing.T) {
	dead := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	daemonSess := session.TunnelSession{
		TunnelID:        "t1",
		Mode:            "daemon",
		PID:             999999, // not alive
		ConnectionState: session.ConnectionStateConnected,
		UpdatedAt:       dead,
	}
	if sessionNeedsAutomaticRecovery(daemonSess, time.Minute) {
		t.Fatal("daemon-mode session must survive a dead daemon; the next daemon re-adopts it")
	}
	foregroundSess := daemonSess
	foregroundSess.Mode = "foreground"
	if !sessionNeedsAutomaticRecovery(foregroundSess, time.Minute) {
		t.Fatal("foreground session with a dead owner must be recovered")
	}
}
