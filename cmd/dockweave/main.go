// dockweave is the DockWeave CLI. It is a thin client over the daemon API;
// all privileged work happens in dockweaved.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/model"
	"github.com/ananta888/dockweave/internal/mcp"
	"github.com/ananta888/dockweave/pkg/sdk"
	"github.com/ananta888/dockweave/ui/tui"
)

var (
	socketPath string
	jsonOut    bool
)

func client() *sdk.Client { return sdk.New(socketPath) }

func main() {
	root := &cobra.Command{
		Use:   "dockweave",
		Short: "DockWeave — dynamic workspaces for isolated AI agents",
	}
	root.PersistentFlags().StringVar(&socketPath, "socket", envOr("DOCKWEAVE_SOCKET", "/run/dockweave/dockweaved.sock"), "daemon socket")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "output JSON")

	root.AddCommand(agentCmd(), workspaceCmd(), grantCmd(), mountCmd(), monitorCmd(), eventsCmd(), tuiCmd(), statusCmd(), memoryCmd(), mcpCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

func ctx() context.Context {
	// Daemon round-trips are bounded by the SDK client's HTTP timeout;
	// the process exits right after the command completes.
	return context.Background()
}

// ---- agent ----

func agentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Manage agents"}

	var spec sdk.AgentSpec
	var ro bool
	create := &cobra.Command{
		Use:   "create NAME --image IMAGE",
		Short: "Create an agent identity",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			spec.Name = args[0]
			spec.AgentRO = &ro
			a, err := client().CreateAgent(ctx(), spec)
			if err != nil {
				fail(err)
			}
			out(a, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "ID\t%s\nNAME\t%s\nIMAGE\t%s\n", a.ID, a.Name, a.Image)
			})
		},
	}
	f := create.Flags()
	f.StringVar(&spec.Type, "type", "custom", "agent type (codex|opencode|claude|gemini|custom)")
	f.StringVar(&spec.Image, "image", "", "container image (required)")
	f.StringVar(&spec.EnvFile, "env-file", "", "file with KEY=VALUE env (secrets)")
	f.StringVar(&spec.NetworkMode, "network", "bridge", "network mode (bridge|none)")
	f.BoolVar(&ro, "agent-ro", true, "mount /agent read-only")
	f.IntVar(&spec.UID, "uid", 0, "run as uid (0 = container default)")
	f.IntVar(&spec.GID, "gid", 0, "run as gid")
	f.Float64Var(&spec.CPULimit, "cpus", 0, "CPU limit (cores)")
	f.Int64Var(&spec.MemoryMB, "memory-mb", 0, "memory limit (MiB)")
	f.StringSliceVar(&spec.Command, "cmd", nil, "override command")
	_ = create.MarkFlagRequired("image")

	list := &cobra.Command{
		Use: "list", Short: "List agents",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().ListAgents(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "NAME\tTYPE\tSTATE\tIMAGE\tID")
				for _, a := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.Type, a.State, a.Image, a.ID)
				}
			})
		},
	}

	action := func(use, act string) *cobra.Command {
		return &cobra.Command{
			Use: use + " NAME|ID", Short: strings.Title(use) + " an agent", Args: cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				a, err := client().AgentAction(ctx(), args[0], act)
				if err != nil {
					fail(err)
				}
				out(a, func(w *tabwriter.Writer) { fmt.Fprintf(w, "%s\t%s\n", a.Name, a.State) })
			},
		}
	}

	stop := &cobra.Command{
		Use: "stop NAME|ID", Short: "Stop an agent", Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := client().StopAgent(ctx(), args[0]); err != nil {
				fail(err)
			}
			fmt.Println("stopped")
		},
	}

	var prune bool
	remove := &cobra.Command{
		Use: "remove NAME|ID", Short: "Remove an agent (host data kept unless --prune)", Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := client().RemoveAgent(ctx(), args[0], prune); err != nil {
				fail(err)
			}
			fmt.Println("removed")
		},
	}
	remove.Flags().BoolVar(&prune, "prune", false, "also delete agent/memory/workspace host data")

	inspect := &cobra.Command{
		Use: "inspect NAME|ID", Short: "Show agent details", Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			a, err := client().GetAgent(ctx(), args[0])
			if err != nil {
				fail(err)
			}
			printJSON(a)
		},
	}

	session := &cobra.Command{
		Use: "session NAME|ID", Short: "Show adapter session metadata (if any)", Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			info, err := client().AgentSession(ctx(), args[0])
			if err != nil {
				fail(err)
			}
			if !info.Detected {
				fmt.Println("no agent adapter detected (container running?)")
				return
			}
			out(info, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "ADAPTER\t%s\n", info.Adapter)
				if info.ActiveProject != "" {
					fmt.Fprintf(w, "ACTIVE PROJECT\t%s\n", info.ActiveProject)
				}
				for _, s := range info.Sessions {
					fmt.Fprintf(w, "SESSION\t%s\n", s)
				}
			})
		},
	}

	var attachTermSocket string
	attach := &cobra.Command{
		Use:   "attach NAME|ID [-- CMD...]",
		Short: "Open an interactive terminal in the agent container",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var extra []string
			if i := cmd.ArgsLenAtDash(); i >= 0 && i < len(args) {
				extra = args[i:]
				args = args[:i]
			}
			if err := attachAgent(attachTermSocket, args[0], extra); err != nil {
				fail(err)
			}
		},
	}
	attach.Flags().StringVar(&attachTermSocket, "term-socket",
		envOr("DOCKWEAVE_TERM_SOCKET", "/run/dockweave/dockweaved.term.sock"), "terminal stream socket")
	attach.Flags().SetInterspersed(false)

	cmd.AddCommand(create, list, action("start", "start"), stop, action("restart", "restart"), remove, inspect, session, attach)
	return cmd
}

// ---- workspace ----

func workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Manage workspaces"}
	register := &cobra.Command{
		Use: "register NAME PATH", Short: "Register a shareable host directory", Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			w, err := client().RegisterWorkspace(ctx(), args[0], args[1])
			if err != nil {
				fail(err)
			}
			out(w, func(tw *tabwriter.Writer) {
				fmt.Fprintf(tw, "ID\t%s\nNAME\t%s\nPATH\t%s\n", w.ID, w.Name, w.HostPath)
			})
		},
	}
	list := &cobra.Command{
		Use: "list", Short: "List workspaces",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().ListWorkspaces(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "NAME\tPATH\tID")
				for _, ws := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\n", ws.Name, ws.HostPath, ws.ID)
				}
			})
		},
	}
	unregister := &cobra.Command{
		Use: "unregister NAME|ID", Short: "Remove a workspace", Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := client().UnregisterWorkspace(ctx(), args[0]); err != nil {
				fail(err)
			}
			fmt.Println("unregistered")
		},
	}
	cmd.AddCommand(register, list, unregister)
	return cmd
}

// ---- grant ----

func grantCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "grant", Short: "Manage workspace grants"}
	var mode string
	var exclusive bool
	var ttl string
	create := &cobra.Command{
		Use:   "create WORKSPACE AGENT",
		Short: "Grant an agent access to a workspace",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			g, err := client().Grant(ctx(), args[0], args[1], mode, exclusive, ttl)
			if err != nil {
				fail(err)
			}
			out(g, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "ID\t%s\nMODE\t%s\n", g.ID, g.Mode)
			})
		},
	}
	create.Flags().StringVar(&mode, "mode", "ro", "access mode (ro|rw)")
	create.Flags().BoolVar(&exclusive, "exclusive", false, "exclusive write grant")
	create.Flags().StringVar(&ttl, "ttl", "", "time to live (e.g. 2h)")
	list := &cobra.Command{
		Use: "list", Short: "List grants",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().ListGrants(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "ID\tWORKSPACE\tAGENT\tMODE\tEXPIRES")
				for _, g := range list {
					exp := "-"
					if g.ExpiresAt != nil {
						exp = g.ExpiresAt.Format(time.RFC3339)
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", g.ID, g.WorkspaceID, g.AgentID, g.Mode, exp)
				}
			})
		},
	}
	var force bool
	revoke := &cobra.Command{
		Use:   "revoke GRANT_ID",
		Short: "Revoke a grant and unmount",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := client().RevokeGrant(ctx(), args[0], force); err != nil {
				fail(err)
			}
			fmt.Println("revoked")
		},
	}
	revoke.Flags().BoolVar(&force, "force", false, "force lazy unmount if busy (audited)")
	cmd.AddCommand(create, list, revoke)
	return cmd
}

// ---- mounts ----

func mountCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mount", Short: "Inspect mounts"}
	var agent string
	list := &cobra.Command{
		Use: "list", Short: "List mount states",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().ListMounts(ctx(), agent)
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "AGENT\tTARGET\tMODE\tSTATUS\tSOURCE")
				for _, m := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.AgentID, m.TargetPath, m.Mode, m.Status, m.HostPath)
				}
			})
		},
	}
	list.Flags().StringVar(&agent, "agent", "", "filter by agent")
	cmd.AddCommand(list)
	return cmd
}

// ---- monitor / events / status ----

func monitorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "monitor", Short: "Live activity and conflicts"}
	activity := &cobra.Command{
		Use: "activity", Short: "Show live agent activity",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().Activity(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "AGENT\tSTATE\tACTIVITY\tCPU%\tMEM")
				for _, a := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\t%.1f\t%d\n", a.AgentID, a.State, a.Activity, a.CPUPercent, a.MemoryBytes)
				}
			})
		},
	}
	conflicts := &cobra.Command{
		Use: "conflicts", Short: "Show multi-agent file conflicts",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().Conflicts(ctx())
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "FILE\tAGENTS\tWRITERS")
				for _, c := range list {
					fmt.Fprintf(w, "%s\t%s\t%s\n", c.Path, strings.Join(c.AgentIDs, ","), strings.Join(c.Writers, ","))
				}
			})
		},
	}
	cmd.AddCommand(activity, conflicts)
	return cmd
}

func eventsCmd() *cobra.Command {
	var follow bool
	var limit int
	cmd := &cobra.Command{
		Use: "events", Short: "Show audit events",
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().Events(ctx(), limit)
			if err != nil {
				fail(err)
			}
			printEvents(list)
			if follow {
				err := client().StreamEvents(context.Background(), func(e *model.Event) {
					printEvents([]*model.Event{e})
				})
				if err != nil {
					fail(err)
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new events")
	cmd.Flags().IntVar(&limit, "limit", 50, "number of events")
	return cmd
}

func printEvents(list []*model.Event) {
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		fmt.Printf("%s  %-18s  %-10s  %s\n", e.Timestamp.Format(time.RFC3339), e.Type, e.Actor, e.Message)
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Daemon health and provider status",
		Run: func(cmd *cobra.Command, args []string) {
			c := client()
			if err := c.Health(ctx()); err != nil {
				fail(err)
			}
			fmt.Println("daemon: ok")
			provs, err := c.Providers(ctx())
			if err != nil {
				fail(err)
			}
			for name, st := range provs {
				fmt.Printf("provider %-8s %s\n", name+":", st)
			}
		},
	}
}

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use: "tui", Short: "Interactive terminal UI",
		Run: func(cmd *cobra.Command, args []string) {
			if err := tui.Run(socketPath); err != nil {
				fail(err)
			}
		},
	}
}

func out(v any, table func(w *tabwriter.Writer)) {
	if jsonOut {
		printJSON(v)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	table(w)
	_ = w.Flush()
}

// ---- memory ----

func memoryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "memory", Short: "Manage agent memory snapshots"}
	snapshot := &cobra.Command{
		Use:   "snapshot NAME|ID",
		Short: "Create a memory snapshot",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			snap, err := client().SnapshotMemory(ctx(), args[0])
			if err != nil {
				fail(err)
			}
			out(snap, func(w *tabwriter.Writer) {
				fmt.Fprintf(w, "SNAPSHOT\t%s\nSIZE\t%d bytes\nCREATED\t%s\n", snap.Name, snap.SizeBytes, snap.CreatedAt)
			})
		},
	}
	list := &cobra.Command{
		Use:   "list NAME|ID",
		Short: "List memory snapshots",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			list, err := client().ListMemorySnapshots(ctx(), args[0])
			if err != nil {
				fail(err)
			}
			out(list, func(w *tabwriter.Writer) {
				fmt.Fprintln(w, "SNAPSHOT\tSIZE\tCREATED")
				for _, s := range list {
					fmt.Fprintf(w, "%s\t%d\t%s\n", s.Name, s.SizeBytes, s.CreatedAt)
				}
			})
		},
	}
	restore := &cobra.Command{
		Use:   "restore NAME|ID SNAPSHOT",
		Short: "Restore a memory snapshot (agent must be stopped)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			if err := client().RestoreMemorySnapshot(ctx(), args[0], args[1]); err != nil {
				fail(err)
			}
			fmt.Println("restored")
		},
	}
	del := &cobra.Command{
		Use:   "delete NAME|ID SNAPSHOT",
		Short: "Delete a memory snapshot",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			if err := client().DeleteMemorySnapshot(ctx(), args[0], args[1]); err != nil {
				fail(err)
			}
			fmt.Println("deleted")
		},
	}
	cmd.AddCommand(snapshot, list, restore, del)
	return cmd
}

// ---- mcp ----

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP stdio server (read-only monitoring tools)",
		Long: "Expose read-only DockWeave tools via the Model Context Protocol\n" +
			"over stdio. Intended as an MCP server entrypoint for AI assistants;\n" +
			"it never exposes mutating operations (no mounts, grants, lifecycle).",
		Run: func(cmd *cobra.Command, args []string) {
			srv := &mcp.Server{Client: sdk.New(socketPath), In: os.Stdin, Out: os.Stdout}
			if err := srv.Serve(context.Background()); err != nil {
				fail(err)
			}
		},
	}
}
