package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/labring/sealtun/pkg/session"
	"github.com/spf13/cobra"
)

type listItem struct {
	TunnelID                    string `json:"tunnelId"`
	Status                      string `json:"status"`
	Host                        string `json:"host"`
	SealosHost                  string `json:"sealosHost,omitempty"`
	CustomDomain                string `json:"customDomain,omitempty"`
	PublicPort                  int32  `json:"publicPort,omitempty"`
	LocalPort                   string `json:"localPort"`
	TargetURL                   string `json:"targetUrl"`
	TargetTLSInsecureSkipVerify bool   `json:"targetTlsInsecureSkipVerify,omitempty"`
	PID                         int    `json:"pid"`
	Mode                        string `json:"mode"`
	Namespace                   string `json:"namespace"`
	Protocol                    string `json:"protocol"`
	Endpoint                    string `json:"endpoint"`
	BasicAuth                   bool   `json:"basicAuth"`
	AccessPolicy                bool   `json:"accessPolicy"`
	TTL                         string `json:"ttl,omitempty"`
	ExpiresAt                   string `json:"expiresAt,omitempty"`
	CreatedAt                   string `json:"createdAt"`
}

var listJSON bool
var listCheck bool

const (
	listRemoteRefreshConcurrency = 4
	listRemoteRefreshTimeout     = 10 * time.Second
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List local Sealtun tunnel sessions",
	Long: `List local Sealtun tunnel sessions tracked on this machine.
By default this command only reads local session records. Use --check to probe
local target ports and mark unreachable running tunnels as degraded.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		items, err := collectListItemsWithContext(cmd.Context(), listCheck)
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
	return collectListItemsWithContext(context.Background(), listCheck)
}

func collectListItemsWithLocalCheck(checkLocalPort bool) ([]listItem, error) {
	return collectListItemsWithContext(context.Background(), checkLocalPort)
}

func collectListItemsWithContext(ctx context.Context, checkLocalPort bool) ([]listItem, error) {
	sessions, err := session.List()
	if err != nil {
		return nil, fmt.Errorf("load tunnel sessions: %w", err)
	}
	if err := refreshSessionsFromRemote(ctx, sessions, true); err != nil {
		return nil, err
	}
	return listItemsFromSessions(sessions, checkLocalPort), nil
}

func refreshSessionsFromRemote(ctx context.Context, sessions []session.TunnelSession, ensurePublicPort bool) error {
	if len(sessions) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workerCount := listRemoteRefreshConcurrency
	if workerCount > len(sessions) {
		workerCount = len(sessions)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				refreshCtx, cancel := context.WithTimeout(ctx, listRemoteRefreshTimeout)
				refreshSessionFromRemote(refreshCtx, &sessions[index])
				if ensurePublicPort {
					ensureSessionPublicPort(refreshCtx, &sessions[index])
				}
				cancel()
			}
		}()
	}
	for index := range sessions {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

func listItemsFromSessions(sessions []session.TunnelSession, checkLocalPort bool) []listItem {
	items := make([]listItem, 0, len(sessions))
	for _, sess := range sessions {
		snapshot := classifySession(sess, checkLocalPort)
		items = append(items, listItem{
			TunnelID:                    sess.TunnelID,
			Status:                      snapshot.Status,
			Host:                        valueOr(sess.Host, "-"),
			SealosHost:                  sess.SealosHost,
			CustomDomain:                sess.CustomDomain,
			PublicPort:                  sess.PublicPort,
			LocalPort:                   valueOr(sess.LocalPort, "-"),
			TargetURL:                   sessionTargetLabel(sess),
			TargetTLSInsecureSkipVerify: targetTLSInsecureSkipVerifyEnabled(sess.TargetTLS),
			PID:                         sess.PID,
			Mode:                        valueOr(sess.Mode, "foreground"),
			Namespace:                   valueOr(sess.Namespace, "-"),
			Protocol:                    valueOr(sess.Protocol, "-"),
			Endpoint:                    endpointLabel(sess.Protocol, sess.Host, sess.SealosHost, sess.PublicPort),
			BasicAuth:                   sess.BasicAuth != nil && sess.BasicAuth.Enabled,
			AccessPolicy:                sess.AccessPolicy != nil,
			TTL:                         sess.TTL,
			ExpiresAt:                   sess.ExpiresAt,
			CreatedAt:                   formatAuthTime(sess.CreatedAt),
		})
	}

	return items
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
	fmt.Fprintln(w, "TUNNEL ID\tSTATUS\tPROTOCOL\tENDPOINT\tTARGET\tAUTH\tACCESS\tPID\tMODE\tNAMESPACE\tEXPIRES AT\tCREATED AT")
	for _, item := range items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			item.TunnelID,
			item.Status,
			item.Protocol,
			item.Endpoint,
			item.TargetURL,
			yesNo(item.BasicAuth),
			yesNo(item.AccessPolicy),
			item.PID,
			item.Mode,
			item.Namespace,
			valueOr(formatAuthTime(item.ExpiresAt), "-"),
			item.CreatedAt,
		)
	}
	_ = w.Flush()
}
