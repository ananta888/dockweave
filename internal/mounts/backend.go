package mounts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/mounthelper"
)

// backend abstracts the privileged syscalls. The direct backend runs inside a
// root daemon; the helper backend sends directory fds to dockweave-mountd so
// the daemon itself needs no CAP_SYS_ADMIN.
type backend interface {
	Bind(src, dst string, flags uintptr) error
	Remount(dst string, flags uintptr) error
	Move(src, dst string) error
	Unmount(dst string, flags int) error
	Propagation(dst string, flags uintptr) error
	SetReadonly(dst string, recursive bool) error
	ChownTree(path string, uid, gid int) error
}

type directBackend struct{}

func (directBackend) Bind(src, dst string, flags uintptr) error {
	return unix.Mount(src, dst, "none", flags, "")
}

func (directBackend) Remount(dst string, flags uintptr) error {
	return unix.Mount("none", dst, "", flags, "")
}

func (directBackend) Move(src, dst string) error {
	return unix.Mount(src, dst, "", unix.MS_MOVE, "")
}

func (directBackend) Unmount(dst string, flags int) error {
	return unix.Unmount(dst, flags)
}

func (directBackend) Propagation(dst string, flags uintptr) error {
	return unix.Mount("none", dst, "", flags, "")
}

func (directBackend) SetReadonly(dst string, recursive bool) error {
	flags := 0
	if recursive {
		flags = unix.AT_RECURSIVE
	}
	return unix.MountSetattr(unix.AT_FDCWD, dst, uint(flags), &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY})
}

func (directBackend) ChownTree(path string, uid, gid int) error {
	return filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(p, uid, gid)
	})
}

// helperBackend passes O_RDONLY directory fds to the privileged helper. The
// fds must be readable (not O_PATH) so the helper can bind-mount the *source*
// via /proc/self/fd/<n> and pin the daemon-verified object across the
// privilege boundary (no TOCTOU re-resolution). The daemon already opens mount
// sources read-only in ResolveSource, and targets live under the daemon-owned
// state root, so O_RDONLY is always available here.
type helperBackend struct {
	client *mounthelper.Client
}

func (h *helperBackend) open(path string) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	return fd, nil
}

func (h *helperBackend) Do(req mounthelper.Request, paths ...string) error {
	fds := make([]int, 0, len(paths))
	for _, p := range paths {
		fd, err := h.open(p)
		if err != nil {
			for _, open := range fds {
				_ = unix.Close(open)
			}
			return err
		}
		fds = append(fds, fd)
	}
	// Do closes the fds after sending; keep no copy.
	return h.client.Do(context.Background(), req, fds)
}

func (h *helperBackend) Bind(src, dst string, flags uintptr) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpBind, Flags: flags, Recursive: flags&unix.MS_REC != 0}, src, dst)
}

func (h *helperBackend) Remount(dst string, flags uintptr) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpRemount, Flags: flags}, dst)
}

func (h *helperBackend) Move(src, dst string) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpMove}, src, dst)
}

func (h *helperBackend) Unmount(dst string, flags int) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpUnmount, Flags: uintptr(flags)}, dst)
}

func (h *helperBackend) Propagation(dst string, flags uintptr) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpPropagation, Flags: flags}, dst)
}

func (h *helperBackend) SetReadonly(dst string, recursive bool) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpSetReadonly, Recursive: recursive}, dst)
}

func (h *helperBackend) ChownTree(path string, uid, gid int) error {
	return h.Do(mounthelper.Request{Op: mounthelper.OpChown, UID: uid, GID: gid}, path)
}

// pickBackend returns the helper backend when a socket is configured and
// reachable, otherwise the direct (root) backend.
func pickBackend(cfg Config) backend {
	if cfg.MountdSocket != "" {
		c := &mounthelper.Client{Socket: cfg.MountdSocket}
		if c.Available() {
			return &helperBackend{client: c}
		}
	}
	return directBackend{}
}
