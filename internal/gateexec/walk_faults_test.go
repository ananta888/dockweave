package gateexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// RF-008/RF-009/RF-011: deterministic faults injected through the walk's
// visit callback (no sleeps, no timing).

func faultTree(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"grow.txt": "a", "d/x.txt": "x"} {
		if err := os.WriteFile(filepath.Join(ws, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func walkWith(t *testing.T, ws string, visit func(treeEntry) error) error {
	t.Helper()
	r, err := openRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	return r.walk(context.Background(), nil, visit)
}

func TestWalkRejectsFileGrowingDuringRead(t *testing.T) {
	ws := faultTree(t)
	err := walkWith(t, ws, func(e treeEntry) error {
		if e.rel == "grow.txt" {
			f, err := os.OpenFile(filepath.Join(ws, "grow.txt"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString(strings.Repeat("b", 4096))
			f.Close()
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "changed during read") {
		t.Fatalf("growing file accepted: %v", err)
	}
}

func TestWalkRejectsEntrySwappedForFIFOAfterEnumeration(t *testing.T) {
	ws := faultTree(t)
	err := walkWith(t, ws, func(e treeEntry) error {
		if e.rel == "d" { // d is enumerated now, its children only afterwards
			target := filepath.Join(ws, "d", "x.txt")
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(target, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "special workspace file") {
		t.Fatalf("FIFO swapped in after enumeration accepted: %v", err)
	}
}

func TestWalkNeverFollowsEntrySwappedForSymlink(t *testing.T) {
	ws := faultTree(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	var sawLink bool
	err := walkWith(t, ws, func(e treeEntry) error {
		if e.rel == "d" {
			target := filepath.Join(ws, "d", "x.txt")
			_ = os.Remove(target)
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
		}
		if e.rel == filepath.Join("d", "x.txt") {
			if e.file != nil {
				t.Fatal("swapped symlink was opened as a file")
			}
			sawLink = e.link == outside
		}
		return nil
	})
	if err != nil || !sawLink {
		t.Fatalf("symlink must be reported by its text, never followed: link=%v err=%v", sawLink, err)
	}
}

func TestWalkFailsOnRootReplacedDuringWalk(t *testing.T) {
	ws := faultTree(t)
	parent := filepath.Dir(ws)
	moved := filepath.Join(parent, "moved")
	err := walkWith(t, ws, func(e treeEntry) error {
		if e.rel == "grow.txt" {
			if err := os.Rename(ws, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(ws, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	if err == nil {
		t.Fatal("replaced workspace root went unnoticed")
	}
}

// RF-011: unreadable directories are errors, never "absent"; hashing and the
// fingerprint fail instead of silently skipping content.
func TestUnreadableDirectoryFailsHashingAndFingerprint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict root")
	}
	ws := faultTree(t)
	locked := filepath.Join(ws, "d")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := hashSelected(context.Background(), ws, nil, nil); err == nil {
		t.Fatal("hashing skipped an unreadable directory")
	}
	if _, err := Fingerprint(context.Background(), ws, FingerprintLimits{}); err == nil {
		t.Fatal("fingerprint skipped an unreadable directory")
	}
	if _, err := hashSelected(context.Background(), ws, nil, []string{"d/x.txt"}); err == nil {
		t.Fatal("selected hash of an unreadable file reported as absent")
	}
}

// RF-009: directories count toward MaxFiles, so a tree of empty directories
// cannot keep the fingerprint busy until the timeout.
func TestFingerprintCountsDirectoriesTowardMaxFiles(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 50; i++ {
		if err := os.MkdirAll(filepath.Join(ws, "d", strings.Repeat("x", i+1)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Fingerprint(context.Background(), ws, FingerprintLimits{MaxFiles: 10}); err == nil {
		t.Fatal("51 directories accepted with MaxFiles=10")
	}
	if _, err := Fingerprint(context.Background(), ws, FingerprintLimits{MaxFiles: 100}); err != nil {
		t.Fatalf("within limit: %v", err)
	}
}

// Large directories are read in batches; cancellation is honoured between
// batches instead of after reading everything.
func TestWalkHonoursCancellationInLargeDirectory(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 3000; i++ {
		if err := os.WriteFile(filepath.Join(ws, fmt.Sprintf("f%04d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := openRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	err = r.walk(ctx, nil, func(treeEntry) error {
		seen++
		if seen == 10 {
			cancel()
		}
		return nil
	})
	if err == nil || seen >= 3000 {
		t.Fatalf("walk ignored cancellation: seen=%d err=%v", seen, err)
	}
}

func TestWalkRejectsSubdirectorySwappedForSymlink(t *testing.T) {
	ws := faultTree(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	var readOutside bool
	err := walkWith(t, ws, func(e treeEntry) error {
		if e.rel == "d" { // visited before its children are opened
			if err := os.RemoveAll(filepath.Join(ws, "d")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(ws, "d")); err != nil {
				t.Fatal(err)
			}
		}
		if e.rel == filepath.Join("d", "x.txt") {
			readOutside = true
		}
		return nil
	})
	if err == nil || readOutside {
		t.Fatalf("descended into a directory swapped for a symlink: read=%v err=%v", readOutside, err)
	}
}
