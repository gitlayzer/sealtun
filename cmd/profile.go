package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/spf13/cobra"
)

type profileItem struct {
	Name              string `json:"name"`
	Current           bool   `json:"current"`
	Region            string `json:"region,omitempty"`
	SealosDomain      string `json:"sealosDomain,omitempty"`
	WorkspaceID       string `json:"workspaceId,omitempty"`
	WorkspaceName     string `json:"workspaceName,omitempty"`
	AuthenticatedAt   string `json:"authenticatedAt,omitempty"`
	KubeconfigPresent bool   `json:"kubeconfigPresent"`
	Error             string `json:"error,omitempty"`
}

var profileJSON bool

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage named Sealtun login profiles",
	Long: `Manage named Sealtun login profiles.

A profile stores one Sealos login bundle: auth data, region metadata, workspace
metadata, and kubeconfig. Switching profiles replaces the active auth.json and
kubeconfig used by commands such as expose, status, and region current.`,
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved login profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := collectProfileItems()
		if err != nil {
			return err
		}
		if profileJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}
		printProfileList(cmd, items)
		return nil
	},
}

var profileCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the active named profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := auth.CurrentSealtunDir()
		if err != nil {
			return fmt.Errorf("load current profile marker: %w", err)
		}
		name, err := auth.CurrentProfileNameFromDir(root)
		if err != nil {
			return fmt.Errorf("load current profile marker: %w", err)
		}
		if profileJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(struct {
				Name string `json:"name,omitempty"`
			}{Name: name})
		}
		if name == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "No active named profile.")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), name)
		return nil
	},
}

var profileSaveCmd = &cobra.Command{
	Use:          "save [name]",
	Short:        "Save the current login as a named profile",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		authData, err := auth.LoadAuthData()
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("not logged in")
			}
			return fmt.Errorf("load active auth data: %w", err)
		}
		kubeconfig, err := auth.ActiveKubeconfig()
		if err != nil {
			return fmt.Errorf("load active kubeconfig: %w", err)
		}
		name, err := auth.SaveProfile(args[0], *authData, kubeconfig)
		if err != nil {
			return err
		}
		if err := auth.SetCurrentProfileName(name); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Saved and activated profile %s.\n", name)
		return nil
	},
}

var profileUseCmd = &cobra.Command{
	Use:          "use [name]",
	Short:        "Switch to a saved login profile",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := auth.ActivateProfile(args[0]); err != nil {
			return err
		}
		name, _ := auth.ValidateProfileName(args[0])
		fmt.Fprintf(cmd.OutOrStdout(), "Activated profile %s.\n", name)
		return nil
	},
}

var profileDeleteCmd = &cobra.Command{
	Use:          "delete [name]",
	Short:        "Delete a saved login profile",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, err := auth.ValidateProfileName(args[0])
		if err != nil {
			return err
		}
		current, err := auth.CurrentProfileName()
		if err != nil {
			return err
		}
		if err := auth.DeleteProfile(name); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Deleted profile %s.\n", name)
		if current == name {
			fmt.Fprintln(cmd.OutOrStdout(), "Active auth credentials were kept; run `sealtun logout` to remove them.")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileCurrentCmd)
	profileCmd.AddCommand(profileSaveCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileCmd.AddCommand(profileDeleteCmd)
	profileCmd.PersistentFlags().BoolVar(&profileJSON, "json", false, "Output profile details as JSON")
}

func collectProfileItems() ([]profileItem, error) {
	profiles, err := auth.ListProfiles()
	if err != nil {
		return nil, err
	}
	items := make([]profileItem, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, profileItem{
			Name:              profile.Name,
			Current:           profile.Current,
			Region:            profile.Region,
			SealosDomain:      profile.SealosDomain,
			WorkspaceID:       profile.WorkspaceID,
			WorkspaceName:     profile.WorkspaceName,
			AuthenticatedAt:   formatAuthTime(profile.AuthenticatedAt),
			KubeconfigPresent: profile.KubeconfigPresent,
			Error:             profile.Error,
		})
	}
	return items, nil
}

func printProfileList(cmd *cobra.Command, items []profileItem) {
	out := cmd.OutOrStdout()
	if len(items) == 0 {
		fmt.Fprintln(out, "No saved Sealtun profiles found.")
		return
	}

	fmt.Fprintln(out, "Sealtun Profiles")
	fmt.Fprintln(out, "")

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCURRENT\tSTATUS\tREGION\tINGRESS DOMAIN\tWORKSPACE\tAUTHENTICATED AT")
	for _, item := range items {
		status := "ok"
		if item.Error != "" {
			status = "broken"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Name,
			yesNo(item.Current),
			status,
			valueOr(item.Region, "-"),
			valueOr(item.SealosDomain, "-"),
			valueOr(workspaceLabel(item.WorkspaceID, item.WorkspaceName), "-"),
			valueOr(item.AuthenticatedAt, "-"),
		)
	}
	_ = w.Flush()
	for _, item := range items {
		if item.Error != "" {
			fmt.Fprintf(out, "[!] Profile %s is broken: %s\n", item.Name, item.Error)
		}
	}
}
