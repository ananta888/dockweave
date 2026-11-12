// Package webui serves the Angular DockWeave frontend and bridges browser
// requests to the daemon's unix sockets. It is unprivileged and local-only by
// default; it never performs mounts or Docker operations itself.
package webui

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

//go:embed all:dist
var embeddedFS embed.FS

// Config configures the web gateway.
type Config struct {
	DaemonSocket string
	TermSocket   string
	WebDir       string // empty = embedded assets
	Token        string // required
	Logger       *slog.Logger
	// AllowedOrigins for WebSocket Origin checks. Empty disables the check
	// (the listener is loopback and every request needs the token).
	AllowedOrigins []string
	// BrowseRoots limits the admin directory browser. Empty uses defaults.
	BrowseRoots []string
	// MCPToken enables the UI MCP endpoint when non-empty.
	MCPToken string
}

// Server is the browser-facing HTTP handler.
type Server struct {
	cfg         Config
	proxy       *httputil.ReverseProxy
	assets      fs.FS
	handler     http.Handler
	browseRoots []string
	uiHub       *UIHub
	mcp         *MCPHandler
}

// New creates the web gateway.
func New(cfg Config) (*Server, error) {
	if cfg.Token == "" {
		return nil, errors.New("web token is required")
	}
	if cfg.DaemonSocket == "" || cfg.TermSocket == "" {
		return nil, errors.New("daemon and term sockets are required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	var assets fs.FS
	if cfg.WebDir != "" {
		assets = os.DirFS(cfg.WebDir)
	} else {
		sub, err := fs.Sub(embeddedFS, "dist")
		if err != nil {
			return nil, err
		}
		assets = sub
	}
	roots := cfg.BrowseRoots
	if len(roots) == 0 {
		roots = defaultBrowseRoots()
	}
	s := &Server{cfg: cfg, assets: assets, browseRoots: roots, uiHub: NewUIHub()}
	if cfg.MCPToken != "" {
		s.mcp = NewMCPHandler(cfg.MCPToken, s.uiHub)
	}
	s.proxy = newUnixProxy(cfg.DaemonSocket)
	s.handler = s.auth(s.routes())
	return s, nil
}

// MCPHandler returns the UI MCP endpoint (nil when no token is configured).
func (s *Server) MCPHandler() http.Handler {
	if s.mcp == nil {
		return nil
	}
	return s.mcp
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/fs/dirs", s.listDirs)
	mux.HandleFunc("GET /ws/ui", s.uiHub.ServeWS)
	mux.Handle("/api/", s.proxy)
	mux.Handle("/v1/", s.proxy) // OpenAI-compatible endpoint (proxied to daemon)
	mux.HandleFunc("/ws/term", s.term)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/", s.static)
	return mux
}

func newUnixProxy(socket string) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    16,
		IdleConnTimeout: 60 * time.Second,
	}
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "dockweaved"
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "daemon unreachable: " + err.Error()})
		},
	}
}

const cookieName = "dockweave_web"

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The initial navigation may carry the token; set a cookie and
		// redirect to a clean URL so it does not stay in history.
		if q := r.URL.Query().Get("token"); q != "" {
			if !tokenEqual(q, s.cfg.Token) {
				http.Error(w, "invalid token", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: cookieName, Value: s.cfg.Token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
			r2 := *r
			u := *r.URL
			u.RawQuery = ""
			r2.URL = &u
			http.Redirect(w, &r2, u.String(), http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/favicon.ico" {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !tokenEqual(c.Value, s.cfg.Token) {
			http.Error(w, "token required: open the URL printed by `dockweave web`", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	file := strings.TrimPrefix(p, "/")
	if file == "" {
		file = "index.html"
	}
	if f, err := s.assets.Open(file); err == nil {
		_ = f.Close()
		if strings.HasPrefix(file, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.FileServer(http.FS(s.assets)).ServeHTTP(w, r)
		return
	}
	// SPA fallback: unknown paths render the Angular shell.
	index, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		http.Error(w, "frontend assets not built; run `make web`", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

// term bridges a browser WebSocket to the daemon's terminal unix socket.
//
// Browser -> server: JSON text frames
//
//	{"type":"input","data":"ls\r"}
//	{"type":"resize","rows":24,"cols":80}
//
// Server -> browser: binary PTY output frames.
func (s *Server) term(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		http.Error(w, "agent query parameter is required", http.StatusBadRequest)
		return
	}
	var extra []string
	if cmd := r.URL.Query().Get("cmd"); cmd != "" {
		extra = strings.Fields(cmd)
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.cfg.AllowedOrigins,
	})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	conn, err := net.Dial("unix", s.cfg.TermSocket)
	if err != nil {
		_ = ws.Write(r.Context(), websocket.MessageText, []byte("ERR term socket: "+err.Error()+"\r\n"))
		return
	}
	defer conn.Close()

	line := "ATTACH " + agent
	for _, a := range extra {
		line += " " + a
	}
	if _, err := io.WriteString(conn, line+"\n"); err != nil {
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// PTY -> browser.
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Browser -> PTY.
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := conn.Write(data); err != nil {
				return
			}
			continue
		}
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "input":
			if _, err := io.WriteString(conn, msg.Data); err != nil {
				return
			}
		case "resize":
			if msg.Rows > 0 && msg.Cols > 0 {
				_, _ = fmt.Fprintf(conn, "\x01W %d %d\n", msg.Rows, msg.Cols)
			}
		}
	}
}

// RandomToken returns a URL-safe random token.
func RandomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// DefaultWebDir returns the first existing candidate, or "" for embedded.
func DefaultWebDir(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// PeekErr consumes a leading "ERR ..." line from a terminal stream and
// returns it as an error. Used by clients that want a clean message.
func PeekErr(br *bufio.Reader) error {
	peek, err := br.Peek(4)
	if err != nil {
		return nil
	}
	if string(peek) != "ERR " {
		return nil
	}
	line, _ := br.ReadString('\n')
	return errors.New(strings.TrimSpace(line))
}
