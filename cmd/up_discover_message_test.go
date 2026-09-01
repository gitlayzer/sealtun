package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The non-interactive multi-discovery error must not point at the removed
// `sealtun discover` command.
func TestSelectUpDiscoveredPortErrorDoesNotReferenceDiscoverCommand(t *testing.T) {
	cmd := &cobra.Command{}
	items := []discoverItem{
		{Port: 3000, Address: "127.0.0.1"},
		{Port: 5432, Address: "127.0.0.1"},
	}
	_, err := selectUpDiscoveredPort(cmd, items, false)
	if err == nil {
		t.Fatal("expected an error for multiple discoveries in non-interactive mode")
	}
	if strings.Contains(err.Error(), "sealtun discover") {
		t.Fatalf("error references the removed discover command: %v", err)
	}
	if !strings.Contains(err.Error(), "sealtun up") {
		t.Fatalf("error should suggest an actionable up command: %v", err)
	}
}
