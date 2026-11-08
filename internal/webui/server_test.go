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

	// No token -> 401.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", rec.Code)
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
