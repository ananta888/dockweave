package gateexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustFP(t *testing.T, root string) string {
	t.Helper()
	fp, err := Fingerprint(context.Background(), root, FingerprintLimits{})
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestFingerprintDeterministicAndSensitive(t *testing.T) {
	files := map[string]string{"src/a.go": "package a", "README.md": "hi", ".paperclip/gates.json": "{}"}
	a, b := t.TempDir(), t.TempDir()
	writeTree(t, a, files)
	writeTree(t, b, files)
	base := mustFP(t, a)
	if base != mustFP(t, b) || base != mustFP(t, a) {
		t.Fatal("same content must give the same fingerprint")
	}

	// Mode changes do not matter.
	_ = os.Chmod(filepath.Join(a, "README.md"), 0o600)
	if mustFP(t, a) != base {
		t.Fatal("mode change must not change the fingerprint")
	}

	// Changed, new and deleted files do.
	writeTree(t, a, map[string]string{"src/a.go": "package a // changed"})
	changed := mustFP(t, a)
	if changed == base {
		t.Fatal("content change not detected")
	}
	writeTree(t, a, map[string]string{"src/a.go": "package a"})
	if mustFP(t, a) != base {
		t.Fatal("restoring content must restore the fingerprint")
	}
	writeTree(t, a, map[string]string{"src/b.go": "package a"})
	if mustFP(t, a) == base {
		t.Fatal("new file not detected")
	}
	_ = os.Remove(filepath.Join(a, "src/b.go"))
	_ = os.Remove(filepath.Join(a, "README.md"))
	if mustFP(t, a) == base {
		t.Fatal("deleted file not detected")
	}
	// Renames are detected (path is part of the hash).
	c := t.TempDir()
	writeTree(t, c, map[string]string{"src/x.go": "package a", "README.md": "hi", ".paperclip/gates.json": "{}"})
	if mustFP(t, c) == base {
		t.Fatal("rename not detected")
	}
}

func TestFingerprintIgnoresDependencyTrees(t *testing.T) {
	a := t.TempDir()
	writeTree(t, a, map[string]string{"main.go": "package main"})
	base := mustFP(t, a)
	writeTree(t, a, map[string]string{
		"node_modules/x/index.js": "x", ".git/objects/ab": "obj", "dist/app.js": "built", "sub/target/out": "o",
	})
	if mustFP(t, a) != base {
		t.Fatal("ignored trees must not change the fingerprint")
	}
}

func TestFingerprintSymlinkNotFollowed(t *testing.T) {
	a := t.TempDir()
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret": "v1"})
	writeTree(t, a, map[string]string{"main.go": "package main"})
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(a, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	base := mustFP(t, a)
	writeTree(t, outside, map[string]string{"secret": "v2"})
	if mustFP(t, a) != base {
		t.Fatal("symlink target content must not be followed")
	}
}

func TestFingerprintLimitsFailClosed(t *testing.T) {
	a := t.TempDir()
	writeTree(t, a, map[string]string{"a": "1", "b": "2", "c": "3"})
	if _, err := Fingerprint(context.Background(), a, FingerprintLimits{MaxFiles: 2}); !errors.Is(err, ErrFingerprintLimit) {
		t.Fatalf("file limit: err = %v", err)
	}
	if _, err := Fingerprint(context.Background(), a, FingerprintLimits{MaxBytes: 2}); !errors.Is(err, ErrFingerprintLimit) {
		t.Fatalf("byte limit: err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fingerprint(ctx, a, FingerprintLimits{}); err == nil {
		t.Fatal("cancelled context must fail")
	}
	if _, err := Fingerprint(context.Background(), filepath.Join(a, "missing"), FingerprintLimits{}); err == nil {
		t.Fatal("missing root must fail")
	}
}
