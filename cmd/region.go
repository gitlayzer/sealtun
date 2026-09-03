package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/labring/sealtun/pkg/auth"
	"github.com/spf13/cobra"
)

type regionListItem struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	SealosDomain string `json:"sealosDomain,omitempty"`
	Default      bool   `json:"default"`
	Current      bool   `json:"current"`
	Reachable    bool   `json:"reachable,omitempty"`
}

var regionJSON bool
var regionCurrentJSON bool

var regionCmd = &cobra.Command{
	Use:   "region",
	Short: "Manage Sealos Cloud regions",
}

var regionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known Sealos Cloud regions",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := collectRegionListItems()
		if err != nil {
			return err
		}
		if regionJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "Sealos Regions")
		for _, item := range items {
			flags := ""
			if item.Default {
				flags += " default"
			}
			if item.Current {
				flags += " current"
			}
			if flags != "" {
				flags = " (" + flags[1:] + ")"
			}
			fmt.Fprintf(out, "  - %s%s\n    region: %s\n    ingress domain: %s\n", item.Name, flags, item.URL, valueOr(item.SealosDomain, "unknown"))
		}
		return nil
	},
}

var regionCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current Sealos Cloud region",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		authData, err := loadActiveAuthDataReadOnly()
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("not logged in")
			}
			return fmt.Errorf("failed to load auth data: %w", err)
		}
		if regionCurrentJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(struct {
				Region       string `json:"region"`
				SealosDomain string `json:"sealosDomain,omitempty"`
			}{
				Region:       authData.Region,
				SealosDomain: authData.SealosDomain,
			})
		}
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, authData.Region)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(regionCmd)
	regionCmd.AddCommand(regionListCmd)
	regionCmd.AddCommand(regionCurrentCmd)
	regionListCmd.Flags().BoolVar(&regionJSON, "json", false, "Output regions as JSON")
	regionCurrentCmd.Flags().BoolVar(&regionCurrentJSON, "json", false, "Output the current region as JSON")
}

func collectRegionListItems() ([]regionListItem, error) {
	current := ""
	currentSealosDomain := ""
	if authData, err := loadActiveAuthDataReadOnly(); err == nil {
		current = authData.Region
		currentSealosDomain = authData.SealosDomain
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load auth data: %w", err)
	}
	items := regionListItemsForCurrent(current)
	for i := range items {
		if items[i].Current && currentSealosDomain != "" {
			items[i].SealosDomain = currentSealosDomain
		}
	}
	return items, nil
}

func regionListItemsForCurrent(current string) []regionListItem {
	regions := auth.KnownRegions()
	items := make([]regionListItem, 0, len(regions))
	for _, region := range regions {
		items = append(items, regionListItem{
			Name:         region.Name,
			URL:          region.URL,
			SealosDomain: region.SealosDomain,
			Default:      region.URL == auth.DefaultRegion,
			Current:      region.URL == current,
		})
	}
	return items
}

func loadActiveAuthDataReadOnly() (*auth.AuthData, error) {
	root, err := auth.CurrentSealtunDir()
	if err != nil {
		return nil, err
	}
	authData, err := auth.LoadAuthDataFromDir(root)
	if err != nil {
		return nil, err
	}
	return authData, nil
}
