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
		{"ticket_run_ran", []string{"ticket", "run", "beta"}, 200, `{"kind":"ran","ticket":{"provider":"native","id":"tk_1"},"status":"done"}`, 0, "/api/v1/tickets/run", func(t *testing.T, r *http.Request) {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["agent"] != "beta" {
				t.Errorf("run body: %v %v", body, err)
			}
		}},
		{"ticket_run_blocked", []string{"ticket", "run", "beta"}, 200, `{"kind":"blocked","detail":"Pipeline-Sperre"}`, 3, "/api/v1/tickets/run", nil},
		{"ticket_run_agent_failed", []string{"ticket", "run", "beta"}, 200, `{"kind":"agent_failed","detail":"no response"}`, 1, "/api/v1/tickets/run", nil},
		{"ticket_route", []string{"ticket", "route", "native:tk_1", "--agent", "alpha"}, 200, `{"lines":["WAKE\tbeta\tallow"],"complete":true}`, 0, "/api/v1/tickets/native:tk_1/route", nil},
		{"ticket_provider_off", []string{"ticket", "provider"}, 200, `{"configured":false,"available":["native","paperclip"],"error":"ticketing is not configured"}`, 0, "/api/v1/tickets/provider", nil},
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
			// Runtime errors (daemon answers, refusals) must not append the
			// usage text: the wrapper copies stderr into blocking comments.
			if strings.Contains(string(out), "Usage:") {
				t.Fatalf("runtime error printed usage: %s", out)
			}
		})
	}
	// Genuine usage errors still show the usage text.
	t.Run("usage_error", func(t *testing.T) {
		command := exec.CommandContext(t.Context(), binary, "pipeline", "status", "--no-such-flag")
		out, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Usage:") {
			t.Fatalf("usage error without usage text: err=%v out=%s", err, out)
		}
	})
	t.Run("unreachable_daemon", func(t *testing.T) {
		command := exec.CommandContext(t.Context(), binary, "--socket", filepath.Join(root, "absent.sock"), "pipeline", "list")
		out, err := command.CombinedOutput()
		if err == nil || strings.Contains(string(out), "Usage:") {
			t.Fatalf("daemon error: err=%v out=%s", err, out)
		}
	})
}
