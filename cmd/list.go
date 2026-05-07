package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type listItem struct {
	TunnelID     string `json:"tunnelId"`
	Status       string `json:"status"`
	Host         string `json:"host"`
	SealosHost   string `json:"sealosHost,omitempty"`
	CustomDomain string `json:"customDomain,omitempty"`
	LocalPort    string `json:"localPort"`
	PID          int    `json:"pid"`
	Mode         string `json:"mode"`
	Namespace    string `json:"namespace"`
	Protocol     string `json:"protocol"`
	CreatedAt    string `json:"createdAt"`
}

var listJSON bool
var listCheck bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List local Sealtun tunnel sessions",
	Long: `List local Sealtun tunnel sessions tracked on this machine.
By default this command only reads local session records. Use --check to probe
local target ports and mark unreachable running tunnels as degraded.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := collectListItems()
		if err != nil {
			return err
		}

		if listJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(items)
		}

		printListTable(cmd, items)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output tunnel sessions as JSON")
	listCmd.Flags().BoolVar(&listCheck, "check", false, "Probe local target ports and report degraded sessions")
}

func collectListItems() ([]listItem, error) {
	return collectListItemsWithLocalCheck(listCheck)
}

func collectListItemsWithLocalCheck(checkLocalPort bool) ([]listItem, error) {
	sessions, err := session.List()
	if err != nil {
		return nil, fmt.Errorf("load tunnel sessions: %w", err)
	}

	items := make([]listItem, 0, len(sessions))
	for _, sess := range sessions {
		snapshot := classifySession(sess, checkLocalPort)
		items = append(items, listItem{
			TunnelID:     sess.TunnelID,
			Status:       snapshot.Status,
			Host:         valueOr(sess.Host, "-"),
			SealosHost:   sess.SealosHost,
			CustomDomain: sess.CustomDomain,
			LocalPort:    valueOr(sess.LocalPort, "-"),
			PID:          sess.PID,
			Mode:         valueOr(sess.Mode, "foreground"),
			Namespace:    valueOr(sess.Namespace, "-"),
			Protocol:     valueOr(sess.Protocol, "-"),
			CreatedAt:    formatAuthTime(sess.CreatedAt),
		})
	}

	return items, nil
}

func printListTable(cmd *cobra.Command, items []listItem) {
	out := cmd.OutOrStdout()
	if len(items) == 0 {
		fmt.Fprintln(out, "No local Sealtun tunnel sessions found.")
		return
	}

	fmt.Fprintln(out, "Sealtun Tunnels")
	fmt.Fprintln(out, "  Source: local session records")
	fmt.Fprintln(out, "")

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TUNNEL ID\tSTATUS\tHOST\tPORT\tPID\tMODE\tNAMESPACE\tCREATED AT")
	for _, item := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			item.TunnelID,
			item.Status,
			item.Host,
			item.LocalPort,
			item.PID,
			item.Mode,
			item.Namespace,
			item.CreatedAt,
		)
	}
	_ = w.Flush()
}
