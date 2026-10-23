package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/pkg/sdk"
)

// fakeAPI serves canned JSON for the read-only endpoints.
func fakeAPI() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":"ag_1","name":"codex-01","state":"running"}]`))
	})
	mux.HandleFunc("GET /api/v1/activity", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"agent_id":"ag_1","state":"running","activity":"WRITING","cpu_percent":12.5}]`))
	})
	mux.HandleFunc("GET /api/v1/conflicts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("GET /api/v1/mounts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	})
	return httptest.NewServer(mux)
}

func runRPC(t *testing.T, srv *Server, lines ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	out := &strings.Builder{}
	srv.In, srv.Out = in, out
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var replies []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			replies = append(replies, m)
		}
	}
	return replies
}

func TestInitializeAndToolsList(t *testing.T) {
	srv := &Server{Client: sdk.New("unused")}
	replies := runRPC(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(replies) != 2 {
		t.Fatalf("expected 2 replies, got %d", len(replies))
	}
	init := replies[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Fatalf("bad protocol version: %v", init["protocolVersion"])
	}
	tools := replies[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(tools))
	}
}

func TestToolCallAgainstFakeAPI(t *testing.T) {
	api := fakeAPI()
	defer api.Close()
	srv := &Server{Client: sdk.NewHTTP(api.URL)}
	replies := runRPC(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_agents"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_activity"}}`,
	)
	for _, r := range replies {
		content := r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)
		text := content["text"].(string)
		if !strings.Contains(text, "ag_1") {
			t.Fatalf("tool result missing agent data: %s", text)
		}
	}
}

func TestUnknownToolAndNotification(t *testing.T) {
	srv := &Server{Client: sdk.New("unused")}
	replies := runRPC(t, srv,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // no reply
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"grant_access"}}`,
		`{"jsonrpc":"2.0","id":10,"method":"bogus"}`,
	)
	if len(replies) != 2 {
		t.Fatalf("notifications must not produce replies; got %d", len(replies))
	}
	if replies[0]["error"] == nil {
		t.Fatal("unknown tool must error (no write tools exposed)")
	}
	if replies[1]["error"] == nil {
		t.Fatal("unknown method must error")
	}
}
