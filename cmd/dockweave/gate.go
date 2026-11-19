package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/model"
)

// Gate CLI: sandboxed gate execution and DockWeave-side human approvals.
func gateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "gate", Short: "Run gates in a sandbox and manage approvals"}

	var image string
	var timeoutSec int
	run := &cobra.Command{
		Use:   "run WORKSPACE",
		Short: "Run <workspace>/.paperclip/gates.json in a sandbox container",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out model.GateRun
			err := client().Do(context.Background(), "POST", "/api/v1/gates/run", map[string]any{
				"workspace": args[0], "image": image, "timeout_sec": timeoutSec,
			}, &out)
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			fmt.Printf("%s  %s  %d/%d gates  %dms", out.Status, out.Image, out.Passed, out.Gates, out.DurationMS)
			if out.Tamper {
				fmt.Print("  TAMPER")
			}
			if out.Reason != "" {
				fmt.Printf("  — %s", out.Reason)
			}
			fmt.Println()
			for _, r := range out.Results {
				fmt.Printf("  %-16s %-6s exit=%d %dms\n", r.ID, r.Status, r.Exit, r.DurationMS)
			}
			return nil
		},
	}
	run.Flags().StringVar(&image, "image", "", "gate runner image override")
	run.Flags().IntVar(&timeoutSec, "timeout", 900, "global timeout in seconds")

	var limit int
	runs := &cobra.Command{
		Use:   "runs [WORKSPACE]",
		Short: "List recent gate runs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			workspace := ""
			if len(args) == 1 {
				workspace = args[0]
			}
			var out struct {
				Runs []model.GateRun `json:"runs"`
			}
			path := fmt.Sprintf("/api/v1/gates/runs?limit=%d", limit)
			if workspace != "" {
				path += "&workspace=" + workspace
			}
			if err := client().Do(context.Background(), "GET", path, nil, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			for _, r := range out.Runs {
				fmt.Printf("%-12s %-7s %2d/%-2d %6dms  %s\n", r.Status, r.Workspace, r.Passed, r.Gates, r.DurationMS, r.StartedAt.Format("2006-01-02 15:04"))
				if r.Reason != "" {
					fmt.Printf("             %s\n", r.Reason)
				}
			}
			return nil
		},
	}
	runs.Flags().IntVar(&limit, "limit", 20, "maximum number of runs")

	var note string
	approve := &cobra.Command{
		Use:   "approve ISSUE",
		Short: "Record a DockWeave-side human approval (admin only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out model.GateApproval
			if err := client().Do(context.Background(), "POST", "/api/v1/gates/approvals", map[string]any{
				"issue_id": args[0], "note": note,
			}, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			fmt.Printf("approved %s by %s (%s)\n", out.IssueID, out.Actor, out.Role)
			return nil
		},
	}
	approve.Flags().StringVar(&note, "note", "", "approval note")

	approval := &cobra.Command{
		Use:   "approval ISSUE",
		Short: "Show the DockWeave-side approval for an issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out model.GateApproval
			err := client().Do(context.Background(), "GET", "/api/v1/gates/approvals/"+strings.TrimSpace(args[0]), nil, &out)
			if err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	}

	cmd.AddCommand(run, runs, approve, approval)
	return cmd
}
