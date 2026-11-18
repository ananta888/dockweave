package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/model"
)

// Productivity CLI: goal hierarchy, daily queue, focus mode, evidence, budgets
// and reviews. Everything is deterministic; no model calls happen here.

func objectiveCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "objective", Short: "Manage strategic objectives"}
	add := &cobra.Command{
		Use:   "add TITLE",
		Short: "Create an objective (vision|year|quarter|month|week|project|operative)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, _ := cmd.Flags().GetString("kind")
			parent, _ := cmd.Flags().GetString("parent")
			start, _ := cmd.Flags().GetString("start")
			end, _ := cmd.Flags().GetString("end")
			desc, _ := cmd.Flags().GetString("description")
			o := model.Objective{
				Kind: model.ObjectiveKind(kind), ParentID: parent,
				Title: strings.Join(args, " "), Description: desc,
				PeriodStart: start, PeriodEnd: end, Status: model.ObjectiveActive,
			}
			var out model.Objective
			if err := client().Do(context.Background(), "POST", "/api/v1/objectives", o, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			fmt.Printf("%s  %s (%s)\n", out.ID, out.Title, out.Kind)
			return nil
		},
	}
	add.Flags().String("kind", string(model.ObjectiveQuarter), "objective kind")
	add.Flags().String("parent", "", "parent objective id")
	add.Flags().String("start", "", "period start YYYY-MM-DD")
	add.Flags().String("end", "", "period end YYYY-MM-DD")
	add.Flags().String("description", "", "description")

	list := &cobra.Command{
		Use: "list", Short: "List objectives",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out []*model.Objective
			if err := client().Do(context.Background(), "GET", "/api/v1/objectives", nil, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			for _, o := range out {
				fmt.Printf("%-22s %-10s %-9s %s\n", o.ID, o.Kind, o.Status, o.Title)
			}
			return nil
		},
	}
	cmd.AddCommand(add, list)
	return cmd
}

func taskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Manage daily tasks and agent orders"}
	add := &cobra.Command{
		Use:   "add TITLE",
		Short: "Add a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			day, _ := cmd.Flags().GetString("day")
			kind, _ := cmd.Flags().GetString("assignee")
			agent, _ := cmd.Flags().GetString("agent")
			priority, _ := cmd.Flags().GetInt("priority")
			effort, _ := cmd.Flags().GetInt("effort")
			desc, _ := cmd.Flags().GetString("description")
			objectives, _ := cmd.Flags().GetStringSlice("objective")
			if day == "today" {
				day = time.Now().Format("2006-01-02")
			}
			t := model.Task{
				Title: strings.Join(args, " "), Description: desc,
				AssigneeKind: model.AssigneeKind(kind), AgentID: agent,
				Day: day, Status: model.TaskTodo, Priority: priority,
				EffortMinutes: effort, ObjectiveIDs: objectives,
			}
			var out model.Task
			if err := client().Do(context.Background(), "POST", "/api/v1/tasks", t, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			fmt.Printf("%s  %s\n", out.ID, out.Title)
			return nil
		},
	}
	add.Flags().String("day", "today", "day (YYYY-MM-DD or today)")
	add.Flags().String("assignee", "human", "human|agent")
	add.Flags().String("agent", "", "agent id for agent tasks")
	add.Flags().Int("priority", 50, "priority 0..100")
	add.Flags().Int("effort", 0, "estimated effort in minutes")
	add.Flags().String("description", "", "description")
	add.Flags().StringSlice("objective", nil, "objective id (repeatable)")

	list := &cobra.Command{
		Use: "list", Short: "List tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			day, _ := cmd.Flags().GetString("day")
			status, _ := cmd.Flags().GetString("status")
			if day == "today" {
				day = time.Now().Format("2006-01-02")
			}
			path := "/api/v1/tasks?day=" + day + "&status=" + status
			var out []*model.Task
			if err := client().Do(context.Background(), "GET", path, nil, &out); err != nil {
				return err
			}
			if jsonOut {
				printJSON(out)
				return nil
			}
			for _, t := range out {
				flag := " "
				if t.AssigneeKind == model.AssigneeAgent {
					flag = "A"
				}
				fmt.Printf("%s %-12s %-11s p%-3d %s\n", flag, t.Status, t.ID, t.Priority, t.Title)
			}
			return nil
		},
	}
	list.Flags().String("day", "today", "day filter (empty = all)")
	list.Flags().String("status", "", "status filter")

	move := &cobra.Command{
		Use: "move ID DAY", Short: "Move a task to another day", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var t model.Task
			if err := client().Do(context.Background(), "GET", "/api/v1/tasks/"+args[0], nil, &t); err != nil {
				return err
			}
			t.Day = args[1]
			if t.Day == "today" {
				t.Day = time.Now().Format("2006-01-02")
			}
			if err := client().Do(context.Background(), "PATCH", "/api/v1/tasks/"+args[0], t, &t); err != nil {
				return err
			}
			fmt.Printf("%s -> %s\n", t.ID, t.Day)
			return nil
		},
	}

	done := &cobra.Command{
		Use: "done ID", Short: "Mark a task done", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var t model.Task
			if err := client().Do(context.Background(), "GET", "/api/v1/tasks/"+args[0], nil, &t); err != nil {
				return err
			}
			t.Status = model.TaskDone
			if err := client().Do(context.Background(), "PATCH", "/api/v1/tasks/"+args[0], t, &t); err != nil {
				return err
			}
			fmt.Printf("%s done\n", t.ID)
			return nil
		},
	}
	cmd.AddCommand(add, list, move, done)
	return cmd
}

func focusCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "focus", Short: "Single-task focus mode"}
	start := &cobra.Command{
		Use: "start TASK", Short: "Start a focus session", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, _ := cmd.Flags().GetString("human")
			var f model.FocusSession
			body := map[string]string{"task_id": args[0], "human": h}
			if err := client().Do(context.Background(), "POST", "/api/v1/focus/start", body, &f); err != nil {
				return err
			}
			fmt.Printf("focus %s on %s since %s\n", f.ID, f.TaskID, f.StartedAt.Format(time.RFC3339))
			return nil
		},
	}
	start.Flags().String("human", "local", "human identity")

	status := &cobra.Command{
		Use: "status", Short: "Show the current focus session",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h, _ := cmd.Flags().GetString("human")
			var f model.FocusSession
			if err := client().Do(context.Background(), "GET", "/api/v1/focus?human="+h, nil, &f); err != nil {
				return err
			}
			var t model.Task
			_ = client().Do(context.Background(), "GET", "/api/v1/tasks/"+f.TaskID, nil, &t)
			elapsed := f.Elapsed(time.Now())
			fmt.Printf("%s  %s  %s (%s)\n", f.Status, elapsed.Round(time.Second), t.Title, t.ID)
			return nil
		},
	}
	status.Flags().String("human", "local", "human identity")

	action := func(use, path string, complete bool) *cobra.Command {
		c := &cobra.Command{
			Use: use, Short: use + " the current focus session",
			RunE: func(cmd *cobra.Command, _ []string) error {
				h, _ := cmd.Flags().GetString("human")
				notes, _ := cmd.Flags().GetString("notes")
				var f model.FocusSession
				body := map[string]string{"notes": notes}
				if err := client().Do(context.Background(), "POST", path+"?human="+h, body, &f); err != nil {
					return err
				}
				fmt.Printf("%s %s after %s\n", f.TaskID, f.Status, f.Elapsed(time.Now()).Round(time.Second))
				return nil
			},
		}
		c.Flags().String("human", "local", "human identity")
		c.Flags().String("notes", "", "session notes")
		return c
	}
	cmd.AddCommand(start, status,
		action("pause", "/api/v1/focus/pause", false),
		action("resume", "/api/v1/focus/resume", false),
		action("done", "/api/v1/focus/complete", true),
		action("skip", "/api/v1/focus/skip", false))

	cmd.PersistentFlags().String("human", "local", "human identity")
	return cmd
}

func planCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "plan", Short: "Show the daily queue and current focus",
		RunE: func(cmd *cobra.Command, _ []string) error {
			day, _ := cmd.Flags().GetString("day")
			h, _ := cmd.Flags().GetString("human")
			if day == "today" {
				day = time.Now().Format("2006-01-02")
			}
			var p model.TodayPlan
			if err := client().Do(context.Background(), "GET", "/api/v1/tasks/today?day="+day+"&human="+h, nil, &p); err != nil {
				return err
			}
			if jsonOut {
				printJSON(p)
				return nil
			}
			fmt.Printf("Plan %s  (agents active: %d)\n", p.Day, p.ActiveAgent)
			if p.Focus != nil {
				fmt.Printf("FOCUS %s since %s\n", p.Focus.TaskID, p.Focus.StartedAt.Format("15:04"))
			}
			for _, t := range p.Tasks {
				mark := " "
				if p.Focus != nil && t.ID == p.Focus.TaskID {
					mark = "*"
				}
				fmt.Printf("%s %-12s p%-3d %s\n", mark, t.Status, t.Priority, t.Title)
			}
			return nil
		},
	}
	cmd.Flags().String("day", "today", "day")
	cmd.Flags().String("human", "local", "human identity")
	return cmd
}

func evidenceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "evidence", Short: "Record verifiable work results"}
	add := &cobra.Command{
		Use: "add TASK", Short: "Attach evidence (commit|test|build|review|...)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, _ := cmd.Flags().GetString("kind")
			status, _ := cmd.Flags().GetString("status")
			ref, _ := cmd.Flags().GetString("ref")
			verified, _ := cmd.Flags().GetBool("verified")
			key, _ := cmd.Flags().GetString("idempotency-key")
			e := model.Evidence{
				TaskID: args[0], Kind: model.EvidenceKind(kind),
				Status: model.EvidenceStatus(status), Ref: ref,
				Verified: verified, IdempotencyKey: key,
			}
			var out map[string]any
			if err := client().Do(context.Background(), "POST", "/api/v1/evidence", e, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	}
	add.Flags().String("kind", "test", "evidence kind")
	add.Flags().String("status", "passed", "passed|failed|unknown")
	add.Flags().String("ref", "", "reference (sha, URL, path)")
	add.Flags().Bool("verified", true, "deterministically verified")
	add.Flags().String("idempotency-key", "", "dedupe key")

	list := &cobra.Command{
		Use: "list TASK", Short: "List evidence of a task", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out []*model.Evidence
			if err := client().Do(context.Background(), "GET", "/api/v1/tasks/"+args[0]+"/evidence", nil, &out); err != nil {
				return err
			}
			for _, e := range out {
				fmt.Printf("%-10s %-7s verified=%-5t %s\n", e.Kind, e.Status, e.Verified, e.Ref)
			}
			return nil
		},
	}
	cmd.AddCommand(add, list)
	return cmd
}

func budgetCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "budget", Short: "Input budgets per scope"}
	set := &cobra.Command{
		Use: "set", Short: "Set an input budget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			kind, _ := cmd.Flags().GetString("kind")
			period, _ := cmd.Flags().GetString("period")
			target, _ := cmd.Flags().GetFloat64("target")
			b := model.InputBudget{Scope: scope, ScopeID: scopeID,
				Kind: model.BudgetKind(kind), Period: period, Target: target}
			var out model.InputBudget
			if err := client().Do(context.Background(), "POST", "/api/v1/budgets", b, &out); err != nil {
				return err
			}
			fmt.Printf("%s %s %s=%g/%s\n", out.Scope, out.ScopeID, out.Kind, out.Target, out.Period)
			return nil
		},
	}
	set.Flags().String("scope", "global", "objective|workspace|agent|global")
	set.Flags().String("scope-id", "", "id of the scope")
	set.Flags().String("kind", "human_hours", "human_hours|agent_hours|tokens|cost_usd")
	set.Flags().String("period", "weekly", "weekly|monthly|total")
	set.Flags().Float64("target", 0, "target value")

	list := &cobra.Command{
		Use: "list", Short: "List budgets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out []*model.InputBudget
			if err := client().Do(context.Background(), "GET", "/api/v1/budgets", nil, &out); err != nil {
				return err
			}
			for _, b := range out {
				fmt.Printf("%-10s %-22s %-12s %g / %s\n", b.Scope, b.ScopeID, b.Kind, b.Target, b.Period)
			}
			return nil
		},
	}
	actuals := &cobra.Command{
		Use: "actuals", Short: "Show target vs measured values",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out struct {
				Budgets []struct {
					Scope    string  `json:"scope"`
					Kind     string  `json:"kind"`
					Period   string  `json:"period"`
					Target   float64 `json:"target"`
					Actual   float64 `json:"actual"`
					Exceeded bool    `json:"exceeded"`
				} `json:"budgets"`
				Verified int `json:"verified_results"`
			}
			if err := client().Do(context.Background(), "GET", "/api/v1/budgets/actuals", nil, &out); err != nil {
				return err
			}
			for _, b := range out.Budgets {
				over := ""
				if b.Exceeded {
					over = "OVER"
				}
				fmt.Printf("%-8s %-12s %-8s %8.2f / %-8.2f %s\n", b.Scope, b.Kind, b.Period, b.Actual, b.Target, over)
			}
			fmt.Printf("verified results: %d\n", out.Verified)
			return nil
		},
	}
	cmd.AddCommand(set, list, actuals)
	return cmd
}

func reviewCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "review", Short: "Weekly and monthly reflections"}
	add := &cobra.Command{
		Use: "add", Short: "Store a review snapshot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, _ := cmd.Flags().GetString("kind")
			start, _ := cmd.Flags().GetString("start")
			end, _ := cmd.Flags().GetString("end")
			bodyText, _ := cmd.Flags().GetString("body")
			body := map[string]any{}
			if bodyText != "" {
				if err := json.Unmarshal([]byte(bodyText), &body); err != nil {
					return fmt.Errorf("body must be JSON: %w", err)
				}
			}
			rv := model.Review{Kind: model.ReviewKind(kind), PeriodStart: start,
				PeriodEnd: end, Body: body}
			var out model.Review
			if err := client().Do(context.Background(), "POST", "/api/v1/reviews", rv, &out); err != nil {
				return err
			}
			fmt.Printf("%s %s %s..%s\n", out.ID, out.Kind, out.PeriodStart, out.PeriodEnd)
			return nil
		},
	}
	add.Flags().String("kind", "weekly", "weekly|monthly")
	add.Flags().String("start", "", "period start YYYY-MM-DD")
	add.Flags().String("end", "", "period end YYYY-MM-DD")
	add.Flags().String("body", "", "JSON body")

	list := &cobra.Command{
		Use: "list", Short: "List reviews",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out []*model.Review
			if err := client().Do(context.Background(), "GET", "/api/v1/reviews", nil, &out); err != nil {
				return err
			}
			for _, r := range out {
				fmt.Printf("%s %-8s %s..%s\n", r.ID, r.Kind, r.PeriodStart, r.PeriodEnd)
			}
			return nil
		},
	}
	cmd.AddCommand(add, list)
	return cmd
}

func workEventCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "event", Short: "Record and inspect work measurements"}
	add := &cobra.Command{
		Use: "add", Short: "Add a measured event",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, _ := cmd.Flags().GetString("kind")
			task, _ := cmd.Flags().GetString("task")
			agent, _ := cmd.Flags().GetString("agent")
			human, _ := cmd.Flags().GetString("human")
			seconds, _ := cmd.Flags().GetInt64("seconds")
			key, _ := cmd.Flags().GetString("idempotency-key")
			source, _ := cmd.Flags().GetString("source")
			e := model.WorkEvent{
				Kind: model.WorkEventKind(kind), TaskID: task, AgentID: agent,
				HumanID: human, DurationSeconds: seconds, Source: source,
				IdempotencyKey: key, StartedAt: time.Now().UTC(),
			}
			e.EndedAt = func() *time.Time { t := e.StartedAt.Add(time.Duration(seconds) * time.Second); return &t }()
			var out map[string]any
			if err := client().Do(context.Background(), "POST", "/api/v1/work-events", e, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	}
	add.Flags().String("kind", "agent_run", "human_focus|agent_run|container|wait|llm|tool")
	add.Flags().String("task", "", "task id")
	add.Flags().String("agent", "", "agent id")
	add.Flags().String("human", "", "human id")
	add.Flags().Int64("seconds", 0, "duration in seconds")
	add.Flags().String("source", "cli", "measurement source")
	add.Flags().String("idempotency-key", "", "dedupe key")

	list := &cobra.Command{
		Use: "list", Short: "List measured events",
		RunE: func(cmd *cobra.Command, _ []string) error {
			task, _ := cmd.Flags().GetString("task")
			var out []*model.WorkEvent
			if err := client().Do(context.Background(), "GET", "/api/v1/work-events?task="+task, nil, &out); err != nil {
				return err
			}
			for _, e := range out {
				fmt.Printf("%s %-12s %5ds %-8s %s\n", e.StartedAt.Format("01-02 15:04"), e.Kind, e.DurationSeconds, e.AgentID, e.TaskID)
			}
			return nil
		},
	}
	list.Flags().String("task", "", "filter by task")

	derived := &cobra.Command{
		Use: "derived", Short: "Container runtime derived from the audit log",
		RunE: func(cmd *cobra.Command, _ []string) error {
			since, _ := cmd.Flags().GetString("since")
			path := "/api/v1/work-events/derived"
			if since != "" {
				path += "?since=" + since
			}
			var out struct {
				Events []*model.WorkEvent `json:"events"`
				Note   string             `json:"note"`
			}
			if err := client().Do(context.Background(), "GET", path, nil, &out); err != nil {
				return err
			}
			for _, e := range out.Events {
				fmt.Printf("%-22s %6.0fmin %s\n", e.AgentID, float64(e.DurationSeconds)/60, e.Meta["state"])
			}
			fmt.Println(out.Note)
			return nil
		},
	}
	derived.Flags().String("since", "", "RFC3339 start (default 7d)")

	cmd.AddCommand(add, list, derived)
	return cmd
}

func suggestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "suggest", Short: "Deterministic priority suggestion for a day",
		RunE: func(cmd *cobra.Command, _ []string) error {
			day, _ := cmd.Flags().GetString("day")
			if day == "today" {
				day = time.Now().Format("2006-01-02")
			}
			var out struct {
				Day         string `json:"day"`
				Suggestions []struct {
					TaskID  string   `json:"task_id"`
					Title   string   `json:"title"`
					Score   float64  `json:"score"`
					Reasons []string `json:"reasons"`
					Blocked bool     `json:"blocked"`
				} `json:"suggestions"`
			}
			if err := client().Do(context.Background(), "GET", "/api/v1/tasks/suggest?day="+day, nil, &out); err != nil {
				return err
			}
			for _, s := range out.Suggestions {
				fmt.Printf("%6.1f  %-40s %s\n", s.Score, s.Title, strings.Join(s.Reasons, ", "))
			}
			return nil
		},
	}
	cmd.Flags().String("day", "today", "day")
	return cmd
}

func calendarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "log", Short: "Calendar logbook of measured work",
		RunE: func(cmd *cobra.Command, _ []string) error {
			start, _ := cmd.Flags().GetString("start")
			end, _ := cmd.Flags().GetString("end")
			var out struct {
				Days map[string][]*model.WorkEvent `json:"days"`
			}
			path := "/api/v1/calendar?start=" + start + "&end=" + end
			if err := client().Do(context.Background(), "GET", path, nil, &out); err != nil {
				return err
			}
			for day, events := range out.Days {
				fmt.Println(day)
				for _, e := range events {
					fmt.Printf("  %s %-12s %6.0fmin %s\n", e.StartedAt.Format("15:04"), e.Kind, float64(e.DurationSeconds)/60, e.TaskID)
				}
			}
			return nil
		},
	}
	cmd.Flags().String("start", "", "start day YYYY-MM-DD")
	cmd.Flags().String("end", "", "end day YYYY-MM-DD")
	return cmd
}

func exportCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "export", Short: "Export versioned contracts"}
	cmd.AddCommand(&cobra.Command{
		Use: "productivity", Short: "Export goals, tasks, events, evidence, budgets, reviews",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var out map[string]any
			if err := client().Do(context.Background(), "GET", "/api/v1/export/productivity", nil, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	return cmd
}
