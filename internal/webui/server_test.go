package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/ananta888/dockweave/internal/oidc"
)

func unixHTTP(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return sock
}

func TestTokenAuthAndProxy(t *testing.T) {
	daemonSock := unixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			t.Errorf("proxy path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>dw</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{DaemonSocket: daemonSock, TermSocket: "/nonexistent", WebDir: webDir, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	// No token on "/" -> login page (200), never a token in the URL.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("no-token / status = %d, want 200 (login page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Login-Token") {
		t.Fatalf("login page missing: %q", rec.Body.String())
	}

	// No token on an API path -> 401.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token api status = %d, want 401", rec.Code)
	}

	// POST /login with a bad token -> 403.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=wrong")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad login status = %d, want 403", rec.Code)
	}

	// POST /login with the right token -> cookie + redirect.
	rec = httptest.NewRecorder()
	reqLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=secret"))
	reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rec, reqLogin)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303", rec.Code)
	}

	// Token query -> cookie + redirect.
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token=secret", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("token status = %d, want 303", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != cookieName {
		t.Fatalf("no auth cookie: %+v", cookies)
	}

	// Cookie -> SPA.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "secret"})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "<html>dw</html>" {
		t.Fatalf("spa = %d %q", rec.Code, rec.Body.String())
	}

	// Cookie -> proxy.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "secret"})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("proxy = %d %q", rec.Code, rec.Body.String())
	}
}

func TestSPAFallback(t *testing.T) {
	webDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(webDir, "index.html"), []byte("INDEX"), 0o644)
	srv, err := New(Config{DaemonSocket: "/nonexistent", TermSocket: "/nonexistent", WebDir: webDir, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/agents/opencode-01", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "t"})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "INDEX" {
		t.Fatalf("fallback = %d %q", rec.Code, rec.Body.String())
	}
}

func TestTerminalWebSocketBridge(t *testing.T) {
	termSock := filepath.Join(t.TempDir(), "term.sock")
	ln, err := net.Listen("unix", termSock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gotAttach := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		line, _ := br.ReadString('\n')
		gotAttach <- line
		_, _ = c.Write([]byte("HELLO"))
		buf := make([]byte, 64)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				_, _ = c.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	webDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(webDir, "index.html"), []byte("x"), 0o644)
	srv, err := New(Config{DaemonSocket: "/nonexistent", TermSocket: termSock, WebDir: webDir, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/term?agent=agent-a"
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookieName + "=t"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	typ, data, err := ws.Read(ctx)
	if err != nil || typ != websocket.MessageBinary || string(data) != "HELLO" {
		t.Fatalf("first frame: %v %q %v", typ, data, err)
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"abc"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err = ws.Read(ctx)
	if err != nil || string(data) != "abc" {
		t.Fatalf("echo: %q %v", data, err)
	}
	select {
	case line := <-gotAttach:
		if !strings.HasPrefix(line, "ATTACH agent-a") {
			t.Fatalf("attach line = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("term server did not receive ATTACH")
	}
}

func TestDirectoryBrowser(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	webDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(webDir, "index.html"), []byte("x"), 0o644)
	srv, err := New(Config{DaemonSocket: "/nonexistent", TermSocket: "/nonexistent",
		WebDir: webDir, Token: "t", BrowseRoots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs?path="+filepath.Join(root, "a"), nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "t"})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Path    string                  `json:"path"`
		Parent  string                  `json:"parent"`
		Entries []struct{ Name string } `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.Entries[0].Name != "b" || out.Parent != root {
		t.Fatalf("listing = %+v", out)
	}
	// Outside the root is rejected.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs?path=/etc", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "t"})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside status = %d", rec.Code)
	}
}

func TestBearerAndRateLimit(t *testing.T) {
	daemonSock := filepath.Join(t.TempDir(), "daemon.sock")
	webDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>dw</html>"), 0o644)
	srv, err := New(Config{DaemonSocket: daemonSock, TermSocket: "/nonexistent", WebDir: webDir, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	// Bearer header grants access without a cookie.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dw") {
		t.Fatalf("bearer / = %d body=%q", rec.Code, rec.Body.String())
	}

	// Security headers are present.
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing Cache-Control: %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing X-Frame-Options")
	}

	// Rate limit: after 5 bad logins the next is 429.
	for i := 0; i < 5; i++ {
		bad := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=wrong"))
		bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, bad)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("bad login %d = %d, want 403", i, rec.Code)
		}
	}
	over := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=wrong"))
	over.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, over)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit = %d, want 429", rec.Code)
	}
}

// stubProvider/stubStore let tests inject an OIDC principal without an IdP.
type stubProvider struct{}

func (stubProvider) AuthURL(_, _, _ string) (string, error) { return "http://idp/auth", nil }
func (stubProvider) Exchange(context.Context, string, string, string) (*oidc.Principal, error) {
	return nil, oidc.ErrTokenExchange
}
func (stubProvider) EndSessionURL(string) string { return "/" }

type stubStore struct{ p oidc.Principal }

func (s stubStore) Save(http.ResponseWriter, oidc.Session) error { return nil }
func (s stubStore) Get(*http.Request) (*oidc.Session, error) {
	return &oidc.Session{Principal: s.p, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (s stubStore) Delete(http.ResponseWriter) {}

func newRoleGateway(t *testing.T, roles []string) *Server {
	t.Helper()
	daemonSock := unixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>dw</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{DaemonSocket: daemonSock, TermSocket: "/nonexistent", WebDir: webDir, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	srv.oidcSvc = oidc.NewService(stubProvider{}, stubStore{
		p: oidc.Principal{Issuer: "iss", Subject: "sub", Roles: roles},
	}, oidc.Config{SessionTTL: time.Hour})
	return srv
}

func TestGatewayRoleEnforcement(t *testing.T) {
	monitor := newRoleGateway(t, []string{"monitor"})

	// monitor: reads are allowed (proxied to the daemon).
	rec := httptest.NewRecorder()
	monitor.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("monitor GET = %d, want 200", rec.Code)
	}

	// monitor: writes are denied.
	rec = httptest.NewRecorder()
	monitor.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/gates/run", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("monitor POST = %d, want 403", rec.Code)
	}

	// monitor: the directory browser is admin-only even though it is a GET.
	rec = httptest.NewRecorder()
	monitor.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("monitor fs/dirs = %d, want 403", rec.Code)
	}

	// monitor: the interactive terminal is admin-only (GET upgrade).
	rec = httptest.NewRecorder()
	monitor.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ws/term?agent=x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("monitor term = %d, want 403", rec.Code)
	}

	admin := newRoleGateway(t, []string{"admin"})
	rec = httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/gates/run", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin POST = %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/fs/dirs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin fs/dirs = %d, want 200", rec.Code)
	}
}

func TestGatewayTokenHolderIsAdmin(t *testing.T) {
	daemonSock := unixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	webDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>dw</html>"), 0o644)
	srv, err := New(Config{DaemonSocket: daemonSock, TermSocket: "/nonexistent", WebDir: webDir, Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	// A bearer-token holder keeps full (admin) write access.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gates/run", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer POST = %d, want 200", rec.Code)
	}
}
