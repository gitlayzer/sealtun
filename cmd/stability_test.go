package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAlphaCommandsAreClearlyMarked(t *testing.T) {
	roots := []*cobra.Command{
		tuiCmd,
		connectCmd,
		meshCmd,
		initCmd,
		sshCmd,
		discoverCmd,
		eventsCmd,
		exportCmd,
		metricsCmd,
		resourcesCmd,
		templateCmd,
		watchCmd,
	}
	for _, root := range roots {
		walkCommandTree(root, func(command *cobra.Command) {
			t.Run(command.CommandPath(), func(t *testing.T) {
				if command.Annotations[stabilityAnnotation] != stabilityAlpha {
					t.Fatalf("%s should be marked alpha", command.CommandPath())
				}
				if !strings.HasPrefix(command.Short, alphaPrefix) {
					t.Fatalf("%s short help does not expose alpha status: %q", command.CommandPath(), command.Short)
				}
				if !strings.Contains(command.Long, alphaNotice) {
					t.Fatalf("%s long help does not contain the alpha notice", command.CommandPath())
				}
			})
		})
	}
}

func TestCoreCommandsRemainStable(t *testing.T) {
	commands := []*cobra.Command{
		upCmd, exposeCmd, applyCmd, diffCmd,
		listCmd, inspectCmd, startCmd, stopCmd, cleanupCmd,
		doctorCmd, logsCmd,
		domainCmd, policyCmd, shareCmd, rotateCmd,
		loginCmd, logoutCmd, statusCmd, profileCmd, regionCmd,
	}
	for _, command := range commands {
		if command.Annotations[stabilityAnnotation] == stabilityAlpha {
			t.Fatalf("%s should remain stable", command.CommandPath())
		}
	}
}

func TestRootCommandsAreGroupedByStabilityAndIntent(t *testing.T) {
	tests := []struct {
		groupID  string
		commands []*cobra.Command
	}{
		{commandGroupCore, []*cobra.Command{upCmd, exposeCmd, applyCmd, diffCmd, listCmd, inspectCmd, startCmd, stopCmd, cleanupCmd}},
		{commandGroupSecurity, []*cobra.Command{domainCmd, policyCmd, shareCmd, rotateCmd}},
		{commandGroupAccount, []*cobra.Command{loginCmd, logoutCmd, statusCmd, profileCmd, regionCmd}},
		{commandGroupOperations, []*cobra.Command{doctorCmd, logsCmd}},
		{commandGroupAlpha, []*cobra.Command{connectCmd, discoverCmd, eventsCmd, exportCmd, initCmd, meshCmd, metricsCmd, resourcesCmd, sshCmd, templateCmd, tuiCmd, watchCmd}},
	}
	for _, test := range tests {
		for _, command := range test.commands {
			if command.GroupID != test.groupID {
				t.Fatalf("%s group = %q, want %q", command.CommandPath(), command.GroupID, test.groupID)
			}
		}
	}
}

func TestEveryVisibleRootCommandHasAKnownGroup(t *testing.T) {
	groups := rootCmd.Groups()
	known := make(map[string]bool, len(groups))
	for _, group := range groups {
		known[group.ID] = true
	}
	for _, command := range rootCmd.Commands() {
		if command.Hidden || command.Deprecated != "" {
			continue
		}
		if !known[command.GroupID] {
			t.Fatalf("visible root command %s has unknown group %q", command.Name(), command.GroupID)
		}
	}
}

func TestCompatibilityCommandsPointToCanonicalReplacements(t *testing.T) {
	tests := []struct {
		command     *cobra.Command
		replacement string
	}{
		{repairCmd, "sealtun doctor <tunnel-id> --fix"},
		{domainSetCmd, "sealtun domain add <tunnel-id> <domain>"},
		{disconnectCmd, "sealtun connect disconnect"},
	}
	for _, test := range tests {
		if !strings.Contains(test.command.Deprecated, test.replacement) {
			t.Fatalf("%s deprecation should identify %q: %q", test.command.CommandPath(), test.replacement, test.command.Deprecated)
		}
	}
}

func TestConnectDisconnectCanonicalCommandIsRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"connect", "disconnect"})
	if err != nil {
		t.Fatalf("find connect disconnect: %v", err)
	}
	if command != connectDisconnectCmd {
		t.Fatalf("connect disconnect resolved to %q", command.CommandPath())
	}
}

func walkCommandTree(command *cobra.Command, visit func(*cobra.Command)) {
	visit(command)
	for _, child := range command.Commands() {
		walkCommandTree(child, visit)
	}
}
