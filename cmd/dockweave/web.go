package main

// web serves the Angular frontend plus a local, token-protected gateway to
// the daemon and terminal unix sockets. It is unprivileged and loopback-only
// by default.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ananta888/dockweave/internal/oidc"
	"github.com/ananta888/dockweave/internal/webui"
)

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func buildSessionKey(hexKey string) ([32]byte, error) {
	var key [32]byte
	if hexKey != "" {
		b, err := hex.DecodeString(hexKey)
		if err != nil || len(b) != 32 {
			return key, errors.New("--oidc-session-key must be 32 bytes hex")
		}
		copy(key[:], b)
		return key, nil
	}
	if _, err := rand.Read(key[:]); err != nil {
		return key, err
	}
	return key, nil
}

func webCmd() *cobra.Command {
	var (
		listen            string
		webDir            string
		termSocket        string
		open              bool
		browseRoots       string
		mcpListen         string
		mcpToken          string
		paperclipURL      string
		paperclipCLI      string
		paperclipWrapper  string
		oidcIssuer        string
		oidcClientID      string
		oidcClientSecret  string
		oidcRedirect      string
		oidcScopes        string
		oidcRolesClaim    string
		oidcAdminGroups   string
		oidcMonitorGroups string
		oidcSessionKey    string
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
			// A configured token is never echoed: under systemd stdout ends
			// up in the journal. Only an ephemeral random one is printed.
			tokenConfigured := token != ""
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
			mcpTokenSource := "--mcp-token / DOCKWEAVE_UI_MCP_TOKEN"
			if mcpListen != "" && mcpToken == "" {
				home, _ := os.UserHomeDir()
				tokenPath := filepath.Join(home, ".local", "share", "dockweave", "ui-mcp-token")
				mcpTokenSource = tokenPath
				if b, err := os.ReadFile(tokenPath); err == nil {
					mcpToken = strings.TrimSpace(string(b))
				}
				if mcpToken == "" {
					var err error
					mcpToken, err = webui.NewMCPToken()
					if err != nil {
						fail(err)
					}
					if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err == nil {
						_ = os.WriteFile(tokenPath, []byte(mcpToken+"\n"), 0o600)
					}
				}
			}
			var roots []string
			for _, r := range strings.Split(browseRoots, ":") {
				if r = strings.TrimSpace(r); r != "" {
					roots = append(roots, r)
				}
			}
			var oidcCfg *oidc.Config
			if oidcIssuer != "" {
				key, err := buildSessionKey(oidcSessionKey)
				if err != nil {
					fail(err)
				}
				redirect := oidcRedirect
				if redirect == "" {
					redirect = fmt.Sprintf("http://%s/oauth/callback", listen)
				}
				oidcCfg = &oidc.Config{
					Issuer:        oidcIssuer,
					ClientID:      oidcClientID,
					ClientSecret:  oidcClientSecret,
					RedirectURL:   redirect,
					Scopes:        splitList(oidcScopes),
					RolesClaim:    oidcRolesClaim,
					AdminGroups:   splitList(oidcAdminGroups),
					MonitorGroups: splitList(oidcMonitorGroups),
					SessionKey:    key,
				}
			}
			srv, err := webui.New(webui.Config{
				DaemonSocket:     socketPath,
				TermSocket:       termSocket,
				WebDir:           dir,
				Token:            token,
				BrowseRoots:      roots,
				MCPToken:         mcpToken,
				PaperclipURL:     paperclipURL,
				PaperclipCLI:     paperclipCLI,
				PaperclipWrapper: paperclipWrapper,
				OIDC:             oidcCfg,
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
			url := fmt.Sprintf("http://%s/", ln.Addr())
			fmt.Printf("DockWeave web UI: %s\n", url)
			if tokenConfigured {
				fmt.Println("Login-Token: from DOCKWEAVE_WEB_TOKEN (not printed)")
			} else {
				fmt.Printf("Login-Token: %s\n", token)
			}
			var mcpSrv *http.Server
			if mcpListen != "" {
				mcpLn, err := net.Listen("tcp", mcpListen)
				if err != nil {
					fail(err)
				}
				mcpSrv = &http.Server{Handler: srv.MCPHandler(), ReadHeaderTimeout: 15 * time.Second}
				go func() { _ = mcpSrv.Serve(mcpLn) }()
				// The token itself is never printed (journal); it is in the
				// 0600 token file or the configured flag/environment.
				fmt.Printf("DockWeave UI MCP: http://%s/mcp/ui (bearer token: %s)\n", mcpLn.Addr(), mcpTokenSource)
			}
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
			if mcpSrv != nil {
				_ = mcpSrv.Shutdown(ctx)
			}
		},
	}
	f := c.Flags()
	f.StringVar(&listen, "listen", envOr("DOCKWEAVE_WEB_LISTEN", "127.0.0.1:8788"), "HTTP listen address (loopback recommended)")
	f.StringVar(&webDir, "web-dir", envOr("DOCKWEAVE_WEB_DIR", ""), "frontend directory (default: embedded assets)")
	f.StringVar(&termSocket, "term-socket", envOr("DOCKWEAVE_TERM_SOCKET", ""), "daemon terminal socket")
	f.BoolVar(&open, "open", false, "open the browser")
	f.StringVar(&browseRoots, "browse-roots", envOr("DOCKWEAVE_WEB_BROWSE_ROOTS",
		"/home:/srv:/opt/projects:/var/lib/dockweave"),
		"colon-separated roots for the directory browser")
	f.StringVar(&mcpListen, "mcp-listen", envOr("DOCKWEAVE_UI_MCP_LISTEN", ""),
		"optional TCP listen address for the UI MCP server, e.g. 0.0.0.0:8791")
	f.StringVar(&mcpToken, "mcp-token", envOr("DOCKWEAVE_UI_MCP_TOKEN", ""),
		"UI MCP bearer token (default: random when --mcp-listen is set)")
	f.StringVar(&oidcIssuer, "oidc-issuer", envOr("DOCKWEAVE_OIDC_ISSUER", ""), "OIDC issuer URL (enables OpenID Connect sign-in)")
	f.StringVar(&oidcClientID, "oidc-client-id", envOr("DOCKWEAVE_OIDC_CLIENT_ID", ""), "OIDC client id")
	f.StringVar(&oidcClientSecret, "oidc-client-secret", envOr("DOCKWEAVE_OIDC_CLIENT_SECRET", ""), "OIDC client secret")
	f.StringVar(&oidcRedirect, "oidc-redirect-url", envOr("DOCKWEAVE_OIDC_REDIRECT_URL", ""), "OIDC redirect URL (default http://<listen>/oauth/callback)")
	f.StringVar(&oidcScopes, "oidc-scopes", envOr("DOCKWEAVE_OIDC_SCOPES", "openid,profile"), "comma-separated OIDC scopes")
	f.StringVar(&oidcRolesClaim, "oidc-roles-claim", envOr("DOCKWEAVE_OIDC_ROLES_CLAIM", "groups"), "id_token claim carrying roles (groups|roles)")
	f.StringVar(&oidcAdminGroups, "oidc-admin-groups", envOr("DOCKWEAVE_OIDC_ADMIN_GROUPS", ""), "comma-separated groups mapped to admin")
	f.StringVar(&oidcMonitorGroups, "oidc-monitor-groups", envOr("DOCKWEAVE_OIDC_MONITOR_GROUPS", ""), "comma-separated groups mapped to monitor")
	f.StringVar(&oidcSessionKey, "oidc-session-key", envOr("DOCKWEAVE_OIDC_SESSION_KEY", ""), "32-byte hex session key (random when empty)")
	f.StringVar(&paperclipURL, "paperclip-url", envOr("DOCKWEAVE_PAPERCLIP_URL", "http://127.0.0.1:3100"),
		"Paperclip UI URL embedded by the frontend")
	f.StringVar(&paperclipCLI, "paperclip-cli", envOr("DOCKWEAVE_PAPERCLIP_CLI", ""),
		"paperclipai binary used by the local adapter (default: autodetect)")
	f.StringVar(&paperclipWrapper, "paperclip-wrapper", envOr("DOCKWEAVE_PAPERCLIP_WRAPPER", ""),
		"process-adapter wrapper for generated paperclip agents (default: deployments/paperclip/dockweave-run.sh)")
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
