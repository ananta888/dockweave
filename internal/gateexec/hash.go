package gateexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// defaultProtected are files whose modification during a gate run means the
// agent tampered with the checks it is being measured against.
var defaultProtected = []string{
	".paperclip/gates.json",
	"package.json",
	"package-lock.json",
	"pnpm-lock.yaml",
	"yarn.lock",
	"go.mod",
	"go.sum",
	"Makefile",
	"vitest.config.ts",
	"vitest.config.js",
	"playwright.config.ts",
	"tsconfig.json",
}

// defaultIgnore keeps diff/hash walks away from dependency and build trees.
var defaultIgnore = map[string]bool{
	".git":              true,
	"node_modules":      true,
	"dist":              true,
	".angular":          true,
	"coverage":          true,
	"test-results":      true,
	"playwright-report": true,
	"vendor":            true,
	"target":            true,
	".next":             true,
	".cache":            true,
	"tmp":               true,
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashPaths hashes the given files/directories (relative to root). Missing
// entries are skipped; a missing file is not a tamper signal by itself.
func hashPaths(root string, paths []string) (map[string]string, error) {
	out := map[string]string{}
	for _, rel := range paths {
		rel = filepath.Clean(strings.TrimSpace(rel))
		if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		abs := filepath.Join(root, rel)
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if h, err := hashFile(abs); err == nil {
				out[filepath.ToSlash(rel)] = h
			}
			continue
		}
		_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if h, err := hashFile(p); err == nil {
				if r, err := filepath.Rel(root, p); err == nil {
					out[filepath.ToSlash(r)] = h
				}
			}
			return nil
		})
	}
	return out, nil
}

func changedHashes(before, after map[string]string) []string {
	seen := map[string]bool{}
	for p, h := range before {
		if after[p] != h {
			seen[p] = true
		}
	}
	for p, h := range after {
		if before[p] != h {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func walkHashes(root string, ignore map[string]bool) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if ignore[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if ignore[d.Name()] {
			return nil
		}
		if h, err := hashFile(p); err == nil {
			out[filepath.ToSlash(rel)] = h
		}
		return nil
	})
	return out, err
}

func diffTrees(before, after map[string]string) []string {
	return changedHashes(before, after)
}

func tailLines(s string, n int) string {
	rows := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(rows) <= n {
		return strings.Join(rows, "\n")
	}
	return strings.Join(rows[len(rows)-n:], "\n")
}

// copyTree copies src into dst (which must exist) without preserving ownership
// so an unprivileged daemon can read a user-owned workspace.
func copyTree(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create gate workdir: %w", err)
	}
	cmd := exec.CommandContext(ctx, "cp", "-a", "--no-preserve=ownership", src+"/.", dst+"/")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copy workspace: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// The sandbox may run under a user namespace where host-owned files are not
	// writable. This is a private copy, so make it writable for the container.
	return makeWritable(dst)
}

func makeWritable(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mode := info.Mode().Perm()
		if d.IsDir() {
			mode |= 0o777
		} else {
			mode |= 0o666
		}
		_ = os.Chmod(p, mode)
		return nil
	})
}
