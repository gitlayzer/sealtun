package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	stabilityAnnotation = "sealtun.io/stability"
	stabilityAlpha      = "alpha"
	alphaPrefix         = "[Alpha] "
	alphaNotice         = "Alpha: this feature is experimental and may change or be removed without backward-compatibility guarantees."

	commandGroupCore       = "core"
	commandGroupSecurity   = "security"
	commandGroupAccount    = "account"
	commandGroupOperations = "operations"
	commandGroupAlpha      = "alpha"
	commandGroupOther      = "other"
)

func configureRootCommandGroups() {
	rootCmd.AddGroup(
		&cobra.Group{ID: commandGroupCore, Title: "Core Workflow:"},
		&cobra.Group{ID: commandGroupSecurity, Title: "Security and Access:"},
		&cobra.Group{ID: commandGroupAccount, Title: "Account and Scope:"},
		&cobra.Group{ID: commandGroupOperations, Title: "Operations:"},
		&cobra.Group{ID: commandGroupAlpha, Title: "Alpha Features:"},
		&cobra.Group{ID: commandGroupOther, Title: "Other Commands:"},
	)
	assignCommandGroup(commandGroupCore,
		upCmd, exposeCmd, applyCmd, diffCmd,
		listCmd, inspectCmd, startCmd, stopCmd, cleanupCmd,
	)
	assignCommandGroup(commandGroupSecurity, domainCmd, policyCmd, shareCmd, rotateCmd)
	assignCommandGroup(commandGroupAccount, loginCmd, logoutCmd, statusCmd, profileCmd, regionCmd)
	assignCommandGroup(commandGroupOperations, doctorCmd, logsCmd)
	assignCommandGroup(commandGroupAlpha,
		connectCmd, discoverCmd, eventsCmd, exportCmd, initCmd, meshCmd,
		metricsCmd, resourcesCmd, sshCmd, templateCmd, tuiCmd, watchCmd,
	)
	rootCmd.SetHelpCommandGroupID(commandGroupOther)
	rootCmd.SetCompletionCommandGroupID(commandGroupOther)
}

func assignCommandGroup(groupID string, commands ...*cobra.Command) {
	for _, cmd := range commands {
		if cmd != nil {
			cmd.GroupID = groupID
		}
	}
}

func markAlpha(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[stabilityAnnotation] = stabilityAlpha
	if !strings.HasPrefix(cmd.Short, alphaPrefix) {
		cmd.Short = alphaPrefix + cmd.Short
	}
	if !strings.Contains(cmd.Long, alphaNotice) {
		long := strings.TrimSpace(cmd.Long)
		if long == "" {
			long = strings.TrimSpace(strings.TrimPrefix(cmd.Short, alphaPrefix))
		}
		cmd.Long = long + "\n\n" + alphaNotice
	}
}

func markAlphaTree(cmd *cobra.Command) {
	markAlpha(cmd)
	for _, child := range cmd.Commands() {
		markAlphaTree(child)
	}
}

func markDeprecated(cmd *cobra.Command, replacement string) {
	if cmd == nil {
		return
	}
	cmd.Deprecated = fmt.Sprintf("use `%s`; this compatibility command may be removed in a future release", replacement)
}
