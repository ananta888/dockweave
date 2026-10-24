package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// TetragonProvider observes file-access events exported by Tetragon (eBPF).
//
// Integration model: Tetragon runs separately (agent on the host, e.g. via
// the cilium/tetragon container with hostPID). A TracingPolicy captures
// security_file_open events; the tetra CLI or an export container writes
// them as JSON lines to a file or unix socket path configured here
// (DOCKWEAVE_TETRAGON_EVENTS). This provider tails that stream.
//
// Limitations (documented, no false claims):
//   - eBPF event delivery is best-effort; there is no loss-free guarantee
//     over all file operations of all processes.
//   - Only events matching the deployed TracingPolicy are visible.
//   - Container-namespace paths are translated heuristically via the
//     /workspace/<name> layout; attribution additionally uses host PIDs
//     when a PIDResolver is configured.
type TetragonProvider struct {
	// EventsPath is a JSON-lines file produced by a Tetragon export.
	EventsPath string
	// WorkspaceRoots maps agent IDs to host workspace tree paths.
	WorkspaceRoots func() map[string]string
	// PIDResolver optionally maps host PIDs to agent IDs (docker Top based).
	PIDResolver func(ctx context.Context) map[int]string
	// PIDCacheTTL bounds how long a PID snapshot is reused (default 5s).
	// Each refresh *replaces* the snapshot completely, so PIDs of dead or
	// restarted containers disappear (review-006).
	PIDCacheTTL time.Duration

	mu     sync.Mutex
	seen   map[string]fileAccess // key: agentID|path -> latest access
	offset int64                 // JSONL read offset (incremental ingest)
	pidAt  time.Time
	pidMap map[int]string
}

// Evidence strength of a file attribution (review-006).
const (
	EvidenceKernel    = "kernel"    // kernel-reported path (kprobe arg)
	EvidenceHeuristic = "heuristic" // cwd/args inference (fallback only)
)

type fileAccess struct {
	Path       string
	Write      bool
	At         time.Time
	Confidence string
}

// NewTetragonProvider creates the provider; eventsPath may be empty (then
// the provider reports unavailable until configured).
func NewTetragonProvider(eventsPath string, roots func() map[string]string) *TetragonProvider {
	return &TetragonProvider{EventsPath: eventsPath, WorkspaceRoots: roots, seen: map[string]fileAccess{}}
}

// Name implements Provider.
func (p *TetragonProvider) Name() string { return "tetragon" }

// Available implements Provider.
func (p *TetragonProvider) Available(ctx context.Context) error {
	if p.EventsPath == "" {
		return fmt.Errorf("no events path configured (set DOCKWEAVE_TETRAGON_EVENTS)")
	}
	st, err := os.Stat(p.EventsPath)
	if err != nil {
		return fmt.Errorf("events path %q not accessible: %w", p.EventsPath, err)
	}
	if st.IsDir() {
		return fmt.Errorf("events path %q is a directory, expected JSONL file", p.EventsPath)
	}
	return nil
}

// tetragonEvent is a tolerant subset of the Tetragon export JSON.
type tetragonEvent struct {
	Time            string `json:"time"`
	Type            string `json:"event_type"` // older exports: "type"
	TypeNum         *int   `json:"type"`
	FunctionName    string `json:"function_name"`
	Process         struct {
		PID      int      `json:"pid"`
		Binary   string   `json:"binary"`
		Args     []string `json:"args"`
		Cwd      string   `json:"cwd"`
		CapError string   `json:"cap_error"`
	} `json:"process"`
	Fields map[string]string `json:"fields"`
}

func (e *tetragonEvent) kind() string {
	if e.Type != "" {
		return e.Type
	}
	// Numeric event types: 1=process_exec, 2=process_exit, 3=process_kprobe,
	// 4=process_socket...
	switch {
	case e.TypeNum == nil:
		return ""
	case *e.TypeNum == 1:
		return "process_exec"
	case *e.TypeNum == 3:
		return "process_kprobe"
	default:
		return fmt.Sprintf("type_%d", *e.TypeNum)
	}
}

// Observe implements Provider: ingests recent export lines and enriches the
// activity record with observed file accesses. Kernel-reported paths take
// precedence; heuristic (cwd/args) attributions are flagged and used only as
// fallback for the activity level (review-006).
func (p *TetragonProvider) Observe(ctx context.Context, act *model.AgentActivity) error {
	if err := p.Available(ctx); err != nil {
		return err
	}
	p.ingest()
	p.mu.Lock()
	defer p.mu.Unlock()
	var paths []string
	evidence := map[string]string{}
	kernelWrite, heurWrite := false, false
	kernelRead, heurRead := false, false
	for key, fa := range p.seen {
		if !strings.HasPrefix(key, act.AgentID+"|") {
			continue
		}
		if time.Since(fa.At) > 5*time.Minute {
			continue // stale evidence
		}
		paths = append(paths, fa.Path)
		evidence[fa.Path] = fa.Confidence
		kernel := fa.Confidence != EvidenceHeuristic
		switch {
		case fa.Write && kernel:
			kernelWrite = true
		case fa.Write:
			heurWrite = true
		case kernel:
			kernelRead = true
		default:
			heurRead = true
		}
	}
	if len(paths) > 0 {
		act.OpenFiles = append(act.OpenFiles, paths...)
		act.FileEvidence = evidence
		switch {
		case kernelWrite:
			act.Activity = model.ActivityWriting
		case kernelRead:
			act.Activity = model.ActivityReading
		case heurWrite:
			act.Activity = model.ActivityWriting
		case heurRead:
			act.Activity = model.ActivityReading
		}
	}
	return nil
}

// pidSnapshotLocked returns a cached PID map, refreshing (fully replacing)
// it at most every PIDCacheTTL. mu must be held. Replacement semantics drop
// stale entries of dead or restarted containers instead of accumulating
// them (review-006).
func (p *TetragonProvider) pidSnapshotLocked(ctx context.Context) map[int]string {
	if p.PIDResolver == nil {
		return nil
	}
	ttl := p.PIDCacheTTL
	if ttl == 0 {
		ttl = 5 * time.Second
	}
	if p.pidMap != nil && time.Now().Before(p.pidAt) {
		return p.pidMap
	}
	p.pidMap = p.PIDResolver(ctx) // full replace: stale PIDs are dropped
	p.pidAt = time.Now().Add(ttl)
	return p.pidMap
}

// ingest reads new lines from the export file and records file accesses.
// The read offset advances only past complete lines; a truncation resets it.
// Historical lines are never re-attributed with a newer PID map.
func (p *TetragonProvider) ingest() {
	f, err := os.Open(p.EventsPath)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if st.Size() < p.offset {
		p.offset = 0 // file rotated/truncated
	}
	if _, err := f.Seek(p.offset, 0); err != nil {
		return
	}
	var pidMap map[int]string
	if p.PIDResolver != nil {
		pidMap = p.pidSnapshotLocked(context.Background())
	}
	roots := p.WorkspaceRoots()
	r := bufio.NewReader(f)
	var consumed int64
	for {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 {
			break
		}
		if line[len(line)-1] != '\n' {
			break // incomplete last line: re-read on next ingest
		}
		consumed += int64(len(line))
		p.ingestLine(line[:len(line)-1], pidMap, roots)
		if err != nil {
			break
		}
	}
	// Bound memory: drop evidence older than 10 minutes.
	for k, fa := range p.seen {
		if time.Since(fa.At) > 10*time.Minute {
			delete(p.seen, k)
		}
	}
	p.offset += consumed
}

// ingestLine parses and records one export line.
func (p *TetragonProvider) ingestLine(line []byte, pidMap map[int]string, roots map[string]string) {
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var ev tetragonEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return // tolerate non-JSON noise in shared export files
	}
	if ev.kind() != "process_kprobe" && ev.kind() != "process_exec" {
		return
	}
	ts := time.Now()
	if t, err := time.Parse(time.RFC3339Nano, ev.Time); err == nil {
		ts = t
	}
	// Kernel-reported paths (kprobe args) are strong evidence; cwd and
	// args are heuristics and always flagged as such.
	write := strings.Contains(ev.Fields["open_flags"], "WRONLY") ||
		strings.Contains(ev.Fields["open_flags"], "RDWR")
	kernelCands := []string{ev.Fields["path"], ev.Fields["filename"]}
	heurCands := append([]string{ev.Process.Cwd}, ev.Process.Args...)
	heurWrite := write || strings.Contains(strings.Join(ev.Process.Args, " "), ">")
	p.recordCandidates(kernelCands, ev.Process.PID, pidMap, roots, EvidenceKernel, write, ts)
	p.recordCandidates(heurCands, ev.Process.PID, pidMap, roots, EvidenceHeuristic, heurWrite, ts)
}

// recordCandidates attributes candidate paths and stores the evidence.
// Kernel confidence is never downgraded by a later heuristic match; a later
// kernel match upgrades a heuristic one.
func (p *TetragonProvider) recordCandidates(cands []string, pid int, pidMap map[int]string, roots map[string]string, conf string, write bool, ts time.Time) {
	for _, cand := range cands {
		if cand == "" {
			continue
		}
		agentID, hostPath := attribute(cand, pid, pidMap, roots)
		if agentID == "" {
			continue
		}
		key := agentID + "|" + hostPath
		if old, ok := p.seen[key]; ok {
			if old.Confidence == EvidenceKernel && conf == EvidenceHeuristic {
				// Keep kernel evidence; merge write flag and timestamp.
				old.Write = old.Write || write
				old.At = ts
				p.seen[key] = old
				continue
			}
		}
		p.seen[key] = fileAccess{Path: hostPath, Write: write, At: ts, Confidence: conf}
	}
}

// attribute maps an observed path/pid to an agent ID and a host path.
// Container paths look like /workspace/<name>/...; host paths must fall
// under a workspace tree. PID attribution has precedence.
func attribute(path string, pid int, pidMap map[int]string, roots map[string]string) (agentID, hostPath string) {
	if pidMap != nil {
		if id, ok := pidMap[pid]; ok {
			return id, path
		}
	}
	if strings.HasPrefix(path, "/workspace/") {
		// The segment after /workspace/ is the target name (workspace name),
		// shared across agents; ambiguity is resolved only via PID map above.
		// Without PID data we skip: attributing to all agents would be guesswork.
		return "", ""
	}
	for id, root := range roots {
		if hasPathPrefix(path, root) {
			return id, path
		}
	}
	return "", ""
}

// BuildPIDMap maps host PIDs of all agent containers to agent IDs using
// docker top. Best effort: containers that cannot be queried are skipped.
func BuildPIDMap(lookup AgentLookupExtended, docker ContainerTopper) map[int]string {
	out := map[int]string{}
	if lookup == nil || docker == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agents, err := lookup.Agents(ctx)
	if err != nil {
		return out
	}
	for _, a := range agents {
		cid, err := lookup.ContainerID(ctx, a.ID)
		if err != nil || cid == "" {
			continue
		}
		pids, err := docker.TopPIDs(ctx, cid)
		if err != nil {
			continue
		}
		for _, pid := range pids {
			out[pid] = a.ID
		}
	}
	return out
}

// AgentLookupExtended supplies agent list + container IDs for PID mapping.
type AgentLookupExtended interface {
	AgentLookup
	Agents(ctx context.Context) ([]*model.Agent, error)
}

// ContainerTopper abstracts docker top.
type ContainerTopper interface {
	TopPIDs(ctx context.Context, containerID string) ([]int, error)
}

var _ Provider = (*TetragonProvider)(nil)

// ensure filepath import usage for future path joining helpers
var _ = filepath.Join
