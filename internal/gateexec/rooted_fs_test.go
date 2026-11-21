package gateexec

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRootedAccessRejectsDirectoryAndFileReplacement(t *testing.T) {
	for _, replacement := range []string{"file", "directory", "root"} {
		t.Run(replacement, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			writeTree(t, root, map[string]string{"sub/file": "inside"})
			writeTree(t, outside, map[string]string{"file": "external-secret"})
			r, err := openRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer r.close()
			switch replacement {
			case "root":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(root + "-old") })
				if err := os.Symlink(outside, root); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "sub")); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.Remove(filepath.Join(root, "sub/file")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "file"), filepath.Join(root, "sub/file")); err != nil {
					t.Fatal(err)
				}
			}
			data, err := r.readFile(context.Background(), "sub/file", 100)
			if err == nil || len(data) > 0 {
				t.Fatalf("replacement accepted: %q %v", data, err)
			}
		})
	}
}

func TestRootedAccessRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := openRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	if _, err := r.readFile(context.Background(), "pipe", 100); err == nil {
		t.Fatal("FIFO accepted")
	}
	if _, err := Fingerprint(context.Background(), root, FingerprintLimits{}); err == nil {
		t.Fatal("special file fingerprint accepted")
	}
}

func TestCopyTreePreservesInternalLinksAndWritableFiles(t *testing.T) {
	root, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	writeTree(t, root, map[string]string{"sub/file": "content"})
	if err := os.Symlink("sub/file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(context.Background(), root, dst); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil || link != "sub/file" {
		t.Fatalf("link: %q %v", link, err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "sub/file"))
	if err != nil || string(data) != "content" {
		t.Fatalf("copy: %q %v", data, err)
	}
	info, err := os.Stat(filepath.Join(dst, "sub/file"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0666 != 0666 {
		t.Fatalf("not writable: %o", info.Mode())
	}
}

func TestBoundedReadCountsActualBytesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		remaining     int64
		want          error
	}{
		{"exact", "1234", 4, nil}, {"overflow", "12345", 4, ErrFingerprintLimit}, {"empty", "", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			remaining := tc.remaining
			err := copyBounded(context.Background(), &out, strings.NewReader(tc.content), &remaining)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v", err)
			}
			if tc.want == nil && out.String() != tc.content {
				t.Fatal("truncated content")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	remaining := int64(5)
	if err := copyBounded(ctx, io.Discard, strings.NewReader("data"), &remaining); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestHashingReportsInvalidPathsAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"source": "data"})
	if _, err := hashPaths(root, []string{"../outside"}); err == nil {
		t.Fatal("escaping protected path accepted")
	}
	if _, err := hashPaths(root, []string{"not-present"}); err != nil {
		t.Fatalf("optional absent path: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "source-pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := walkHashes(root, nil); err == nil {
		t.Fatal("incomplete policy hash accepted")
	}
}

func TestReadConfigRejectsSymlinksAndOversizedContent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTree(t, root, map[string]string{".paperclip/file": ""})
	writeTree(t, outside, map[string]string{"config": "{}"})
	if err := os.Symlink(filepath.Join(outside, "config"), filepath.Join(root, ".paperclip/gates.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readConfig(root); err == nil {
		t.Fatal("external config accepted")
	}
	if err := os.Remove(filepath.Join(root, ".paperclip/gates.json")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{".paperclip/gates.json": strings.Repeat(" ", 4<<20) + "{}"})
	if _, _, err := readConfig(root); !errors.Is(err, ErrFingerprintLimit) {
		t.Fatalf("limit not enforced: %v", err)
	}
}
