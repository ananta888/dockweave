// Package memory implements snapshotting, restore and retention for
// long-term memory stores. Memories have their own identity (memory IDs),
// decoupled from agent identities (review-009). Snapshots are deterministic
// tar.gz archives stored under <root>/snapshots/<memoryID>/. All methods
// key by memory ID.
package memory

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrBusy is returned when the owning agent is running and a mutation of
// the memory store is unsafe.
var ErrBusy = errors.New("agent is running")

// ErrNotFound is returned when a snapshot does not exist.
var ErrNotFound = errors.New("snapshot not found")

// Manager performs snapshot operations on memory directories.
type Manager struct {
	// Root is the dockweave state root (snapshots live under <root>/snapshots).
	Root string
	// MemoryDir resolves the host path of a memory ID.
	MemoryDir func(memoryID string) string
}

// New creates a Manager.
func New(root string, memoryDir func(string) string) (*Manager, error) {
	if root == "" || memoryDir == nil {
		return nil, errors.New("memory: root and memoryDir are required")
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0o700); err != nil {
		return nil, fmt.Errorf("create snapshots dir: %w", err)
	}
	return &Manager{Root: root, MemoryDir: memoryDir}, nil
}

// Snapshot describes one stored snapshot archive.
type Snapshot struct {
	MemoryID  string    `json:"memory_id"`
	Name      string    `json:"name"` // file name (timestamp-based)
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

func (m *Manager) snapshotsDir(memoryID string) string {
	return filepath.Join(m.Root, "snapshots", memoryID)
}

// Path returns the host directory of a memory store.
func (m *Manager) Path(memoryID string) string { return m.MemoryDir(memoryID) }

// EnsureDir creates the memory store directory if missing.
func (m *Manager) EnsureDir(memoryID string) error {
	if err := os.MkdirAll(m.MemoryDir(memoryID), 0o700); err != nil {
		return fmt.Errorf("create memory dir for %s: %w", memoryID, err)
	}
	return nil
}

// Create writes a tar.gz snapshot of the agent's memory directory.
// The agent should be stopped (running agents may mutate files concurrently);
// callers enforce this — Create itself refuses nothing for testability but
// documents the contract.
func (m *Manager) Create(memoryID string) (*Snapshot, error) {
	src := m.MemoryDir(memoryID)
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("memory dir: %w", err)
	}
	dir := m.snapshotsDir(memoryID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}
	name := time.Now().UTC().Format("20060102-150405.000") + ".tar.gz"
	tmp := filepath.Join(dir, "."+name)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create snapshot file: %w", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	// Walk memory dir; archive with relative paths.
	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Uname, hdr.Gname = "", "" // deterministic archives
		if info.Mode()&os.ModeSymlink != 0 {
			link, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			hdr.Linkname = link
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			rf, rerr := os.Open(path)
			if rerr != nil {
				return rerr
			}
			_, rerr = io.Copy(tw, rf)
			rf.Close()
			if rerr != nil {
				return rerr
			}
		}
		return nil
	})
	if err != nil {
		tw.Close()
		gz.Close()
		os.Remove(tmp)
		return nil, fmt.Errorf("archive memory: %w", err)
	}
	if err := tw.Close(); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("close file: %w", err)
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("finalize snapshot: %w", err)
	}
	st, _ := os.Stat(final)
	snap := &Snapshot{MemoryID: memoryID, Name: name, Path: final, CreatedAt: time.Now().UTC()}
	if st != nil {
		snap.SizeBytes = st.Size()
		snap.CreatedAt = st.ModTime()
	}
	return snap, nil
}

// List returns snapshots of an agent, newest first.
func (m *Manager) List(memoryID string) ([]Snapshot, error) {
	dir := m.snapshotsDir(memoryID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshots: %w", err)
	}
	var out []Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Snapshot{
			MemoryID: memoryID, Name: e.Name(), Path: filepath.Join(dir, e.Name()),
			SizeBytes: info.Size(), CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Restore extracts a snapshot into the agent's memory directory. The target
// directory is REPLACED (previous contents moved to a .bak-<ts> sibling).
func (m *Manager) Restore(memoryID, name string) error {
	src := filepath.Join(m.snapshotsDir(memoryID), name)
	f, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	dst := m.MemoryDir(memoryID)
	if _, err := os.Stat(dst); err == nil {
		bak := dst + ".bak-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(dst, bak); err != nil {
			return fmt.Errorf("backup existing memory: %w", err)
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("recreate memory dir: %w", err)
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		// Path traversal protection: reject absolute paths and ..
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(hdr.Name) {
			return fmt.Errorf("snapshot contains unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode).Perm()); err != nil {
				return fmt.Errorf("mkdir %q: %w", clean, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			wf, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return fmt.Errorf("create %q: %w", clean, err)
			}
			if _, err := io.Copy(wf, tr); err != nil {
				wf.Close()
				return fmt.Errorf("write %q: %w", clean, err)
			}
			wf.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %q: %w", clean, err)
			}
		default:
			// skip unknown entry types (fifo, dev...) — never create devices
		}
	}
	return nil
}

// Delete removes one snapshot.
func (m *Manager) Delete(memoryID, name string) error {
	p := filepath.Join(m.snapshotsDir(memoryID), name)
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// RetentionPolicy defines when old snapshots are pruned.
type RetentionPolicy struct {
	MaxCount int           // keep at most N newest (0 = unlimited)
	MaxAge   time.Duration // delete older than (0 = unlimited)
}

// ApplyRetention prunes snapshots of an agent according to the policy.
// Returns the names of removed snapshots.
func (m *Manager) ApplyRetention(memoryID string, pol RetentionPolicy) ([]string, error) {
	list, err := m.List(memoryID)
	if err != nil {
		return nil, err
	}
	var removed []string
	cutoff := time.Time{}
	if pol.MaxAge > 0 {
		cutoff = time.Now().Add(-pol.MaxAge)
	}
	for i, s := range list { // list is newest first
		overCount := pol.MaxCount > 0 && i >= pol.MaxCount
		overAge := pol.MaxAge > 0 && s.CreatedAt.Before(cutoff)
		if !overCount && !overAge {
			continue
		}
		if err := m.Delete(memoryID, s.Name); err != nil {
			return removed, err
		}
		removed = append(removed, s.Name)
	}
	return removed, nil
}
