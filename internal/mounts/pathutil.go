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
func ResolveSource(path string, allowedRoots []string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrNotAllowed)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("canonicalize %q: %w", path, err)
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
		return "", fmt.Errorf("%w: %q is not beneath any allowed root", ErrNotAllowed, canon)
	}
	fi, err := os.Stat(canon)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", canon, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%w: %q is not a directory", ErrNotAllowed, canon)
	}
	// TOCTOU hardening: re-open the canonical path with RESOLVE_BENEATH
	// relative to its parent, refusing magic links.
	if err := safeVerify(canon); err != nil {
		return "", err
	}
	return canon, nil
}

// safeVerify opens path with openat2(RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS)
// relative to its parent directory. It proves that a non-magiclink,
// beneath-resolving resolution of the path exists at check time.
// On kernels without openat2 it degrades to the EvalSymlinks result.
func safeVerify(canon string) error {
	parent := filepath.Dir(canon)
	base := filepath.Base(canon)
	pfd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open parent of %q: %w", canon, err)
	}
	defer unix.Close(pfd)
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(pfd, base, how)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
			return nil // openat2 unavailable: EvalSymlinks result stands
		}
		if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ELOOP) {
			return fmt.Errorf("%w: %q escapes its parent (symlink race?)", ErrNotAllowed, canon)
		}
		return fmt.Errorf("openat2 %q: %w", canon, err)
	}
	unix.Close(fd)
	return nil
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
