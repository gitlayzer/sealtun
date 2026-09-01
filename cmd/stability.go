package cmd

import "github.com/spf13/cobra"

const (
	commandGroupCore       = "core"
	commandGroupSecurity   = "security"
	commandGroupAccount    = "account"
	commandGroupOperations = "operations"
	commandGroupOther      = "other"
)

func configureRootCommandGroups() {
	rootCmd.AddGroup(
		&cobra.Group{ID: commandGroupCore, Title: "Core Workflow:"},
		&cobra.Group{ID: commandGroupSecurity, Title: "Security and Access:"},
		&cobra.Group{ID: commandGroupAccount, Title: "Account and Scope:"},
		&cobra.Group{ID: commandGroupOperations, Title: "Operations:"},
		&cobra.Group{ID: commandGroupOther, Title: "Other Commands:"},
	)
	assignCommandGroup(commandGroupCore,
		upCmd, exposeCmd, applyCmd,
		listCmd, inspectCmd, startCmd, stopCmd, cleanupCmd,
	)
	assignCommandGroup(commandGroupSecurity, domainCmd, policyCmd, shareCmd, rotateCmd)
	assignCommandGroup(commandGroupAccount, loginCmd, logoutCmd, statusCmd, profileCmd, regionCmd)
	assignCommandGroup(commandGroupOperations, doctorCmd, logsCmd)
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
