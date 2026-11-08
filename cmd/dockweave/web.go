package main

// web serves the Angular frontend plus a local, token-protected gateway to
// the daemon and terminal unix sockets. It is unprivileged and loopback-only
// by default.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/webui"
)

func webCmd() *cobra.Command {
	var (
		listen     string
		webDir     string
		termSocket string
		open       bool
	)
	c := &cobra.Command{
		Use:   "web",
		Short: "Serve the Angular web UI with a local API/terminal gateway",
		Long: "Starts a loopback-only HTTP server that serves the Angular SPA and\n" +
			"bridges /api and /ws/term to the daemon unix sockets. A random token\n" +
			"is required; the full URL is printed at startup. The gateway runs\n" +
			"unprivileged with your own UID.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			token := envOr("DOCKWEAVE_WEB_TOKEN", "")
			if token == "" {
				var err error
				token, err = webui.RandomToken()
				if err != nil {
					fail(err)
				}
			}
			if termSocket == "" {
				termSocket = envOr("DOCKWEAVE_TERM_SOCKET",
					filepath.Join(filepath.Dir(socketPath), "dockweaved.term.sock"))
			}
			dir := webDir
			if dir == "" {
				dir = webui.DefaultWebDir(
					"ui/web/dist/dockweave-web/browser",
					"web",
				)
			}
			srv, err := webui.New(webui.Config{
				DaemonSocket: socketPath,
				TermSocket:   termSocket,
				WebDir:       dir,
				Token:        token,
			})
			if err != nil {
				fail(err)
			}
			ln, err := net.Listen("tcp", listen)
			if err != nil {
				fail(err)
			}
			host, _, _ := net.SplitHostPort(ln.Addr().String())
			if host != "" && host != "127.0.0.1" && host != "::1" && host != "localhost" {
				fmt.Fprintf(os.Stderr, "warning: web UI is listening on %s, not loopback\n", ln.Addr())
			}
			url := fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token)
			fmt.Printf("DockWeave web UI: %s\n", url)
			if open {
				openBrowser(url)
			}
			httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 15 * time.Second}
			errCh := make(chan error, 1)
			go func() { errCh <- httpSrv.Serve(ln) }()
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			select {
			case s := <-sig:
				fmt.Fprintf(os.Stderr, "shutting down (%s)\n", s)
			case err := <-errCh:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					fail(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(ctx)
		},
	}
	f := c.Flags()
	f.StringVar(&listen, "listen", envOr("DOCKWEAVE_WEB_LISTEN", "127.0.0.1:8788"), "HTTP listen address (loopback recommended)")
	f.StringVar(&webDir, "web-dir", envOr("DOCKWEAVE_WEB_DIR", ""), "frontend directory (default: embedded assets)")
	f.StringVar(&termSocket, "term-socket", envOr("DOCKWEAVE_TERM_SOCKET", ""), "daemon terminal socket")
	f.BoolVar(&open, "open", false, "open the browser")
	return c
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "open browser: %v\n", err)
	}
}
