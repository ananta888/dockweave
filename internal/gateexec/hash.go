package gateexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
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

func hashPaths(root string, paths []string) (map[string]string, error) {
	return hashSelected(context.Background(), root, nil, paths)
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
	return hashSelected(context.Background(), root, ignore, nil)
}

func hashSelected(ctx context.Context, root string, ignore map[string]bool, paths []string) (map[string]string, error) {
	for _, p := range paths {
		if !filepath.IsLocal(p) || p == "." {
			return nil, fmt.Errorf("invalid protected path: %q", p)
		}
	}
	r, err := openRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.close()
	out := map[string]string{}
	remaining := DefaultFingerprintLimits.MaxBytes
	err = r.walk(ctx, ignore, func(e treeEntry) error {
		selected := paths == nil
		for _, p := range paths {
			p = filepath.Clean(p)
			if e.rel == p || strings.HasPrefix(e.rel, p+"/") || (e.info.IsDir() && strings.HasPrefix(p, e.rel+"/")) {
				selected = true
			}
		}
		if !selected {
			if e.info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.info.IsDir() {
			return nil
		}
		h := sha256.New()
		if e.info.Mode()&os.ModeSymlink != 0 {
			h.Write([]byte(e.link))
			out[e.rel] = "L" + hex.EncodeToString(h.Sum(nil))
			return nil
		}
		if err := copyBounded(ctx, h, e.file, &remaining); err != nil {
			return err
		}
		out[e.rel] = "F" + hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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
