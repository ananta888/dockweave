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

func (f fakeLookup) ContainerID(ctx context.Context, id string) (string, error) {
	return "c-" + id, nil
}
func (f fakeLookup) Agents(ctx context.Context) ([]*model.Agent, error) { return f.agents, nil }

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

// TestTetragonConfidenceKernelVsHeuristic (review-006): kernel-reported
// paths must be flagged "kernel", cwd/args inferences "heuristic", and a
// later heuristic match must never downgrade kernel evidence.
func TestTetragonConfidenceKernelVsHeuristic(t *testing.T) {
	dir := t.TempDir()
	rootA := filepath.Join(dir, "wsA")
	os.MkdirAll(rootA, 0o755)
	events := filepath.Join(dir, "events.jsonl")

	// Kernel path field: /workspace/proj/a.txt (kernel evidence, write).
	// Args mention a different file: only heuristic evidence.
	line := `{"time":"` + time.Now().Format(time.RFC3339Nano) + `","type":3,` +
		`"function_name":"security_file_open","process":{"pid":4242,"binary":"/bin/sh","args":["cat","/workspace/proj/other.txt"]},` +
		`"fields":{"path":"/workspace/proj/a.txt","open_flags":"O_WRONLY"}}` + "\n"
	if err := os.WriteFile(events, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewTetragonProvider(events, func() map[string]string { return map[string]string{"agA": rootA} })
	p.PIDResolver = func(ctx context.Context) map[int]string { return map[int]string{4242: "agA"} }

	act := &model.AgentActivity{AgentID: "agA"}
	if err := p.Observe(context.Background(), act); err != nil {
		t.Fatal(err)
	}
	if act.FileEvidence["/workspace/proj/a.txt"] != EvidenceKernel {
		t.Fatalf("a.txt evidence = %q, want kernel", act.FileEvidence["/workspace/proj/a.txt"])
	}
	if act.FileEvidence["/workspace/proj/other.txt"] != EvidenceHeuristic {
		t.Fatalf("other.txt evidence = %q, want heuristic", act.FileEvidence["/workspace/proj/other.txt"])
	}
	if act.Activity != model.ActivityWriting {
		t.Fatalf("kernel write evidence must drive activity, got %s", act.Activity)
	}
}

// TestTetragonKernelFileWriteWithoutArgs (review-006): a file-write event
// with only kernel fields (no args, no cwd) must attribute correctly via
// the PID map — no args heuristics required.
func TestTetragonKernelFileWriteWithoutArgs(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.jsonl")
	line := `{"time":"` + time.Now().Format(time.RFC3339Nano) + `","type":3,` +
		`"function_name":"security_file_open","process":{"pid":4242},` +
		`"fields":{"path":"/workspace/proj/out.txt","open_flags":"O_RDWR"}}` + "\n"
	if err := os.WriteFile(events, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewTetragonProvider(events, func() map[string]string { return map[string]string{} })
	p.PIDResolver = func(ctx context.Context) map[int]string { return map[int]string{4242: "agA"} }

	act := &model.AgentActivity{AgentID: "agA"}
	if err := p.Observe(context.Background(), act); err != nil {
		t.Fatal(err)
	}
	if act.Activity != model.ActivityWriting {
		t.Fatalf("kernel O_RDWR event must yield WRITING, got %s", act.Activity)
	}
	if act.FileEvidence["/workspace/proj/out.txt"] != EvidenceKernel {
		t.Fatalf("evidence = %v, want kernel for the kprobe path", act.FileEvidence)
	}
	if len(act.OpenFiles) != 1 {
		t.Fatalf("expected exactly the kernel path, got %v", act.OpenFiles)
	}
}

// TestTetragonPIDMapReplaceDropsStaleEntries (review-006): PID snapshots
// are fully replaced on refresh — entries of dead/restarted containers do
// not linger and cannot attribute new events.
func TestTetragonPIDMapReplaceDropsStaleEntries(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(events, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewTetragonProvider(events, func() map[string]string { return map[string]string{} })
	p.PIDCacheTTL = time.Nanosecond // refresh on every ingest
	resolved := map[int]string{4242: "agA"}
	p.PIDResolver = func(ctx context.Context) map[int]string {
		out := map[int]string{}
		for k, v := range resolved {
			out[k] = v
		}
		return out
	}

	// First ingest: PID 4242 belongs to agA.
	p.mu.Lock()
	first := p.pidSnapshotLocked(context.Background())
	p.mu.Unlock()
	if first[4242] != "agA" {
		t.Fatalf("initial snapshot: %v", first)
	}

	// Container restart: the PID is gone. The next refresh must REPLACE
	// the map, not merge into it.
	resolved = map[int]string{}
	time.Sleep(time.Millisecond)
	p.mu.Lock()
	second := p.pidSnapshotLocked(context.Background())
	p.mu.Unlock()
	if _, ok := second[4242]; ok {
		t.Fatalf("stale PID entry survived refresh: %v", second)
	}
	if len(second) != 0 {
		t.Fatalf("expected empty replacement snapshot, got %v", second)
	}

	// A new event with the dead PID must not be attributed.
	line := `{"time":"` + time.Now().Format(time.RFC3339Nano) + `","type":3,` +
		`"process":{"pid":4242},` +
		`"fields":{"path":"/workspace/proj/x.txt","open_flags":"O_RDONLY"}}` + "\n"
	f, err := os.OpenFile(events, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()

	act := &model.AgentActivity{AgentID: "agA"}
	if err := p.Observe(context.Background(), act); err != nil {
		t.Fatal(err)
	}
	if len(act.OpenFiles) != 0 {
		t.Fatalf("dead PID must not attribute events, got %v", act.OpenFiles)
	}
}
