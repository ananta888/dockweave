package mounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/model"
)

// call records one backend operation for sequence assertions.
type call struct {
	op    string
	src   string
	dst   string
	flags uintptr
}

// recBackend is a fake backend that records calls and can inject a
// bind failure when MS_NOSYMFOLLOW is requested (to exercise fail-closed).
type recBackend struct {
	calls              []call
	bindNoSymfollowErr error
}

func (r *recBackend) Bind(src, dst string, flags uintptr) error {
	if r.bindNoSymfollowErr != nil && flags&unix.MS_NOSYMFOLLOW != 0 {
		return r.bindNoSymfollowErr
	}
	r.calls = append(r.calls, call{op: "bind", src: src, dst: dst, flags: flags})
	return nil
}
func (r *recBackend) Remount(dst string, flags uintptr) error {
	r.calls = append(r.calls, call{op: "remount", dst: dst, flags: flags})
	return nil
}
func (r *recBackend) Move(src, dst string) error {
	r.calls = append(r.calls, call{op: "move", src: src, dst: dst})
	return nil
}
func (r *recBackend) Unmount(dst string, flags int) error {
	r.calls = append(r.calls, call{op: "unmount", dst: dst, flags: uintptr(flags)})
	return nil
}
func (r *recBackend) Propagation(dst string, flags uintptr) error {
	r.calls = append(r.calls, call{op: "propagation", dst: dst, flags: flags})
	return nil
}
func (r *recBackend) SetReadonly(dst string, recursive bool) error {
	flags := uintptr(0)
	if recursive {
		flags = unix.AT_RECURSIVE
	}
	r.calls = append(r.calls, call{op: "setreadonly", dst: dst, flags: flags})
	return nil
}
func (r *recBackend) ChownTree(path string, uid, gid int) error { return nil }

func TestMountNoSymfollowFailsClosed(t *testing.T) {
	rec := &recBackend{bindNoSymfollowErr: unix.EINVAL}
	m := &Manager{sys: rec}

	err := m.mountNoSymfollow("/src", "/dst", unix.MS_BIND|unix.MS_REC)
	if !errors.Is(err, ErrSymlinkProtectionUnavailable) {
		t.Fatalf("err = %v, want ErrSymlinkProtectionUnavailable", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("expected no fallback bind on NOSYMFOLLOW failure, got %d calls", len(rec.calls))
	}
}

func TestAddROStagingSequenceMakesPrivate(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	if err := os.MkdirAll(filepath.Join(allowed, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := &recBackend{}
	m := &Manager{
		cfg:     Config{Root: root, MountinfoPath: writeSampleWithRoot(t, root)},
		locks:   map[string]*sync.Mutex{},
		allowed: []string{allowed},
		sys:     rec,
	}

	if _, err := m.Add("ag_1", filepath.Join(allowed, "proj"), "proj", model.ModeRO); err != nil {
		t.Fatalf("Add: %v", err)
	}

	staging := filepath.Join(root, ".staging", "ag_1-proj")
	target := filepath.Join(root, "agents", "ag_1", "workspace", "proj")

	// Expected sequence: bind -> make private -> remount ro -> setreadonly -> move.
	wantOps := []string{"bind", "propagation", "remount", "setreadonly", "move"}
	if len(rec.calls) != len(wantOps) {
		t.Fatalf("got %d calls %+v, want %d ops", len(rec.calls), rec.calls, len(wantOps))
	}
	for i, op := range wantOps {
		if rec.calls[i].op != op {
			t.Fatalf("call[%d] = %q, want %q (%+v)", i, rec.calls[i].op, op, rec.calls[i])
		}
	}

	// Bind source must be the verified-fd magic link (TOCTOU pin).
	if !strings.HasPrefix(rec.calls[0].src, "/proc/self/fd/") {
		t.Fatalf("bind source = %q, want /proc/self/fd/<n>", rec.calls[0].src)
	}
	if rec.calls[0].dst != staging {
		t.Fatalf("bind dst = %q, want staging %q", rec.calls[0].dst, staging)
	}
	if rec.calls[0].flags&unix.MS_BIND == 0 || rec.calls[0].flags&unix.MS_REC == 0 {
		t.Fatalf("bind flags = %#x, want MS_BIND|MS_REC|MS_NOSYMFOLLOW", rec.calls[0].flags)
	}

	// Staging must be made private recursively (AGENTS.md rule 2).
	if rec.calls[1].dst != staging {
		t.Fatalf("propagation dst = %q, want staging", rec.calls[1].dst)
	}
	if rec.calls[1].flags&unix.MS_PRIVATE == 0 || rec.calls[1].flags&unix.MS_REC == 0 {
		t.Fatalf("propagation flags = %#x, want MS_PRIVATE|MS_REC", rec.calls[1].flags)
	}

	// Remount must be read-only.
	if rec.calls[2].flags&unix.MS_RDONLY == 0 {
		t.Fatalf("remount flags = %#x, want MS_RDONLY", rec.calls[2].flags)
	}

	// Move from staging into the shared tree.
	if rec.calls[4].src != staging || rec.calls[4].dst != target {
		t.Fatalf("move = %q -> %q, want %q -> %q", rec.calls[4].src, rec.calls[4].dst, staging, target)
	}
}
