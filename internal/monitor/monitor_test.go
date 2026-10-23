package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ananta888/dockweave/internal/model"
)

func TestFdinfoWrite(t *testing.T) {
	cases := []struct {
		info string
		want bool
	}{
		{"pos:\t0\nflags:\t0100002\nmnt_id:\t23\n", true},  // 0o100002 -> access 2 (RDWR) -> write
		{"pos:\t0\nflags:\t0100000\nmnt_id:\t23\n", false}, // 0o100000 -> access 0 (RDONLY)
		{"pos:\t0\nflags:\t0100001\nmnt_id:\t23\n", true},  // 0o100001 -> access 1 (WRONLY)
		{"pos:\t0\nflags:\t02\nmnt_id:\t23\n", true},       // plain O_RDWR
		{"garbage", false},
	}
	for i, c := range cases {
		if got := fdinfoWrite(c.info); got != c.want {
			t.Fatalf("case %d (%q): got %v want %v", i, c.info, got, c.want)
		}
	}
}

func TestHasPathPrefix(t *testing.T) {
	if !hasPathPrefix("/a/b/c", "/a/b") {
		t.Fatal("prefix match failed")
	}
	if hasPathPrefix("/a/bc", "/a/b") {
		t.Fatal("partial component prefix must not match")
	}
	if hasPathPrefix("/a", "/a/b") {
		t.Fatal("shorter path must not match")
	}
}

func TestScanOpenFilesUnder(t *testing.T) {
	// Synthetic /proc with one process holding an fd into root.
	proc := t.TempDir()
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(root, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "proj", "f.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pdir := filepath.Join(proc, "42", "fd")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "42", "fdinfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	// fd 3 -> target (write), fd 4 -> outside root
	if err := os.Symlink(target, filepath.Join(pdir, "3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(pdir, "4")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "42", "fdinfo", "3"), []byte("pos:\t0\nflags:\t0100001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ScanOpenFilesUnder(proc, root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file under root, got %d (%+v)", len(files), files)
	}
	if !files[0].Write {
		t.Fatal("write flag not detected")
	}
	if files[0].Path != target {
		t.Fatalf("path: %s != %s", files[0].Path, target)
	}
}

func TestConflictDetector(t *testing.T) {
	proc := t.TempDir()
	rootA := filepath.Join(t.TempDir(), "wsA")
	rootB := filepath.Join(t.TempDir(), "wsB")
	os.MkdirAll(rootA, 0o755)
	os.MkdirAll(rootB, 0o755)

	// The same physical file (same inode) reachable under both roots via hardlinks.
	fileA := filepath.Join(rootA, "common.txt")
	os.WriteFile(fileA, []byte("x"), 0o644)
	fileB := filepath.Join(rootB, "common.txt")
	if err := os.Link(fileA, fileB); err != nil {
		t.Fatal(err)
	}

	mk := func(pid int, target string, write bool) {
		pdir := filepath.Join(proc, itoa(pid), "fd")
		os.MkdirAll(pdir, 0o755)
		os.MkdirAll(filepath.Join(proc, itoa(pid), "fdinfo"), 0o755)
		os.Symlink(target, filepath.Join(pdir, "3"))
		flag := "0100000"
		if write {
			flag = "0100001"
		}
		os.WriteFile(filepath.Join(proc, itoa(pid), "fdinfo", "3"), []byte("flags:\t"+flag+"\n"), 0o644)
	}
	mk(1, fileA, true)  // agent A writes
	mk(2, fileB, false) // agent B reads (same inode)

	d := &ConflictDetector{
		ProcRoot: proc,
		Roots: func() map[string]string {
			return map[string]string{"agA": rootA, "agB": rootB}
		},
	}
	conf, err := d.Conflicts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(conf) != 1 {
		t.Fatalf("expected 1 conflict, got %d (%+v)", len(conf), conf)
	}
	if len(conf[0].Writers) == 0 {
		t.Fatalf("conflict without writers: %+v", conf[0])
	}
	if len(conf[0].AgentIDs) != 2 {
		t.Fatalf("expected 2 agents in conflict, got %v", conf[0].AgentIDs)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestMonitorCollectDegrades(t *testing.T) {
	// A provider that reports unavailable must degrade the record, not fail.
	m := New(&brokenProvider{})
	out := m.Collect(context.Background(), []*model.Agent{{ID: "ag1", Name: "a", State: model.AgentRunning}})
	if len(out) != 1 {
		t.Fatal("expected 1 record")
	}
	if out[0].Unavailable == "" {
		t.Fatal("unavailable reason must be recorded")
	}
}

type brokenProvider struct{}

func (brokenProvider) Name() string { return "broken" }

func (brokenProvider) Available(ctx context.Context) error { return nil }

func (brokenProvider) Observe(ctx context.Context, act *model.AgentActivity) error {
	return errors.New("forced failure")
}
