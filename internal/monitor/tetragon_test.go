package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

func TestTetragonProviderUnavailable(t *testing.T) {
	p := NewTetragonProvider("", nil)
	if err := p.Available(context.Background()); err == nil {
		t.Fatal("empty events path must be unavailable")
	}
}

func TestTetragonProviderIngestAndObserve(t *testing.T) {
	dir := t.TempDir()
	rootA := filepath.Join(dir, "wsA")
	os.MkdirAll(rootA, 0o755)
	events := filepath.Join(dir, "events.jsonl")

	// JSONL export lines: one kprobe file-open (write) by pid 4242,
	// one exec; pid map attributes pid 4242 to agA.
	line1 := `{"time":"` + time.Now().Format(time.RFC3339Nano) + `","type":3,` +
		`"function_name":"security_file_open","process":{"pid":4242,"binary":"/bin/sh","args":["cat","/workspace/proj/a.txt"]},` +
		`"fields":{"path":"/workspace/proj/a.txt","open_flags":"O_WRONLY"}}` + "\n"
	line2 := `{"time":"` + time.Now().Format(time.RFC3339Nano) + `","type":1,` +
		`"process":{"pid":4242,"binary":"/usr/bin/vim","cwd":"/workspace/proj"}}` + "\n"
	noise := "not json\n"
	if err := os.WriteFile(events, []byte(line1+noise+line2), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewTetragonProvider(events, func() map[string]string { return map[string]string{"agA": rootA} })
	p.PIDResolver = func(ctx context.Context) map[int]string { return map[int]string{4242: "agA"} }

	act := &model.AgentActivity{AgentID: "agA", Activity: model.ActivityUnknown}
	if err := p.Observe(context.Background(), act); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if act.Activity != model.ActivityWriting {
		t.Fatalf("expected WRITING from O_WRONLY event, got %s", act.Activity)
	}
	found := false
	for _, f := range act.OpenFiles {
		if f == "/workspace/proj/a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("observed file missing: %v", act.OpenFiles)
	}
}

func TestTetragonStaleEvidenceIgnored(t *testing.T) {
	dir := t.TempDir()
	rootA := filepath.Join(dir, "wsA")
	os.MkdirAll(rootA, 0o755)
	events := filepath.Join(dir, "events.jsonl")
	old := time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	line := `{"time":"` + old + `","type":3,"process":{"pid":1},"fields":{"path":"/workspace/p/f"}}` + "\n"
	os.WriteFile(events, []byte(line), 0o644)
	p := NewTetragonProvider(events, func() map[string]string { return map[string]string{"agA": rootA} })
	p.PIDResolver = func(ctx context.Context) map[int]string { return map[int]string{1: "agA"} }
	act := &model.AgentActivity{AgentID: "agA", Activity: model.ActivityIdle}
	if err := p.Observe(context.Background(), act); err != nil {
		t.Fatal(err)
	}
	if act.Activity != model.ActivityIdle {
		t.Fatalf("stale evidence must not flip activity, got %s", act.Activity)
	}
}

func TestAttributeRequiresPIDForWorkspacePaths(t *testing.T) {
	roots := map[string]string{"agA": "/r/a", "agB": "/r/b"}
	// /workspace paths are ambiguous without PID attribution.
	if id, _ := attribute("/workspace/proj/f", 1, nil, roots); id != "" {
		t.Fatal("workspace path without pid must not be attributed")
	}
	if id, _ := attribute("/workspace/proj/f", 7, map[int]string{7: "agB"}, roots); id != "agB" {
		t.Fatalf("pid attribution failed: %s", id)
	}
	// Host paths under a root attribute without pid.
	if id, _ := attribute("/r/a/proj/f", 1, nil, roots); id != "agA" {
		t.Fatalf("host path attribution failed: %s", id)
	}
}

type fakeTopper struct{}

func (fakeTopper) TopPIDs(ctx context.Context, cid string) ([]int, error) {
	if cid == "c-agA" {
		return []int{11, 12}, nil
	}
	return []int{21, 22}, nil
}

type fakeLookup struct {
	agents []*model.Agent
}

func (f fakeLookup) ContainerID(ctx context.Context, id string) (string, error) { return "c-" + id, nil }
func (f fakeLookup) Agents(ctx context.Context) ([]*model.Agent, error)         { return f.agents, nil }

func TestBuildPIDMap(t *testing.T) {
	agents := []*model.Agent{{ID: "agA"}, {ID: "agB"}}
	m := BuildPIDMap(fakeLookup{agents: agents}, fakeTopper{})
	if m[11] != "agA" && m[11] != "agB" {
		t.Fatalf("expected pid mapping, got %v", m)
	}
	if len(m) != 4 {
		t.Fatalf("expected 4 pid entries, got %d", len(m))
	}
}
