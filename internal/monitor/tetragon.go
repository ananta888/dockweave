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

	mu   sync.Mutex
	seen map[string]fileAccess // key: agentID|path -> latest access
}

type fileAccess struct {
	Path    string
	Write   bool
	At      time.Time
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
// activity record with observed file accesses.
func (p *TetragonProvider) Observe(ctx context.Context, act *model.AgentActivity) error {
	if err := p.Available(ctx); err != nil {
		return err
	}
	p.ingest()
	p.mu.Lock()
	defer p.mu.Unlock()
	var paths []string
	writing := false
	for key, fa := range p.seen {
		if !strings.HasPrefix(key, act.AgentID+"|") {
			continue
		}
		if time.Since(fa.At) > 5*time.Minute {
			continue // stale evidence
		}
		paths = append(paths, fa.Path)
		if fa.Write {
			writing = true
		}
	}
	if len(paths) > 0 {
		act.OpenFiles = append(act.OpenFiles, paths...)
		act.Activity = model.ActivityReading
		if writing {
			act.Activity = model.ActivityWriting
		}
	}
	return nil
}

// ingest reads all lines currently present in the export file and records
// file accesses. Files rotate; partial last lines are tolerated.
func (p *TetragonProvider) ingest() {
	f, err := os.Open(p.EventsPath)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	var pidMap map[int]string
	if p.PIDResolver != nil {
		pidMap = p.PIDResolver(context.Background())
	}
	roots := p.WorkspaceRoots()
	p.mu.Lock()
	defer p.mu.Unlock()
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev tetragonEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // tolerate non-JSON noise in shared export files
		}
		if ev.kind() != "process_kprobe" && ev.kind() != "process_exec" {
			continue
		}
		ts := time.Now()
		if t, err := time.Parse(time.RFC3339Nano, ev.Time); err == nil {
			ts = t
		}
		// Candidate paths: explicit field, cwd, binary, args.
		candidates := []string{ev.Fields["path"], ev.Process.Cwd, ev.Fields["filename"]}
		candidates = append(candidates, ev.Process.Args...)
		write := strings.Contains(ev.Fields["open_flags"], "WRONLY") ||
			strings.Contains(ev.Fields["open_flags"], "RDWR") ||
			strings.Contains(strings.Join(ev.Process.Args, " "), ">")
		for _, cand := range candidates {
			if cand == "" {
				continue
			}
			agentID, hostPath := attribute(cand, ev.Process.PID, pidMap, roots)
			if agentID == "" {
				continue
			}
			key := agentID + "|" + hostPath
			p.seen[key] = fileAccess{Path: hostPath, Write: write, At: ts}
		}
	}
	// Bound memory: drop evidence older than 10 minutes.
	for k, fa := range p.seen {
		if time.Since(fa.At) > 10*time.Minute {
			delete(p.seen, k)
		}
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
