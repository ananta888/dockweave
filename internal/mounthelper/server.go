package mounthelper

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ServerConfig configures dockweave-mountd.
type ServerConfig struct {
	Socket    string
	StateRoot string
	// AllowedUIDs restricts which clients may connect. Empty = accept any
	// process that can reach the 0660 socket.
	AllowedUIDs []uint32
}

// Serve accepts mount requests until the listener is closed.
func Serve(cfg ServerConfig) error {
	if cfg.Socket == "" || cfg.StateRoot == "" {
		return fmt.Errorf("mountd: socket and state root are required")
	}
	_ = os.Remove(cfg.Socket)
	ln, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("mountd listen: %w", err)
	}
	defer ln.Close()
	if err := os.Chmod(cfg.Socket, 0o660); err != nil {
		return fmt.Errorf("mountd chmod: %w", err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(conn.(*net.UnixConn), cfg)
	}
}

func handle(conn *net.UnixConn, cfg ServerConfig) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if len(cfg.AllowedUIDs) > 0 {
		if !peerAllowed(conn, cfg.AllowedUIDs) {
			writeResponse(conn, fmt.Errorf("permission denied"))
			return
		}
	}

	fds, req, err := recvRequest(conn)
	if err != nil {
		writeResponse(conn, err)
		return
	}
	writeResponse(conn, dispatch(req, fds, cfg.StateRoot))
}

// dispatch resolves the received fds to paths (verifying the target inode for
// bind, where a fresh directory must not have been swapped) and performs the
// syscall. FDs are closed here.
func dispatch(req Request, fds []int, root string) error {
	defer closeAll(fds)
	need := func(n int) error {
		if len(fds) < n {
			return fmt.Errorf("op %s needs %d fds, got %d", req.Op, n, len(fds))
		}
		return nil
	}
	path := func(i int, verify bool) (string, error) {
		return resolveFD(fds[i], root, true, verify)
	}
	// source resolves a source fd to its path string. The daemon verifies
	// sources (allowlist + openat2) before passing the fd; the helper
	// re-resolves the path for the syscall. WSL2 kernels reject a bind source
	// mounted via /proc/self/fd/<n> (EINVAL), so the readlink resolution is
	// kept here.
	source := func(i int) (string, error) {
		return resolveFD(fds[i], root, false, false)
	}

	switch req.Op {
	case OpPing:
		return nil
	case OpBind:
		if err := need(2); err != nil {
			return err
		}
		dst, err := path(1, true)
		if err != nil {
			return err
		}
		// Prefer open_tree/move_mount so the source is pinned to its verified
		// fd (no path re-resolution = no TOCTOU). Fall back to the readlink
		// path on kernels/filesystems without the new mount API.
		if err := bindSourceFD(fds[0], dst, req); err == nil {
			return nil
		}
		src, err := source(0)
		if err != nil {
			return err
		}
		return unix.Mount(src, dst, "none", req.Flags|unix.MS_BIND, "")
	case OpRemount:
		if err := need(1); err != nil {
			return err
		}
		dst, err := path(0, false)
		if err != nil {
			return err
		}
		return unix.Mount("none", dst, "", req.Flags|unix.MS_REMOUNT, "")
	case OpMove:
		if err := need(2); err != nil {
			return err
		}
		src, err := source(0)
		if err != nil {
			return err
		}
		dst, err := path(1, false)
		if err != nil {
			return err
		}
		return unix.Mount(src, dst, "", unix.MS_MOVE, "")
	case OpUnmount:
		if err := need(1); err != nil {
			return err
		}
		dst, err := path(0, false)
		if err != nil {
			return err
		}
		_ = unix.Close(fds[0])
		fds[0] = -1
		return unix.Unmount(dst, int(req.Flags))
	case OpPropagation:
		if err := need(1); err != nil {
			return err
		}
		dst, err := path(0, false)
		if err != nil {
			return err
		}
		return unix.Mount("none", dst, "", req.Flags, "")
	case OpSetReadonly:
		if err := need(1); err != nil {
			return err
		}
		dst, err := path(0, false)
		if err != nil {
			return err
		}
		flags := uint(0)
		if req.Recursive {
			flags = unix.AT_RECURSIVE
		}
		attr := &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}
		return unix.MountSetattr(unix.AT_FDCWD, dst, flags, attr)
	case OpChown:
		if err := need(1); err != nil {
			return err
		}
		dst, err := path(0, false)
		if err != nil {
			return err
		}
		return filepath.WalkDir(dst, func(p string, _ os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Lchown(p, req.UID, req.GID)
		})
	default:
		return fmt.Errorf("unknown op %q", req.Op)
	}
}

func closeAll(fds []int) {
	for _, fd := range fds {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
}

// bindSourceFD binds a source directory via open_tree/move_mount so the source
// is pinned to its received fd (no path re-resolution). It returns an error
// when the new mount API is unavailable, so the caller can fall back to the
// readlink-based bind. NOSYMFOLLOW is applied on the target mount to preserve
// the symlink-planting protection (SD-016).
func bindSourceFD(srcFD int, dst string, req Request) error {
	flags := uint(unix.OPEN_TREE_CLONE | unix.OPEN_TREE_CLOEXEC | unix.AT_EMPTY_PATH)
	if req.Recursive {
		flags |= unix.AT_RECURSIVE
	}
	mfd, err := unix.OpenTree(srcFD, "", flags)
	if err != nil {
		return err
	}
	defer unix.Close(mfd)
	if err := unix.MoveMount(mfd, "", unix.AT_FDCWD, dst, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		return err
	}
	if req.Flags&unix.MS_NOSYMFOLLOW != 0 {
		attr := &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_NOSYMFOLLOW}
		if err := unix.MountSetattr(unix.AT_FDCWD, dst, unix.AT_RECURSIVE, attr); err != nil {
			_ = unix.Unmount(dst, 0)
			return err
		}
	}
	return nil
}

// resolveFD returns the path an fd refers to. The target must lie below the
// state root. With verify, the path must still point at the same inode as the
// fd (no swap between check and syscall).
func resolveFD(fd int, root string, requireRoot, verify bool) (string, error) {
	link := fmt.Sprintf("/proc/self/fd/%d", fd)
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("resolve fd %d: %w", fd, err)
	}
	if requireRoot {
		cleanRoot := filepath.Clean(root)
		if !strings.HasPrefix(filepath.Clean(target), cleanRoot+string(filepath.Separator)) && filepath.Clean(target) != cleanRoot {
			return "", fmt.Errorf("target %q is outside the state root", target)
		}
	}
	if verify {
		var fdStat, pathStat unix.Stat_t
		if err := unix.Fstat(fd, &fdStat); err != nil {
			return "", fmt.Errorf("fstat fd %d: %w", fd, err)
		}
		if err := unix.Stat(target, &pathStat); err != nil {
			return "", fmt.Errorf("stat target %q: %w", target, err)
		}
		if fdStat.Dev != pathStat.Dev || fdStat.Ino != pathStat.Ino {
			return "", fmt.Errorf("target %q changed identity", target)
		}
	}
	return target, nil
}

func peerAllowed(conn *net.UnixConn, allowed []uint32) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	var uid uint32
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			credErr = err
			return
		}
		uid = cred.Uid
	}); err != nil || credErr != nil {
		return false
	}
	for _, u := range allowed {
		if u == uid {
			return true
		}
	}
	return false
}

func recvRequest(conn *net.UnixConn) ([]int, Request, error) {
	var req Request
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, req, err
	}
	buf := make([]byte, 4096)
	oob := make([]byte, unix.CmsgSpace(4*4))
	var n, oobn int
	var recvErr error
	if err := raw.Read(func(fd uintptr) bool {
		n, oobn, _, _, recvErr = unix.Recvmsg(int(fd), buf, oob, 0)
		if recvErr == unix.EAGAIN || recvErr == unix.EWOULDBLOCK {
			return false
		}
		return true
	}); err != nil {
		return nil, req, err
	}
	if recvErr != nil {
		return nil, req, recvErr
	}
	fds, err := parseRights(oob[:oobn])
	if err != nil {
		return nil, req, err
	}
	if err := json.Unmarshal(buf[:n], &req); err != nil {
		return fds, req, fmt.Errorf("invalid request: %w", err)
	}
	return fds, req, nil
}

func parseRights(oob []byte) ([]int, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for i := range msgs {
		got, err := unix.ParseUnixRights(&msgs[i])
		if err != nil {
			continue
		}
		fds = append(fds, got...)
	}
	return fds, nil
}

func writeResponse(conn *net.UnixConn, err error) {
	resp := Response{OK: err == nil}
	if err != nil {
		resp.Error = err.Error()
		if errno, ok := err.(syscall.Errno); ok {
			resp.Errno = int(errno)
		}
	}
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}
