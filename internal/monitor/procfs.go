package monitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ananta888/dockweave/internal/model"
)

// OpenFile describes one file held open by a process on the host.
type OpenFile struct {
	PID   int
	Path  string // readlink of /proc/PID/fd/N (host-namespace path)
	Dev   uint64
	Ino   uint64
	Write bool // opened O_WRONLY or O_RDWR
}

// ProcfsProvider observes open files and working directories via /proc.
// It requires read access to /proc of other processes (daemon runs as root).
type ProcfsProvider struct {
	// ProcRoot overrides /proc (tests).
	ProcRoot string
	// WorkspaceRoots maps agent IDs to the host path of their workspace tree.
	WorkspaceRoots func() map[string]string
}

// NewProcfsProvider creates the provider.
func NewProcfsProvider(roots func() map[string]string) *ProcfsProvider {
	return &ProcfsProvider{ProcRoot: "/proc", WorkspaceRoots: roots}
}

// Name implements Provider.
func (p *ProcfsProvider) Name() string { return "procfs" }

// Available implements Provider.
func (p *ProcfsProvider) Available(ctx context.Context) error {
	if st, err := os.Stat(p.ProcRoot); err != nil || !st.IsDir() {
		return fmt.Errorf("%s not readable", p.ProcRoot)
	}
	// Reading our own fd dir proves basic capability; cross-process reads
	// need privilege and are checked lazily per-process.
	if _, err := os.ReadDir(filepath.Join(p.ProcRoot, "self", "fd")); err != nil {
		return errors.New("cannot read /proc/self/fd (restricted?)")
	}
	return nil
}

// Observe implements Provider: open files under the agent's workspace tree
// and the derived activity level.
func (p *ProcfsProvider) Observe(ctx context.Context, act *model.AgentActivity) error {
	roots := p.WorkspaceRoots()
	root, ok := roots[act.AgentID]
	if !ok {
		return nil // no tree known; nothing to attribute
	}
	files, err := ScanOpenFilesUnder(p.ProcRoot, root)
	if err != nil {
		return err
	}
	writing := false
	seen := map[string]bool{}
	var paths []string
	for _, f := range files {
		if !seen[f.Path] {
			seen[f.Path] = true
			paths = append(paths, f.Path)
			if len(paths) >= 20 { // cap payload
				break
			}
		}
		if f.Write {
			writing = true
		}
	}
	act.OpenFiles = paths
	if len(files) > 0 {
		act.Workspace = root
		if writing {
			act.Activity = model.ActivityWriting
		} else {
			act.Activity = model.ActivityReading
		}
	} else if act.Activity == model.ActivityUnknown && act.State == "running" {
		act.Activity = model.ActivityIdle
	}
	return nil
}

// ScanOpenFilesUnder lists files opened by any process whose target path
// lies under root. Entries the process lacks permission for are skipped.
func ScanOpenFilesUnder(procRoot, root string) ([]OpenFile, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", procRoot, err)
	}
	var out []OpenFile
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		out = append(out, scanProcessFDs(procRoot, pid, root)...)
	}
	return out, nil
}

func scanProcessFDs(procRoot string, pid int, root string) []OpenFile {
	fdDir := filepath.Join(procRoot, strconv.Itoa(pid), "fd")
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		return nil // process gone or no permission; skip silently
	}
	var out []OpenFile
	for _, fd := range fds {
		link := filepath.Join(fdDir, fd.Name())
		target, err := os.Readlink(link)
		if err != nil || !hasPathPrefix(target, root) {
			continue
		}
		var st syscall.Stat_t
		if err := syscall.Stat(link, &st); err != nil {
			continue
		}
		write := false
		if info, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "fdinfo", fd.Name())); err == nil {
			write = fdinfoWrite(string(info))
		}
		out = append(out, OpenFile{PID: pid, Path: target, Dev: uint64(st.Dev), Ino: st.Ino, Write: write})
	}
	return out
}

// fdinfoWrite parses the flags line of /proc/PID/fdinfo/N:
// flags are octal; access mode is the lowest two bits (1=WRONLY, 2=RDWR).
func fdinfoWrite(info string) bool {
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "flags:") {
			f := strings.TrimSpace(strings.TrimPrefix(line, "flags:"))
			v, err := strconv.ParseUint(f, 8, 64)
			if err != nil {
				return false
			}
			mode := v & 0o3
			return mode == 0o1 || mode == 0o2
		}
	}
	return false
}

// ConflictDetector finds files opened by processes of multiple agents,
// with at least one writer.
type ConflictDetector struct {
	ProcRoot string
	// Roots maps agent IDs to workspace tree host paths.
	Roots func() map[string]string
}

// NewConflictDetector creates the detector.
func NewConflictDetector() *ConflictDetector {
	return &ConflictDetector{ProcRoot: "/proc"}
}

// Conflicts implements the detection. Returns an empty list (not an error)
// when /proc cannot be fully read; partial evidence beats failure.
func (d *ConflictDetector) Conflicts(ctx context.Context) ([]model.FileConflict, error) {
	if d.Roots == nil {
		return nil, nil
	}
	procRoot := d.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	roots := d.Roots()
	// file identity -> set of agents (writers tracked separately)
	type holder struct {
		agents  map[string]bool
		writers map[string]bool
	}
	byFile := map[string]*holder{}
	for agentID, root := range roots {
		files, err := ScanOpenFilesUnder(procRoot, root)
		if err != nil {
			continue
		}
		for _, f := range files {
			key := fmt.Sprintf("%d:%d", f.Dev, f.Ino)
			h, ok := byFile[key]
			if !ok {
				h = &holder{agents: map[string]bool{}, writers: map[string]bool{}}
				byFile[key] = h
			}
			h.agents[agentID] = true
			if f.Write {
				h.writers[agentID] = true
			}
		}
	}
	var out []model.FileConflict
	for key, h := range byFile {
		if len(h.agents) < 2 || len(h.writers) == 0 {
			continue
		}
		c := model.FileConflict{Path: key, Detected: time.Now()}
		for a := range h.agents {
			c.AgentIDs = append(c.AgentIDs, a)
		}
		for w := range h.writers {
			c.Writers = append(c.Writers, w)
		}
		out = append(out, c)
	}
	return out, nil
}
