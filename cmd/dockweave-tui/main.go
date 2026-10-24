// Command dockweave-tui renders the DockWeave terminal UI. It is a
// separate binary so that non-interactive commands (CLI, agent attach)
// never load the TUI stack: bubbletea's package init queries the
// terminal (OSC 11/DSR) and blocks for its timeout when no reply is
// routed back (e.g. when another pane has focus).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ananta888/dockweave/ui/tui"
)

func main() {
	socket := flag.String("socket", envOr("DOCKWEAVE_SOCKET", "/run/dockweave/dockweaved.sock"), "daemon socket")
	embedded := flag.Bool("embedded", false, "run inside the managed tmux layout")
	plain := flag.Bool("plain", false, "run without the tmux layout")
	flag.Parse()

	var err error
	if !*embedded && !*plain {
		used, lerr := tui.LaunchLayout(*socket)
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "error:", lerr)
			os.Exit(1)
		}
		if used {
			return
		}
	}
	if err = tui.Run(*socket, *embedded); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
