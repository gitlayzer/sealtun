package cmd

import (
	"fmt"
	"os"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the current Sealtun login session",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := auth.LoadAuthData()
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No active Sealtun session found.")
				return nil
			}
			return fmt.Errorf("failed to load auth data: %w", err)
		}

		if err := auth.ClearAuthData(); err != nil {
			return fmt.Errorf("failed to clear auth data: %w", err)
		}

		fmt.Println("Logged out. Removed local Sealtun credentials.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
