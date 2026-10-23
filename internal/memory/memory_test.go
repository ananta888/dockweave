package memory

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	mem := filepath.Join(root, "agents", "ag_1", "memory")
	if err := os.MkdirAll(filepath.Join(mem, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "db.sqlite"), []byte("fake-sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "sessions", "s1.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := New(root, func(id string) string { return filepath.Join(root, "agents", id, "memory") })
	if err != nil {
		t.Fatal(err)
	}
	return m, mem
}

func TestSnapshotCreateListRestore(t *testing.T) {
	m, mem := setup(t)

	snap, err := m.Create("ag_1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if snap.SizeBytes == 0 {
		t.Fatal("empty snapshot")
	}

	list, err := m.List("ag_1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	// Mutate memory (delete files), then restore.
	if err := os.RemoveAll(mem); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore("ag_1", snap.Name); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(mem, "db.sqlite")); err != nil || string(b) != "fake-sqlite" {
		t.Fatalf("restored content wrong: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(mem, "sessions", "s1.json")); err != nil {
		t.Fatalf("nested file not restored: %v", err)
	}
	// The restore created a .bak of the (empty) previous memory — acceptable.
}

func TestRestoreRejectsTraversal(t *testing.T) {
	m, _ := setup(t)
	if _, err := m.Create("ag_1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore("ag_1", "nope.tar.gz"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Craft malicious archive with a traversal entry ("../escaped.txt").
	dir := m.snapshotsDir("ag_1")
	evil := filepath.Join(dir, "evil.tar.gz")
	if err := writeEvilArchive(evil); err != nil {
		t.Fatalf("craft evil archive: %v", err)
	}
	if err := m.Restore("ag_1", "evil.tar.gz"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("traversal must be rejected, got %v", err)
	}
	// Cleanup so Retention test doesn't see the fake archive.
	os.Remove(evil)
}

func writeEvilArchive(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	hdr := &tar.Header{Name: "../escaped.txt", Mode: 0o644, Size: 4}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write([]byte("evil")); err != nil {
		return err
	}
	return nil
}

func TestRetention(t *testing.T) {
	m, _ := setup(t)
	// create 3 snapshots
	for i := 0; i < 3; i++ {
		if _, err := m.Create("ag_1"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	removed, err := m.ApplyRetention("ag_1", RetentionPolicy{MaxCount: 2})
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removal, got %v", removed)
	}
	list, _ := m.List("ag_1")
	if len(list) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(list))
	}
	// Age the remaining snapshots 2h into the past, then MaxAge=1h removes both.
	old := time.Now().Add(-2 * time.Hour)
	for _, s := range list {
		if err := os.Chtimes(s.Path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, err = m.ApplyRetention("ag_1", RetentionPolicy{MaxAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removals by age, got %v", removed)
	}
}
