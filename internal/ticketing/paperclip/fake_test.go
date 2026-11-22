package paperclip

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePaperclip mimics the Paperclip HTTP API as observed on 2026.1106.0:
// list filters (status csv, assigneeAgentId must be a UUID, parentId, q),
// 404 {"error":"Issue not found"}, statusVersion with expectedStatusVersion
// (409 on mismatch), atomic checkout with expectedStatuses (409 when the
// current status is not expected), release, wakeup, closed comment metadata.
type fakePaperclip struct {
	mu       sync.Mutex
	company  string
	agents   []map[string]any
	issues   map[string]map[string]any
	order    []string
	comments map[string][]map[string]any
	seq      int
	wakes    []string
	clock    time.Time
	srv      *httptest.Server
}

func newFake(t *testing.T, dwAgents ...string) *fakePaperclip {
	f := &fakePaperclip{company: "11111111-1111-4111-8111-111111111111", issues: map[string]map[string]any{},
		comments: map[string][]map[string]any{}, clock: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)}
	for i, a := range dwAgents {
		f.agents = append(f.agents, map[string]any{"id": uuid(1000 + i), "name": "Agent " + a, "adapterType": "process",
			"adapterConfig": map[string]any{"command": "dockweave-run.sh", "args": []string{a}}})
	}
	// An agent that drives no DockWeave agent.
	f.agents = append(f.agents, map[string]any{"id": uuid(999), "name": "Claude CEO", "adapterType": "claude_local", "adapterConfig": map[string]any{}})
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func uuid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func isUUID(s string) bool { return len(s) == 36 && strings.Count(s, "-") == 4 }

func (f *fakePaperclip) now() string {
	f.clock = f.clock.Add(time.Millisecond)
	return f.clock.Format("2006-01-02T15:04:05.000Z")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakePaperclip) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	seg := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/api/health":
		writeJSON(w, 200, map[string]any{"status": "ok", "version": "2026.1106.0"})
	case p == "/api/companies":
		writeJSON(w, 200, []map[string]any{{"id": f.company, "name": "Test"}})
	case p == "/api/companies/"+f.company+"/agents":
		writeJSON(w, 200, f.agents)
	case p == "/api/companies/"+f.company+"/issues" && r.Method == http.MethodGet:
		q := r.URL.Query()
		if a := q.Get("assigneeAgentId"); a != "" && a != "null" && !isUUID(a) {
			writeJSON(w, 400, map[string]any{"error": "assigneeAgentId must be a UUID or 'null'"})
			return
		}
		out := []map[string]any{}
		for _, id := range f.order {
			is := f.issues[id]
			if s := q.Get("status"); s != "" && !slices.Contains(strings.Split(s, ","), is["status"].(string)) {
				continue
			}
			if a := q.Get("assigneeAgentId"); a != "" && is["assigneeAgentId"] != a {
				continue
			}
			if pid := q.Get("parentId"); pid != "" && is["parentId"] != pid {
				continue
			}
			if s := strings.ToLower(q.Get("q")); s != "" && !strings.Contains(strings.ToLower(is["title"].(string)), s) &&
				!strings.Contains(strings.ToLower(fmt.Sprint(is["description"])), s) {
				continue
			}
			out = append(out, is)
		}
		writeJSON(w, 200, out)
	case p == "/api/companies/"+f.company+"/issues" && r.Method == http.MethodPost:
		f.seq++
		id := uuid(f.seq)
		ts := f.now()
		is := map[string]any{"id": id, "identifier": fmt.Sprintf("PC-%d", f.seq), "title": body["title"], "description": body["description"],
			"status": or(body["status"], "todo"), "priority": or(body["priority"], "medium"), "assigneeAgentId": body["assigneeAgentId"],
			"assigneeUserId": body["assigneeUserId"], "parentId": body["parentId"], "blockedByIssueIds": body["blockedByIssueIds"], "createdByUserId": "local-board",
			"statusVersion": 1, "createdAt": ts, "updatedAt": ts, "companyId": f.company}
		f.issues[id] = is
		f.order = append(f.order, id)
		writeJSON(w, 201, is)
	case len(seg) >= 3 && seg[0] == "api" && seg[1] == "issues":
		is, ok := f.issues[seg[2]]
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "Issue not found"})
			return
		}
		sub := strings.Join(seg[3:], "/")
		switch {
		case sub == "" && r.Method == http.MethodGet:
			writeJSON(w, 200, is)
		case sub == "" && r.Method == http.MethodPatch:
			if ev, ok := body["expectedStatusVersion"]; ok && int(ev.(float64)) != is["statusVersion"].(int) {
				writeJSON(w, 409, map[string]any{"error": "Issue status changed"})
				return
			}
			for _, k := range []string{"title", "description", "priority", "assigneeAgentId", "assigneeUserId"} {
				if v, ok := body[k]; ok {
					is[k] = v
				}
			}
			if s, ok := body["status"]; ok && s != is["status"] {
				is["status"] = s
				is["statusVersion"] = is["statusVersion"].(int) + 1
			}
			is["updatedAt"] = f.now()
			writeJSON(w, 200, is)
		case sub == "comments" && r.Method == http.MethodGet:
			writeJSON(w, 200, f.comments[seg[2]])
		case sub == "comments" && r.Method == http.MethodPost:
			if _, bad := body["metadata"]; bad {
				writeJSON(w, 400, map[string]any{"error": "invalid metadata"})
				return
			}
			f.seq++
			c := map[string]any{"id": uuid(5000 + f.seq), "issueId": seg[2], "body": body["body"], "authorType": "user",
				"authorUserId": "local-board", "createdAt": f.now()}
			f.comments[seg[2]] = append(f.comments[seg[2]], c)
			writeJSON(w, 201, c)
		case sub == "checkout":
			exp, _ := body["expectedStatuses"].([]any)
			okStatus := false
			for _, e := range exp {
				okStatus = okStatus || e == is["status"]
			}
			if !okStatus {
				writeJSON(w, 409, map[string]any{"error": "Issue checkout conflict"})
				return
			}
			is["assigneeAgentId"], is["status"] = body["agentId"], "in_progress"
			is["statusVersion"] = is["statusVersion"].(int) + 1
			is["updatedAt"] = f.now()
			writeJSON(w, 200, is)
		case sub == "release":
			is["status"] = "todo"
			is["statusVersion"] = is["statusVersion"].(int) + 1
			is["updatedAt"] = f.now()
			writeJSON(w, 200, is)
		default:
			writeJSON(w, 404, map[string]any{"error": "not found"})
		}
	case len(seg) == 4 && seg[1] == "agents" && seg[3] == "wakeup":
		f.wakes = append(f.wakes, seg[2])
		writeJSON(w, 202, map[string]any{"ok": true})
	default:
		writeJSON(w, 404, map[string]any{"error": "not found"})
	}
}

func or(v any, def string) any {
	if v == nil || v == "" {
		return def
	}
	return v
}
