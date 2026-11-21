package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/pkg/sdk"
)

// Pipeline CLI: gate -> agent edges, admission and audited overrides
// (ADR 0002).
func pipelineCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pipeline", Short: "Inspect gate -> agent pipelines, admission and overrides"}

	var agent string
	list := &cobra.Command{
		Use:   "list",
		Short: "List pipeline edges (derived from the 2D graph)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			st, err := client().ListPipelines(context.Background(), agent)
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(st)
				return nil
			}
			fmt.Printf("revision %d, %d edge(s)\n", st.Revision, len(st.Edges))
			for _, e := range st.Edges {
				fmt.Printf("  %-16s %s -> %s  on_true=%s on_false=%s  %s\n",
					e.GateID, orDash(e.UpstreamAgentID), e.DownstreamAgentID, e.OnTrue, e.OnFalse, e.Workspace)
			}
			return nil
		},
	}
	list.Flags().StringVar(&agent, "agent", "", "only edges into this agent (id or name)")

	status := &cobra.Command{
		Use:     "status [AGENT]",
		Aliases: []string{"admission"},
		Short:   "Show whether an agent may take pipeline work (all gated agents without AGENT)",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			if len(args) == 0 {
				rev, all, err := client().PipelineAdmissions(ctx)
				if err != nil {
					return err
				}
				if jsonOut {
					printJSON(map[string]any{"revision": rev, "admissions": all})
					return nil
				}
				fmt.Printf("revision %d\n", rev)
				for _, a := range all {
					printAdmission(a)
				}
				return nil
			}
			adm, err := client().PipelineAdmission(ctx, args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(adm)
			} else {
				printAdmission(adm)
			}
			if !adm.Allowed {
				os.Exit(3) // scripts: blocked
			}
			return nil
		},
	}

	var gate, reason string
	var ttl time.Duration
	var multi bool
	override := &cobra.Command{
		Use:   "override AGENT --gate ID --reason TEXT",
		Short: "Allow a blocked agent despite a failed, missing or stale gate (admin, audited; never for tamper or a deliberate deny)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if strings.TrimSpace(gate) == "" || strings.TrimSpace(reason) == "" {
				return fmt.Errorf("--gate and --reason are required")
			}
			single := !multi
			o, err := client().CreatePipelineOverride(context.Background(), sdk.OverrideRequest{
				Agent: args[0], GateID: gate, Reason: reason, TTLSec: int(ttl.Seconds()), SingleUse: &single,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(o)
				return nil
			}
			use := "single use"
			if !o.SingleUse {
				use = "reusable"
			}
			fmt.Printf("override %s: agent %s gate %s until %s (%s)\n", o.ID, o.AgentID, o.GateID, o.ExpiresAt.Local().Format(time.RFC3339), use)
			return nil
		},
	}
	override.Flags().StringVar(&gate, "gate", "", "gate id to override (* = every gate of the agent)")
	override.Flags().StringVar(&reason, "reason", "", "mandatory justification (audited)")
	override.Flags().DurationVar(&ttl, "ttl", 15*time.Minute, "validity (max 24h)")
	override.Flags().BoolVar(&multi, "reusable", false, "allow multiple uses until it expires")

	var active bool
	overrides := &cobra.Command{
		Use:   "overrides [AGENT]",
		Short: "List pipeline overrides",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			a := ""
			if len(args) == 1 {
				a = args[0]
			}
			list, err := client().ListPipelineOverrides(context.Background(), a, active)
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(list)
				return nil
			}
			for _, o := range list {
				used := "unused"
				if o.UsedAt != nil {
					used = "used " + o.UsedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Printf("%-24s %-10s %-12s %-24s %s  %s\n", o.ID, o.AgentID, o.GateID, used, o.Actor, o.Reason)
			}
			return nil
		},
	}
	overrides.Flags().BoolVar(&active, "active", false, "only overrides that can still be used")

	cmd.AddCommand(list, status, override, overrides)
	return cmd
}

func printAdmission(a *model.Admission) {
	verdict := "ALLOWED"
	if !a.Allowed {
		verdict = "BLOCKED"
	}
	name := a.Agent
	if name == "" {
		name = a.AgentID
	}
	fmt.Printf("%s  %s  action=%s\n", name, verdict, a.Action)
	for _, e := range a.Edges {
		mark := "ok "
		if !e.Satisfied {
			mark = "-- "
		}
		fmt.Printf("  %s%-16s %-24s run=%s  %s\n", mark, orDash(e.GateID), e.Code, orDash(e.RunID), e.Message)
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
