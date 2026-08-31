package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandsAreGroupedByStabilityAndIntent(t *testing.T) {
	tests := []struct {
		groupID  string
		commands []*cobra.Command
	}{
		{commandGroupCore, []*cobra.Command{upCmd, exposeCmd, applyCmd, diffCmd, listCmd, inspectCmd, startCmd, stopCmd, cleanupCmd}},
		{commandGroupSecurity, []*cobra.Command{domainCmd, policyCmd, shareCmd, rotateCmd}},
		{commandGroupAccount, []*cobra.Command{loginCmd, logoutCmd, statusCmd, profileCmd, regionCmd}},
		{commandGroupOperations, []*cobra.Command{doctorCmd, logsCmd}},
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

func TestCanonicalCommandsDoNotExposeRemovedAliases(t *testing.T) {
	commands := []*cobra.Command{
		startCmd,
		shareCreateCmd,
		shareRevokeCmd,
		profileDeleteCmd,
	}
	for _, command := range commands {
		if len(command.Aliases) != 0 {
			t.Fatalf("%s still exposes aliases: %v", command.CommandPath(), command.Aliases)
		}
	}

	removed := []struct {
		parent *cobra.Command
		name   string
	}{
		{rootCmd, "resume"},
		{shareCmd, "add"},
		{shareCmd, "delete"},
		{shareCmd, "remove"},
		{profileCmd, "rm"},
		{profileCmd, "remove"},
	}
	for _, item := range removed {
		if commandNamedOrAliased(item.parent, item.name) != nil {
			t.Fatalf("removed command alias %q is still registered under %s", item.name, item.parent.CommandPath())
		}
	}
}

func TestHiddenRuntimeCommandsRemainRegistered(t *testing.T) {
	for _, command := range []*cobra.Command{daemonCmd, serverCmd} {
		if command == nil {
			t.Fatal("runtime command is nil")
		}
		if !command.Hidden {
			t.Fatalf("runtime command %s must remain hidden", command.CommandPath())
		}
		if commandNamedOrAliased(rootCmd, command.Name()) != command {
			t.Fatalf("runtime command %s is not registered on root", command.Name())
		}
	}
}

func commandNamedOrAliased(parent *cobra.Command, name string) *cobra.Command {
	for _, command := range parent.Commands() {
		if command.Name() == name {
			return command
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return command
			}
		}
	}
	return nil
}
