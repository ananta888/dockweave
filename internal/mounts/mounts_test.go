package mounts

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func writeSampleWithRoot(t *testing.T, root string) string {
	t.Helper()
	sample := `22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw
33 22 0:23 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
45 22 8:1 /workspaces ` + root + `/agents/ag_1/workspace rw,relatime shared:12 - ext4 /dev/sda1 rw
46 45 8:1 /srv/a ` + root + `/agents/ag_1/workspace/proj-a ro,relatime - ext4 /dev/sda1 ro
47 45 8:1 /srv/b ` + root + `/agents/ag_1/workspace/proj-b rw,relatime - ext4 /dev/sda1 rw
48 22 8:1 /dir\040with\040space /mnt/space rw - ext4 /dev/sda1 rw
`
	p := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeSample(t *testing.T) string {
	return writeSampleWithRoot(t, "/var/lib/dockweave")
}

func TestParseMountinfo(t *testing.T) {
	infos, err := ReadMountinfo(writeSample(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(infos) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(infos))
	}
	var ws, pa, pb, sp *MountInfo
	for i := range infos {
		switch infos[i].MountPoint {
		case "/var/lib/dockweave/agents/ag_1/workspace":
			ws = &infos[i]
		case "/var/lib/dockweave/agents/ag_1/workspace/proj-a":
			pa = &infos[i]
		case "/var/lib/dockweave/agents/ag_1/workspace/proj-b":
			pb = &infos[i]
		case "/mnt/space":
			sp = &infos[i]
		}
	}
	if ws == nil || !ws.Shared() {
		t.Fatal("workspace tree should be shared")
	}
	if pa == nil || !pa.Readonly() {
		t.Fatal("proj-a should be ro")
	}
	if pb == nil || pb.Readonly() {
		t.Fatal("proj-b should be rw")
	}
	if sp == nil || sp.Root != "/dir with space" {
		t.Fatalf("octal unescape failed: %+v", sp)
	}
	under := MountsUnder(infos, "/var/lib/dockweave/agents/ag_1/workspace")
	if len(under) != 3 {
		t.Fatalf("expected 3 mounts under tree, got %d", len(under))
	}
}

func TestResolveSourceAllowlist(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	forbidden := filepath.Join(root, "forbidden")
	os.MkdirAll(filepath.Join(allowed, "proj"), 0o755)
	os.MkdirAll(forbidden, 0o755)

	canon, fd, err := ResolveSource(filepath.Join(allowed, "proj"), []string{allowed})
	if err != nil {
		t.Fatalf("allowed path rejected: %v", err)
	}
	defer unix.Close(fd)
	if canon == "" || fd < 0 {
		t.Fatal("empty canonical path or invalid fd")
	}
	// The fd must refer to the verified directory (Dev+Ino match).
	var stPath, stFD unix.Stat_t
	if err := unix.Stat(canon, &stPath); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(fd, &stFD); err != nil {
		t.Fatal(err)
	}
	if stPath.Dev != stFD.Dev || stPath.Ino != stFD.Ino {
		t.Fatal("fd does not refer to the canonical directory")
	}
	if _, _, err := ResolveSource(forbidden, []string{allowed}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("forbidden path accepted: %v", err)
	}
	// Traversal attempt: allowed/../forbidden resolves outside the allowlist.
	if _, _, err := ResolveSource(filepath.Join(allowed, "..", "forbidden"), []string{allowed}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("traversal accepted: %v", err)
	}
	// Symlink escape: symlink inside allowed pointing to forbidden.
	link := filepath.Join(allowed, "evil-link")
	if err := os.Symlink(forbidden, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveSource(link, []string{allowed}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("symlink escape accepted: %v", err)
	}
	// Non-directory must fail.
	f := filepath.Join(allowed, "file.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, _, err := ResolveSource(f, []string{allowed}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("file accepted as mount source: %v", err)
	}
}

func TestSanitizeTargetName(t *testing.T) {
	for _, ok := range []string{"proj-a", "backend", "x", "a.b_c"} {
		if err := SanitizeTargetName(ok); err != nil {
			t.Fatalf("valid name %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../x", "a\x00b"} {
		if err := SanitizeTargetName(bad); err == nil {
			t.Fatalf("invalid name %q accepted", bad)
		}
	}
}

func TestManagerListFromKernelState(t *testing.T) {
	root := t.TempDir()
	m, err := New(Config{Root: root, MountinfoPath: writeSampleWithRoot(t, root)})
	if err != nil {
		t.Fatal(err)
	}
	list, err := m.List("ag_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 mounts (tree root excluded), got %d", len(list))
	}
	modes := map[string]bool{}
	for _, mi := range list {
		modes[string(mi.Mode)] = true
	}
	if !modes["ro"] || !modes["rw"] {
		t.Fatalf("modes not detected: %v", modes)
	}
}

func TestSameBindSource(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	open := func(p string) int {
		fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		return fd
	}
	fdA, fdB := open(a), open(b)
	defer unix.Close(fdA)
	defer unix.Close(fdB)

	same, err := sameBindSource(a, fdA)
	if err != nil {
		t.Fatalf("same: %v", err)
	}
	if !same {
		t.Fatal("dir must be identical to its own fd")
	}
	same, err = sameBindSource(a, fdB)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if same {
		t.Fatal("different dirs must not compare equal")
	}
	if _, err := sameBindSource(filepath.Join(dir, "missing"), fdA); err == nil {
		t.Fatal("expected error for missing target")
	}
}
