package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// RF-031: CLI contract as a real subprocess against a recording daemon socket.

type recorded struct {
	Method, Path, Query string
	Body                map[string]any
}

type cliFixture struct {
	t      *testing.T
	binary string
	socket string
	mu     sync.Mutex
	reqs   []recorded
	status int
	reply  string
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "dw-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	f := &cliFixture{t: t, binary: filepath.Join(root, "dockweave"), socket: filepath.Join(root, "d.sock"), status: 200, reply: "{}"}
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", f.binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.URL.RawQuery, body})
		status, reply := f.status, f.reply
		f.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return f
}

func (f *cliFixture) respond(status int, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.reply, f.reqs = status, reply, nil
}

func (f *cliFixture) run(args ...string) (stdout, stderr string, code int) {
	f.t.Helper()
	cmd := exec.CommandContext(f.t.Context(), f.binary, append([]string{"--socket", f.socket}, args...)...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return o.String(), e.String(), code
}

func (f *cliFixture) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func TestCLIStdoutIsPureJSONAndErrorsGoToStderr(t *testing.T) {
	f := newCLIFixture(t)
	f.respond(200, `{"agent_id":"b","allowed":false,"edges":[]}`)
	out, errOut, code := f.run("--json", "pipeline", "status", "beta")
	if code != 3 || !json.Valid([]byte(out)) {
		t.Fatalf("blocked: exit %d stdout %q", code, out)
	}
	f.respond(200, `{"revision":2,"admissions":[]}`)
	if out, _, code := f.run("--json", "pipeline", "status"); code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("collection: exit %d stdout %q", code, out)
	}
	f.respond(404, `{"error":{"code":"not_found","message":"agent missing"}}`)
	out, errOut, code = f.run("--json", "pipeline", "status", "missing")
	if code != 1 || strings.TrimSpace(out) != "" || !strings.Contains(errOut, "not_found") {
		t.Fatalf("technical error: exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestCLIAliasFiltersAndEscaping(t *testing.T) {
	f := newCLIFixture(t)
	f.respond(200, `{"agent_id":"b","allowed":true,"edges":[]}`)
	f.run("--json", "pipeline", "admission", "my agent&x=1")
	r := f.requests()
	if len(r) != 1 || r[0].Path != "/api/v1/pipelines/admission" || !strings.Contains(r[0].Query, "agent=my+agent%26x%3D1") {
		t.Fatalf("admission alias / escaping: %+v", r)
	}
	f.respond(200, `{"revision":1,"edges":[]}`)
	f.run("--json", "pipeline", "list", "--agent", "a b")
	if r := f.requests(); len(r) != 1 || r[0].Path != "/api/v1/pipelines" || r[0].Query != "agent=a+b" {
		t.Fatalf("list filter: %+v", r)
	}
	f.respond(200, `{"overrides":[]}`)
	f.run("--json", "pipeline", "overrides", "beta", "--active")
	if r := f.requests(); len(r) != 1 || !strings.Contains(r[0].Query, "active=true") || !strings.Contains(r[0].Query, "agent=beta") {
		t.Fatalf("overrides filter: %+v", r)
	}
}

func TestCLIOverrideValidationAndFlags(t *testing.T) {
	f := newCLIFixture(t)
	f.respond(200, `{"id":"o1","single_use":true}`)
	for _, args := range [][]string{
		{"pipeline", "override", "beta", "--gate", "tests"},
		{"pipeline", "override", "beta", "--reason", "why"},
		{"pipeline", "override", "beta", "--gate", " ", "--reason", "why"},
	} {
		if _, _, code := f.run(args...); code == 0 {
			t.Fatalf("%v must fail", args)
		}
		if n := len(f.requests()); n != 0 {
			t.Fatalf("%v sent %d mutation(s)", args, n)
		}
	}
	f.run("--json", "pipeline", "override", "beta", "--gate", "tests", "--reason", "hotfix")
	r := f.requests()
	if len(r) != 1 || r[0].Body["single_use"] != true || r[0].Body["ttl_sec"] != float64(900) {
		t.Fatalf("defaults: %+v", r)
	}
	f.respond(200, `{"id":"o2","single_use":false}`)
	f.run("--json", "pipeline", "override", "beta", "--gate", "*", "--reason", "x", "--reusable", "--ttl", "2m")
	if r := f.requests(); len(r) != 1 || r[0].Body["single_use"] != false || r[0].Body["ttl_sec"] != float64(120) || r[0].Body["gate_id"] != "*" {
		t.Fatalf("reusable/ttl: %+v", r)
	}
	if _, _, code := f.run("pipeline", "override", "beta", "--gate", "g", "--reason", "x", "--ttl", "soon"); code == 0 || len(f.requests()) != 1 {
		t.Fatal("invalid --ttl must fail before sending")
	}
	f.respond(400, `{"error":{"code":"invalid","message":"ttl_sec must be 0..86400"}}`)
	if _, errOut, code := f.run("pipeline", "override", "beta", "--gate", "g", "--reason", "x", "--ttl", "48h"); code != 1 || !strings.Contains(errOut, "ttl_sec") {
		t.Fatalf("server rejection: %d %q", code, errOut)
	}
}

func TestCLIGateReviewAndPruneParameters(t *testing.T) {
	f := newCLIFixture(t)
	f.respond(200, `{"issue_id":"DOC-1","verdict":"PASS"}`)
	f.run("--json", "gate", "review", "DOC-1", "--verdict", "PASS", "--assignee", "pc-rev", "--note", "lgtm")
	r := f.requests()
	if len(r) != 1 || r[0].Path != "/api/v1/gates/reviews" || r[0].Body["assigned_reviewer"] != "pc-rev" || r[0].Body["verdict"] != "PASS" {
		t.Fatalf("review: %+v", r)
	}
	f.respond(200, `{}`)
	if _, _, code := f.run("gate", "review", "DOC-1"); code == 0 || len(f.requests()) != 0 {
		t.Fatal("review without --verdict must fail without request")
	}
	f.respond(200, `{"pruned":3}`)
	f.run("--json", "gate", "prune", "--days", "7", "--keep", "5")
	if r := f.requests(); len(r) != 1 || r[0].Body["days"] != float64(7) || r[0].Body["keep_per_workspace"] != float64(5) {
		t.Fatalf("prune: %+v", r)
	}
	f.respond(400, `{"error":{"code":"invalid","message":"days must be 0..36500"}}`)
	if _, errOut, code := f.run("gate", "prune", "--days", "-1"); code != 1 || !strings.Contains(errOut, "days") {
		t.Fatalf("invalid prune: %d %q", code, errOut)
	}
	if _, _, code := f.run("gate", "run", "/ws", "--timeout", "abc"); code == 0 {
		t.Fatal("non-numeric --timeout must fail")
	}
}
