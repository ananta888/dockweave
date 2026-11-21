package gateexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FingerprintLimits bounds the cost of Fingerprint. Exceeding a limit is an
// error (ErrFingerprintLimit): without a complete fingerprint a passing gate
// run cannot be proven fresh, so callers must fail closed.
type FingerprintLimits struct {
	MaxFiles int
	MaxBytes int64
	Timeout  time.Duration
}

// DefaultFingerprintLimits are used when a limit is zero.
var DefaultFingerprintLimits = FingerprintLimits{
	MaxFiles: 100_000,
	MaxBytes: 2 << 30, // 2 GiB of file content
	Timeout:  60 * time.Second,
}

// ErrFingerprintLimit reports that a workspace is too large to fingerprint.
var ErrFingerprintLimit = errors.New("workspace exceeds fingerprint limits")

// Fingerprint returns a deterministic content hash of a workspace: the sorted
// relative paths of all regular files and symlinks together with the SHA-256
// of their content (symlinks: their target, never followed). The same trees
// as the gate diff are ignored (defaultIgnore: .git, node_modules, build
// output ...), so dependency installs or build artefacts do not invalidate a
// passing gate run, while every source, test or config change does. File
// modes and timestamps are not part of the fingerprint.
func Fingerprint(ctx context.Context, root string, lim FingerprintLimits) (string, error) {
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = DefaultFingerprintLimits.MaxFiles
	}
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultFingerprintLimits.MaxBytes
	}
	if lim.Timeout <= 0 {
		lim.Timeout = DefaultFingerprintLimits.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Timeout)
	defer cancel()

	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("fingerprint: %s is not a directory", root)
	}

	type entry struct{ rel, sum string }
	var (
		entries []entry
		total   int64
	)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			// An unreadable entry makes the fingerprint incomplete.
			return fmt.Errorf("fingerprint: %w", werr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("fingerprint: %w", err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if defaultIgnore[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if defaultIgnore[d.Name()] {
			return nil
		}
		if len(entries) >= lim.MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrFingerprintLimit, lim.MaxFiles)
		}
		rel = filepath.ToSlash(rel)
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return fmt.Errorf("fingerprint: %w", err)
			}
			s := sha256.Sum256([]byte(target))
			entries = append(entries, entry{rel, "L" + hex.EncodeToString(s[:])})
		case t.IsRegular():
			fi, err := d.Info()
			if err != nil {
				return fmt.Errorf("fingerprint: %w", err)
			}
			total += fi.Size()
			if total > lim.MaxBytes {
				return fmt.Errorf("%w: more than %d bytes", ErrFingerprintLimit, lim.MaxBytes)
			}
			sum, err := hashFileCtx(ctx, p)
			if err != nil {
				return fmt.Errorf("fingerprint: %w", err)
			}
			entries = append(entries, entry{rel, "F" + sum})
		default:
			// Sockets, devices and fifos carry no content; record their
			// presence so adding or removing one still changes the hash.
			entries = append(entries, entry{rel, "S"})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\n", e.rel, e.sum)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func hashFileCtx(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
