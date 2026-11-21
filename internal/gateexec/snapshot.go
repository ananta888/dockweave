package gateexec

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ananta888/dockweave/internal/gatesync"
)

// copyTree creates an independent private tree. Unlike cp -a plus chmod,
// neither source links nor copied links are ever dereferenced on the host.
// Links that leave the workspace are handled by policy (see
// gatesync.Config.SymlinkPolicy); the returned paths were left out.
func copyTree(ctx context.Context, src, dst, policy string) ([]string, error) {
	switch policy {
	case "":
		policy = gatesync.SymlinkHash
	case gatesync.SymlinkHash, gatesync.SymlinkIgnored, gatesync.SymlinkFail:
	default:
		return nil, fmt.Errorf("unknown symlink_policy %q (hash|ignored|fail)", policy)
	}
	var omitted []string
	r, err := openRoot(src)
	if err != nil {
		return nil, err
	}
	defer r.close()
	if err := os.MkdirAll(dst, 0o777); err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}
	if err := os.Chmod(dst, 0o777); err != nil {
		return nil, fmt.Errorf("make snapshot root writable: %w", err)
	}
	remaining := int64(16 << 30)
	err = r.walk(ctx, nil, func(e treeEntry) error {
		target := filepath.Join(dst, e.rel)
		switch {
		case e.info.IsDir():
			if err := os.Mkdir(target, 0o777); err != nil {
				return fmt.Errorf("create snapshot directory: %w", err)
			}
			return os.Chmod(target, 0o777)
		case e.info.Mode()&fs.ModeSymlink != 0:
			if filepath.IsAbs(e.link) || !filepath.IsLocal(filepath.Join(filepath.Dir(e.rel), e.link)) {
				if policy == gatesync.SymlinkFail || (policy == gatesync.SymlinkIgnored && !inIgnoredTree(e.rel)) {
					return fmt.Errorf("snapshot symlink escapes workspace: %q (symlink_policy %s)", e.rel, policy)
				}
				omitted = append(omitted, e.rel)
				return nil
			}
			if err := os.Symlink(e.link, target); err != nil {
				return fmt.Errorf("copy snapshot link: %w", err)
			}
			return nil
		default:
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, e.info.Mode().Perm()|0o666)
			if err != nil {
				return fmt.Errorf("create snapshot file: %w", err)
			}
			err = copyBounded(ctx, f, e.file, &remaining)
			if err == nil {
				err = f.Chmod(e.info.Mode().Perm() | 0o666)
			}
			closeErr := f.Close()
			if err != nil {
				return err
			}
			return closeErr
		}
	})
	if err != nil {
		return nil, err
	}
	return omitted, nil
}

// inIgnoredTree reports whether rel lies in a dependency/build tree that the
// diff and hash walks ignore (defaultIgnore).
func inIgnoredTree(rel string) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if defaultIgnore[part] {
			return true
		}
	}
	return false
}
