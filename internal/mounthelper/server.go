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
	// FDs are consumed by dispatch; each op closes what it opened.
	defer closeAll(fds)
	writeResponse(conn, dispatch(req, fds, cfg.StateRoot))
}

// dispatch performs the syscall. Bind/remount/move/setattr are fd-based
// (open_tree/move_mount/mount_setattr); unmount and propagation need a path
// and are resolved from the fd with a state-root check.
func dispatch(req Request, fds []int, root string) error {
	need := func(n int) error {
		if len(fds) < n {
			return fmt.Errorf("op %s needs %d fds, got %d", req.Op, n, len(fds))
		}
		return nil
	}
	rec := uint(0)
	if req.Recursive {
		rec = unix.AT_RECURSIVE
	}
	nosymfollow := req.Flags&unix.MS_NOSYMFOLLOW != 0

	switch req.Op {
	case OpPing:
		return nil
	case OpBind:
		if err := need(2); err != nil {
			return err
		}
		tree, err := unix.OpenTree(fds[0], "",
			uint(unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_EMPTY_PATH)|rec)
		if err != nil {
			return fmt.Errorf("open_tree source: %w", err)
		}
		defer unix.Close(tree)
		if req.RO || nosymfollow {
			var attr unix.MountAttr
			if req.RO {
				attr.Attr_set |= unix.MOUNT_ATTR_RDONLY
			}
			if nosymfollow {
				attr.Attr_set |= unix.MOUNT_ATTR_NOSYMFOLLOW
			}
			if err := unix.MountSetattr(tree, "", uint(unix.AT_EMPTY_PATH)|rec, &attr); err != nil {
				return fmt.Errorf("setattr source: %w", err)
			}
		}
		if err := unix.MoveMount(tree, "", fds[1], "", unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
			return fmt.Errorf("move_mount: %w", err)
		}
		return nil
	case OpRemount:
		if err := need(1); err != nil {
			return err
		}
		var attr unix.MountAttr
		if req.Flags&unix.MS_RDONLY != 0 {
			attr.Attr_set |= unix.MOUNT_ATTR_RDONLY
		}
		if nosymfollow {
			attr.Attr_set |= unix.MOUNT_ATTR_NOSYMFOLLOW
		}
		if attr.Attr_set == 0 {
			return fmt.Errorf("remount without attribute change")
		}
		return unix.MountSetattr(fds[0], "", uint(unix.AT_EMPTY_PATH)|rec, &attr)
	case OpMove:
		if err := need(2); err != nil {
			return err
		}
		return unix.MoveMount(fds[0], "", fds[1], "", unix.MOVE_MOUNT_F_EMPTY_PATH)
	case OpSetReadonly:
		if err := need(1); err != nil {
			return err
		}
		attr := &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}
		return unix.MountSetattr(fds[0], "", unix.AT_EMPTY_PATH|rec, attr)
	case OpUnmount:
		if err := need(1); err != nil {
			return err
		}
		target, err := resolvePath(fds[0], root)
		if err != nil {
			return err
		}
		_ = unix.Close(fds[0]) // close before umount, else EBUSY
		fds[0] = -1
		return unix.Unmount(target, int(req.Flags))
	case OpPropagation:
		if err := need(1); err != nil {
			return err
		}
		target, err := resolvePath(fds[0], root)
		if err != nil {
			return err
		}
		return unix.Mount("none", target, "", req.Flags, "")
	case OpChown:
		if err := need(1); err != nil {
			return err
		}
		target, err := resolvePath(fds[0], root)
		if err != nil {
			return err
		}
		return filepath.WalkDir(target, func(p string, _ os.DirEntry, err error) error {
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

// resolvePath returns the path an fd refers to, after checking it lies below
// the state root. Used only for the two ops that have no fd-based syscall.
func resolvePath(fd int, root string) (string, error) {
	link := fmt.Sprintf("/proc/self/fd/%d", fd)
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("resolve fd %d: %w", fd, err)
	}
	cleanRoot := filepath.Clean(root)
	if !strings.HasPrefix(filepath.Clean(target), cleanRoot+string(filepath.Separator)) && filepath.Clean(target) != cleanRoot {
		return "", fmt.Errorf("target %q is outside the state root", target)
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
