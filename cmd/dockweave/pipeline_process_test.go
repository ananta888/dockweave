package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipelineAndGateCLIProcesses(t *testing.T) {
	root, err := os.MkdirTemp("", "dw-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	binary := filepath.Join(root, "dockweave")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	for _, tc := range []struct {
		name     string
		args     []string
		status   int
		response string
		exit     int
		path     string
		check    func(*testing.T, *http.Request)
	}{
		{"allowed", []string{"pipeline", "status", "beta"}, 200, `{"agent_id":"b","allowed":true,"edges":[]}`, 0, "/api/v1/pipelines/admission", nil},
		{"blocked", []string{"pipeline", "status", "beta"}, 200, `{"agent_id":"b","allowed":false,"edges":[]}`, 3, "/api/v1/pipelines/admission", nil},
		{"forbidden", []string{"pipeline", "override", "beta", "--gate", "g", "--reason", "test"}, 403, `{"error":{"code":"forbidden","message":"admin only"}}`, 1, "/api/v1/pipelines/override", nil},
		{"not_found", []string{"pipeline", "status", "missing"}, 404, `{"error":{"code":"not_found","message":"missing"}}`, 1, "/api/v1/pipelines/admission", nil},
		{"invalid_json", []string{"pipeline", "list"}, 200, `{`, 1, "/api/v1/pipelines", nil},
		{"gate_phases", []string{"gate", "run", "/workspace", "--phase", "after_run,post_run", "--upstream-agent", "alpha"}, 200, `{"status":"passed"}`, 0, "/api/v1/gates/run", func(t *testing.T, r *http.Request) {
			var body struct {
				Phases   []string `json:"phases"`
				Upstream string   `json:"upstream_agent_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.Join(body.Phases, ",") != "after_run,post_run" || body.Upstream != "alpha" {
				t.Errorf("wrong run attribution: %+v %v", body, err)
			}
		}},
		{"gate_workspace_query", []string{"gate", "runs", "/work/a & b"}, 200, `{"runs":[]}`, 0, "/api/v1/gates/runs", func(t *testing.T, r *http.Request) {
			if r.URL.Query().Get("workspace") != "/work/a & b" {
				t.Errorf("workspace query: %s", r.URL.RawQuery)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(root, tc.name+".sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path=%s want %s", r.URL.Path, tc.path)
				}
				if tc.check != nil {
					tc.check(t, r)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			})}
			t.Cleanup(func() { server.Close() })
			go server.Serve(listener)
			args := append([]string{"--socket", socket, "--json"}, tc.args...)
			command := exec.CommandContext(t.Context(), binary, args...)
			out, err := command.CombinedOutput()
			code := 0
			if err != nil {
				code = command.ProcessState.ExitCode()
			}
			if code != tc.exit {
				t.Fatalf("exit=%d want=%d: %s", code, tc.exit, out)
			}
			if (tc.exit == 0 || tc.exit == 3) && !json.Valid(out) {
				t.Fatalf("stdout not JSON: %s", out)
			}
		})
	}
}
