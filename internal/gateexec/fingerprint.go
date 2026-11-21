package gateexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"
)

// FingerprintLimits bounds actual content bytes, entries and cooperative I/O
// time. Kernel-blocked disk/network filesystems may delay cancellation; FIFO,
// device and socket entries are rejected before reading.
type FingerprintLimits struct {
	// MaxFiles bounds the visited entries: files, symlinks and directories.
	MaxFiles int
	// MaxBytes bounds the bytes actually read (link texts included).
	MaxBytes int64
	// Timeout is cooperative: checked between reads (see SD-021).
	Timeout time.Duration
}

var DefaultFingerprintLimits = FingerprintLimits{MaxFiles: 100_000, MaxBytes: 2 << 30, Timeout: 60 * time.Second}
var ErrFingerprintLimit = errors.New("workspace exceeds fingerprint limits")

// Fingerprint hashes content and link text, never symlink targets. All access
// is rooted with openat2, with no path-based fallback on unsupported kernels.
func Fingerprint(ctx context.Context, root string, lim FingerprintLimits) (string, error) {
	return fingerprintExcluding(ctx, root, lim, nil)
}

// fingerprintExcluding leaves the given relative paths out; the runner uses
// it to compare a snapshot that omitted links under the symlink policy.
func fingerprintExcluding(ctx context.Context, root string, lim FingerprintLimits, exclude map[string]bool) (string, error) {
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
	r, err := openRoot(root)
	if err != nil {
		return "", err
	}
	defer r.close()
	type entry struct{ rel, sum string }
	var entries []entry
	remaining := lim.MaxBytes
	visited := 0
	err = r.walk(ctx, defaultIgnore, func(e treeEntry) error {
		// Every entry counts, directories included, so a tree of empty
		// directories is bounded by MaxFiles and not only by the timeout.
		visited++
		if visited > lim.MaxFiles {
			return ErrFingerprintLimit
		}
		if exclude[e.rel] {
			return nil
		}
		if e.info.IsDir() {
			return nil
		}
		var sum string
		if e.info.Mode()&fs.ModeSymlink != 0 {
			if int64(len(e.link)) > remaining {
				return ErrFingerprintLimit
			}
			remaining -= int64(len(e.link))
			h := sha256.Sum256([]byte(e.link))
			sum = "L" + hex.EncodeToString(h[:])
		} else {
			h := sha256.New()
			if err := copyBounded(ctx, h, e.file, &remaining); err != nil {
				return err
			}
			sum = "F" + hex.EncodeToString(h.Sum(nil))
		}
		entries = append(entries, entry{e.rel, sum})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\n", e.rel, e.sum)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
