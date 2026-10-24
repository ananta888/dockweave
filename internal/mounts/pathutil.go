package mounts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrNotAllowed is returned when a path fails security validation.
var ErrNotAllowed = errors.New("path not allowed")

// ErrBusy reports an unmount rejected by the kernel because the mount is in use (EBUSY).
var ErrBusy = errors.New("mount target is busy (open files or working directories)")

// ErrConflict reports that a target is already mounted from a different
// source (bind-source identity mismatch).
var ErrConflict = errors.New("mount source conflict")

// ResolveSource canonicalizes a user-supplied host path and enforces the
// allowlist of permitted roots. Symlinks are resolved; the result must be a
// directory beneath one of the allowed roots.
//
// The second return value is an open directory fd of the *verified* object
// (openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS), plain open as fallback on
// older kernels). Mount via /proc/self/fd/<fd> so the verified object and
// the mount stay coupled — a path swap after verification cannot redirect
// the mount (TOCTOU). The caller must close the fd.
func ResolveSource(path string, allowedRoots []string) (string, int, error) {
	if path == "" {
		return "", -1, fmt.Errorf("%w: empty path", ErrNotAllowed)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", -1, fmt.Errorf("resolve %q: %w", path, err)
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", -1, fmt.Errorf("canonicalize %q: %w", path, err)
	}
	allowed := false
	for _, root := range allowedRoots {
		r, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue // nonexistent allowlist root is ignored
		}
		if canon == r || strings.HasPrefix(canon, r+string(os.PathSeparator)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", -1, fmt.Errorf("%w: %q is not beneath any allowed root", ErrNotAllowed, canon)
	}
	fi, err := os.Stat(canon)
	if err != nil {
		return "", -1, fmt.Errorf("stat %q: %w", canon, err)
	}
	if !fi.IsDir() {
		return "", -1, fmt.Errorf("%w: %q is not a directory", ErrNotAllowed, canon)
	}
	// TOCTOU hardening: open the canonical path with RESOLVE_BENEATH
	// relative to its parent, refusing magic links, and hand the fd to the
	// caller for the actual mount.
	fd, err := safeVerify(canon)
	if err != nil {
		return "", -1, err
	}
	return canon, fd, nil
}

// safeVerify opens path with openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS)
// relative to its parent directory and returns the open fd. It proves that a
// non-magiclink, beneath-resolving resolution of the path exists at check
// time; keeping the fd open ties later use (mounting) to that exact object.
// On kernels without openat2 it degrades to a plain O_DIRECTORY open of the
// canonical path (EvalSymlinks result stands).
func safeVerify(canon string) (int, error) {
	parent := filepath.Dir(canon)
	base := filepath.Base(canon)
	pfd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open parent of %q: %w", canon, err)
	}
	defer unix.Close(pfd)
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(pfd, base, how)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
			// openat2 unavailable: fall back to a direct open. The fd still
			// couples verification and mount, just without RESOLVE flags.
			fd, err := unix.Open(canon, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				return -1, fmt.Errorf("open %q: %w", canon, err)
			}
			return fd, nil
		}
		if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ELOOP) {
			return -1, fmt.Errorf("%w: %q escapes its parent (symlink race?)", ErrNotAllowed, canon)
		}
		return -1, fmt.Errorf("openat2 %q: %w", canon, err)
	}
	return fd, nil
}

// SanitizeTargetName validates a single path component used as the mount
// target name inside the agent workspace tree.
func SanitizeTargetName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("%w: invalid target name %q", ErrNotAllowed, name)
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, 0) {
		return fmt.Errorf("%w: target name %q must be a single path component", ErrNotAllowed, name)
	}
	return nil
}
