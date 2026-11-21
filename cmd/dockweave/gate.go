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
	var issueID string
	run := &cobra.Command{
		Use:   "run WORKSPACE",
		Short: "Run <workspace>/.paperclip/gates.json in a sandbox container",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out model.GateRun
			err := client().Do(context.Background(), "POST", "/api/v1/gates/run", map[string]any{
				"workspace": args[0], "issue_id": issueID, "image": image, "timeout_sec": timeoutSec,
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
	run.Flags().StringVar(&issueID, "issue", "", "Paperclip issue identifier to link the run to")

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

	var verdict string
	var reviewNote string
	var reviewer string
	review := &cobra.Command{
		Use:   "review ISSUE",
		Short: "Record a review verdict (PASS|FAIL) for an issue, idempotently",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out model.GateReview
			if err := client().Do(context.Background(), "POST", "/api/v1/gates/reviews", map[string]any{
				"issue_id": args[0], "verdict": verdict, "note": reviewNote,
				"assigned_reviewer": reviewer,
			}, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			fmt.Printf("%s %s (evidence %s)\n", out.IssueID, out.Verdict, out.EvidenceID)
			return nil
		},
	}
	review.Flags().StringVar(&verdict, "verdict", "", "PASS or FAIL")
	review.Flags().StringVar(&reviewNote, "note", "", "review note")
	review.Flags().StringVar(&reviewer, "assignee", "", "identity the review issue is assigned to in Paperclip (stored as unverified metadata)")
	_ = review.MarkFlagRequired("verdict")

	var reviewLimit int
	reviews := &cobra.Command{
		Use:   "reviews",
		Short: "List recorded review verdicts",
		RunE: func(_ *cobra.Command, _ []string) error {
			var out struct {
				Reviews []model.GateReview `json:"reviews"`
			}
			if err := client().Do(context.Background(), "GET", fmt.Sprintf("/api/v1/gates/reviews?limit=%d", reviewLimit), nil, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			for _, r := range out.Reviews {
				fmt.Printf("%-16s %-4s %-12s %s\n", r.IssueID, r.Verdict, r.Actor, r.RecordedAt.Format("2006-01-02 15:04"))
			}
			return nil
		},
	}
	reviews.Flags().IntVar(&reviewLimit, "limit", 20, "maximum number of reviews")

	var pruneDays int
	var pruneKeep int
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete old gate runs (retention)",
		RunE: func(_ *cobra.Command, _ []string) error {
			var out struct {
				Pruned int64 `json:"pruned"`
			}
			if err := client().Do(context.Background(), "POST", "/api/v1/gates/prune", map[string]any{
				"days": pruneDays, "keep_per_workspace": pruneKeep,
			}, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			fmt.Printf("pruned %d gate run(s)\n", out.Pruned)
			return nil
		},
	}
	prune.Flags().IntVar(&pruneDays, "days", 30, "delete runs older than this many days")
	prune.Flags().IntVar(&pruneKeep, "keep", 20, "keep this many runs per workspace")

	cmd.AddCommand(run, runs, approve, approval, review, reviews, prune)
	return cmd
}
