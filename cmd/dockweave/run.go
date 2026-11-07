package main

// run is the one-shot convenience command: it uses the current directory as
// the project workspace, makes sure the selected agent has access, starts the
// agent and attaches to its terminal. It is a thin composition of the normal
// register -> grant -> start -> attach flow; it never bypasses the daemon.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/pkg/sdk"
)

func runCmd() *cobra.Command {
	var (
		agentFlag  string
		mode       string
		wsName     string
		noAttach   bool
		termSocket string
	)
	c := &cobra.Command{
		Use:   "run [AGENT] [-- CMD...]",
		Short: "Start an agent on the current directory and attach",
		Long: "Use the current directory as the project workspace, grant the agent\n" +
			"access, start it and attach to its terminal.\n\n" +
			"The agent is selected by name or ID; if only one agent exists it is\n" +
			"used automatically. Everything after -- is passed to the agent's\n" +
			"terminal as an override command.",
		Args: cobra.ArbitraryArgs,
		Run: func(cmd *cobra.Command, args []string) {
			var extra []string
			if i := cmd.ArgsLenAtDash(); i >= 0 {
				extra = append([]string(nil), args[i:]...)
				args = args[:i]
			}
			if len(args) > 1 {
				fail(errors.New("usage: dockweave run [AGENT] [-- CMD...]"))
			}
			ref := agentFlag
			if len(args) == 1 {
				if ref != "" && ref != args[0] {
					fail(fmt.Errorf("agent given twice: --agent %q and %q", ref, args[0]))
				}
				ref = args[0]
			}
			if err := runInCurrentDir(ctx(), ref, mode, wsName, !noAttach, termSocket, extra); err != nil {
				fail(err)
			}
		},
	}
	f := c.Flags()
	f.StringVarP(&agentFlag, "agent", "a", "", "agent name or ID (default: the only existing agent)")
	f.StringVar(&mode, "mode", "rw", "workspace access mode (ro|rw)")
	f.StringVar(&wsName, "name", "", "workspace name (default: current directory name)")
	f.BoolVar(&noAttach, "no-attach", false, "register, grant and start, but do not open a terminal")
	f.StringVar(&termSocket, "term-socket",
		envOr("DOCKWEAVE_TERM_SOCKET", "/run/dockweave/dockweaved.term.sock"), "terminal stream socket")
	return c
}

func runInCurrentDir(ctx context.Context, agentRef, mode, wsName string, attach bool, termSocket string, extra []string) error {
	if mode != string(model.ModeRO) && mode != string(model.ModeRW) {
		return fmt.Errorf("mode must be ro or rw, got %q", mode)
	}
	c := client()
	a, err := resolveRunAgent(ctx, c, agentRef)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("current directory: %w", err)
	}
	ws, err := ensureProjectWorkspace(ctx, c, cwd, wsName)
	if err != nil {
		return err
	}
	if err := ensureProjectGrant(ctx, c, a, ws, mode); err != nil {
		return err
	}

	// Start is idempotent and boots the container inside the first active
	// project mount, so the grant must exist before this call.
	a, err = c.AgentAction(ctx, a.ID, "start")
	if err != nil {
		return fmt.Errorf("start agent %q: %w", a.Name, err)
	}

	fmt.Fprintf(os.Stderr, "agent %s · %s → /workspace/%s\n", a.Name, ws.HostPath, ws.Name)

	if !attach {
		out(map[string]string{
			"agent":     a.Name,
			"workspace": ws.Name,
			"host_path": ws.HostPath,
			"mount":     "/workspace/" + ws.Name,
		}, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "AGENT\t%s\nWORKSPACE\t%s\nHOST PATH\t%s\nMOUNT\t/workspace/%s\n",
				a.Name, ws.Name, ws.HostPath, ws.Name)
		})
		return nil
	}
	return attachAgent(termSocket, a.ID, extra)
}

// resolveRunAgent selects the agent to run. With a reference it resolves the
// name or ID; without one, a single existing agent is used and an ambiguous
// set is reported instead of guessing.
func resolveRunAgent(ctx context.Context, c *sdk.Client, ref string) (*model.Agent, error) {
	if ref != "" {
		a, err := c.GetAgent(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", ref, err)
		}
		return a, nil
	}
	list, err := c.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	switch len(list) {
	case 0:
		return nil, errors.New("no agents exist yet; create one with 'dockweave agent create ...'")
	case 1:
		return list[0], nil
	default:
		names := make([]string, len(list))
		for i, a := range list {
			names[i] = a.Name
		}
		sort.Strings(names)
		return nil, fmt.Errorf("multiple agents exist; choose one: dockweave run <agent>\n  %s", strings.Join(names, "\n  "))
	}
}

// ensureProjectWorkspace registers cwd (canonicalized) unless a workspace for
// the same canonical path already exists. Name collisions are resolved with a
// numeric suffix rather than reusing another directory's workspace.
func ensureProjectWorkspace(ctx context.Context, c *sdk.Client, cwd, requested string) (*model.Workspace, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve current directory: %w", err)
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("canonicalize current directory: %w", err)
	}
	list, err := c.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	existing, name := planProjectWorkspace(list, canon, requested)
	if existing != nil {
		return existing, nil
	}
	w, err := c.RegisterWorkspace(ctx, name, canon)
	if err != nil {
		return nil, fmt.Errorf("register workspace %q for %s: %w", name, canon, err)
	}
	return w, nil
}

// planProjectWorkspace returns either the existing workspace for canon or a
// free name to register it under.
func planProjectWorkspace(list []*model.Workspace, canon, requested string) (*model.Workspace, string) {
	used := make(map[string]bool, len(list))
	for _, w := range list {
		if w.HostPath == canon {
			return w, ""
		}
		used[w.Name] = true
	}
	name := requested
	if name == "" {
		name = projectSlug(filepath.Base(canon))
	}
	base := name
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return nil, name
}

// ensureProjectGrant makes sure the agent has an active grant for the
// workspace in the requested mode. A conflicting grant is reported, never
// silently changed: grants are permissions and revoking is an explicit act.
func ensureProjectGrant(ctx context.Context, c *sdk.Client, a *model.Agent, ws *model.Workspace, mode string) error {
	grants, err := c.ListGrants(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, g := range grants {
		if g.AgentID != a.ID || g.WorkspaceID != ws.ID {
			continue
		}
		if g.Status == model.GrantActive && string(g.Mode) == mode {
			return nil
		}
		if !g.Pending(now) && g.Status == model.GrantActive {
			// Expired grants still occupy the uniqueness index; clean up so a
			// fresh grant can be created.
			if err := c.RevokeGrant(ctx, g.ID, false); err != nil {
				return fmt.Errorf("revoke expired grant %s on %q: %w", g.ID, ws.Name, err)
			}
			continue
		}
		return fmt.Errorf("agent %q already has a %s grant on %q; revoke it first or run with --mode %s",
			a.Name, g.Mode, ws.Name, g.Mode)
	}
	if _, err := c.Grant(ctx, ws.Name, a.ID, mode, false, ""); err != nil {
		return fmt.Errorf("grant %q to %q: %w", ws.Name, a.Name, err)
	}
	return nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

// projectSlug turns a directory name into a valid workspace name.
func projectSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-._")
	}
	if s == "" {
		return "project"
	}
	return s
}
