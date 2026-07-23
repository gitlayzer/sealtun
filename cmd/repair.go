package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

type repairPayload struct {
	DryRun  bool              `json:"dryRun"`
	Plan    *doctorFixPayload `json:"plan"`
	Results *doctorFixPayload `json:"results,omitempty"`
}

var repairJSON bool
var repairDryRun bool

var repairCmd = &cobra.Command{
	Use:          "repair [tunnel-id]",
	Short:        "Plan and run conservative repairs for one Sealtun tunnel",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		payload, err := runRepair(cmd.Context(), args[0], repairDryRun)
		if err != nil {
			return err
		}
		if repairJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(payload); err != nil {
				return err
			}
			if payload.Results != nil {
				return doctorFixExecutionError(payload.Results)
			}
			return nil
		}
		printRepair(cmd, payload)
		if payload.Results != nil {
			return doctorFixExecutionError(payload.Results)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(repairCmd)
	markDeprecated(repairCmd, "sealtun doctor <tunnel-id> --fix")
	repairCmd.Flags().BoolVar(&repairJSON, "json", false, "Output repair plan/results as JSON")
	repairCmd.Flags().BoolVar(&repairDryRun, "dry-run", false, "Show conservative repair actions without executing them")
}

func runRepair(ctx context.Context, tunnelID string, dryRun bool) (*repairPayload, error) {
	plan, err := runDoctorFix(ctx, []string{tunnelID}, true)
	if err != nil {
		return nil, err
	}
	payload := &repairPayload{DryRun: dryRun, Plan: plan}
	if dryRun {
		return payload, nil
	}
	results, err := runDoctorFix(ctx, []string{tunnelID}, false)
	if err != nil {
		return nil, err
	}
	payload.Results = results
	return payload, nil
}

func printRepair(cmd *cobra.Command, payload *repairPayload) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Sealtun Repair")
	if payload == nil || payload.Plan == nil || len(payload.Plan.Actions) == 0 {
		fmt.Fprintln(out, "  No conservative repair actions are available.")
		return
	}
	fmt.Fprintln(out, "")
	printDoctorFix(cmd, payload.Plan)
	if payload.DryRun {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Dry run only. Re-run without --dry-run to execute allowed actions.")
		return
	}
	fmt.Fprintln(out, "")
	if payload.Results == nil {
		fmt.Fprintln(out, "No repair results were produced.")
		return
	}
	printDoctorFix(cmd, payload.Results)
}
