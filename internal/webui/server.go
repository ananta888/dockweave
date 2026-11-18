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
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ananta888/dockweave/internal/oidc"
	"github.com/ananta888/dockweave/pkg/sdk"
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
	// PaperclipURL is the original Paperclip UI embedded by the frontend.
	PaperclipURL string
	// PaperclipCLI overrides the paperclipai binary used by the local adapter.
	PaperclipCLI string
	// PaperclipWrapper is the process-adapter command for generated agents.
	PaperclipWrapper string
	// OIDC (optional) enables OpenID Connect sign-in via internal/oidc.
	OIDC *oidc.Config
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
	loginLim    *loginLimiter
	oidcSvc     *oidc.Service
}

// loginLimiter throttles failed /login attempts per client address.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

const loginWindow = time.Minute
const loginMaxFails = 5

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string][]time.Time{}}
}

func (l *loginLimiter) allow(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-loginWindow)
	kept := l.attempts[addr][:0]
	for _, t := range l.attempts[addr] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.attempts[addr] = kept
	return len(kept) < loginMaxFails
}

func (l *loginLimiter) fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[addr] = append(l.attempts[addr], time.Now())
}

func clientAddr(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		return strings.TrimSpace(strings.Split(h, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
	s.loginLim = newLoginLimiter()
	if cfg.OIDC != nil && cfg.OIDC.Issuer != "" {
		prov, err := oidc.NewProvider(context.Background(), *cfg.OIDC)
		if err != nil {
			return nil, fmt.Errorf("oidc: %w", err)
		}
		store := oidc.CookieStore{Name: "dockweave_oidc", Key: cfg.OIDC.SessionKey, Secure: false}
		s.oidcSvc = oidc.NewService(prov, store, *cfg.OIDC)
		s.oidcSvc.SetAuditor(func(event string, p oidc.Principal) {
			s.auditOIDC(event, p)
		})
	}
	s.handler = s.securityHeaders(s.auth(s.routes()))
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

// auditOIDC records an OIDC event: structured log plus a best-effort,
// tamper-resistant daemon audit entry. Only iss + sub are ever stored.
func (s *Server) auditOIDC(event string, p oidc.Principal) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("oidc", "event", event, "iss", p.Issuer, "sub", p.Subject)
	}
	if s.cfg.DaemonSocket == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cl := sdk.New(s.cfg.DaemonSocket)
	_ = cl.Do(ctx, "POST", "/api/v1/audit",
		map[string]string{"event": event, "iss": p.Issuer, "sub": p.Subject}, nil)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/fs/dirs", s.listDirs)
	mux.HandleFunc("GET /graph", s.getGraph)
	mux.HandleFunc("PUT /graph", s.putGraph)
	mux.HandleFunc("GET /paperclip/config", s.paperclipConfig)
	mux.HandleFunc("GET /paperclip/status", s.paperclipStatus)
	mux.HandleFunc("POST /paperclip/issues", s.paperclipCreateIssue)
	mux.HandleFunc("POST /paperclip/agents/{id}/wake", s.paperclipWake)
	mux.HandleFunc("POST /paperclip/agents/{id}/target", s.paperclipSetTarget)
	mux.HandleFunc("POST /paperclip/agents", s.paperclipEnsureAgent)
	mux.HandleFunc("POST /paperclip/sync", s.paperclipSync)
	mux.HandleFunc("GET /ws/ui", s.uiHub.ServeWS)
	if s.oidcSvc != nil {
		mux.HandleFunc("GET /oauth/login", s.oidcSvc.BeginLogin)
		mux.HandleFunc("GET /oauth/callback", s.oidcSvc.HandleCallback)
		mux.HandleFunc("POST /oauth/logout", s.oidcSvc.Logout)
	}
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

const loginPageTemplate = `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>DockWeave</title>
<style>
:root{color-scheme:dark}
body{background:#0d1117;color:#c9d1d9;font:15px/1.4 system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
form{background:#161b22;border:1px solid #30363d;border-radius:8px;padding:2rem;width:320px}
h1{margin:0 0 .4rem;font-size:1.1rem;color:#58a6ff}
p{color:#8b949e;font-size:.8rem;margin:.2rem 0 1rem}
input{width:100%;box-sizing:border-box;background:#0d1117;border:1px solid #30363d;color:#c9d1d9;border-radius:5px;padding:.5rem;font-size:.9rem}
button{margin-top:.8rem;width:100%;background:#238636;border:1px solid #2ea043;color:#fff;border-radius:5px;padding:.5rem;cursor:pointer}
a.idp{display:block;margin-top:.8rem;text-align:center;color:#58a6ff;text-decoration:none}
</style></head><body>
<form method="post" action="/login">
<h1>DockWeave</h1>
<p>Login-Token eingeben (steht im Terminal, in dem <code>dockweave web</code> laeuft).</p>
<input name="token" type="password" autofocus autocomplete="off" placeholder="Token">
<button type="submit">Anmelden</button>
{{IDP}}
</form></body></html>`

// loginPage renders the login form, with an IdP link when OIDC is enabled.
func loginPage(idp bool) string {
	link := ""
	if idp {
		link = `<hr><a class="idp" href="/oauth/login">Mit IdP anmelden (OpenID Connect)</a>`
	}
	return strings.Replace(loginPageTemplate, "{{IDP}}", link, 1)
}

func (s *Server) setCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: s.cfg.Token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(loginPage(s.oidcSvc != nil)))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr := clientAddr(r)
	if !s.loginLim.allow(addr) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	if !tokenEqual(r.FormValue("token"), s.cfg.Token) {
		s.loginLim.fail(addr)
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	s.setCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// bearerOK reports whether the request carries the token as a bearer header.
func (s *Server) bearerOK(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	return tokenEqual(strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")), s.cfg.Token)
}

// securityHeaders sets conservative defaults on every response. The headers
// are re-applied in WriteHeader so a downstream handler (e.g. the static
// server's "no-cache") cannot override them.
type headerWriter struct {
	http.ResponseWriter
}

func (w headerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

func (w headerWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w headerWriter) setSecurity() {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
}

func (w headerWriter) Write(b []byte) (int, error) {
	w.setSecurity()
	return w.ResponseWriter.Write(b)
}

func (w headerWriter) WriteHeader(code int) {
	w.setSecurity()
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(headerWriter{w}, r)
	})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The OIDC routes handle their own security and must be reachable
		// without a session (the login redirect, callback and logout).
		if strings.HasPrefix(r.URL.Path, "/oauth/") {
			next.ServeHTTP(w, r)
			return
		}
		// Legacy/automation: a token in the query sets the cookie and
		// redirects to a clean URL so it does not stay in history.
		if q := r.URL.Query().Get("token"); q != "" {
			if !tokenEqual(q, s.cfg.Token) {
				http.Error(w, "invalid token", http.StatusForbidden)
				return
			}
			s.setCookie(w)
			r2 := *r
			u := *r.URL
			u.RawQuery = ""
			r2.URL = &u
			http.Redirect(w, &r2, u.String(), http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/login" {
			s.handleLogin(w, r)
			return
		}
		if r.URL.Path == "/favicon.ico" {
			next.ServeHTTP(w, r)
			return
		}
		if s.bearerOK(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.oidcSvc != nil {
			if _, err := s.oidcSvc.Principal(r); err == nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		c, err := r.Cookie(cookieName)
		if err == nil && tokenEqual(c.Value, s.cfg.Token) {
			next.ServeHTTP(w, r)
			return
		}
		// Browser navigation gets a login page; API/asset requests stay 401.
		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(loginPage(s.oidcSvc != nil)))
			return
		}
		http.Error(w, "token required: open the URL printed by `dockweave web`", http.StatusUnauthorized)
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
