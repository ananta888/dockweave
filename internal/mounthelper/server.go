package mounthelper

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	writeResponse(conn, dispatch(req, fds, cfg.StateRoot))
}

func dispatch(req Request, fds []int, root string) error {
	// Resolve each fd to its path, but only after proving that the path still
	// refers to the same inode as the fd (no swap between check and syscall).
	// Bind and move create a new mount, so the target must still be the
	// inode we validated. Operations on an existing mount (unmount, remount,
	// propagation, setattr) may legitimately see the mounted root instead of
	// the covered directory, so they skip the identity check.
	verifyIdentity := req.Op == OpBind || req.Op == OpMove
	paths := make([]string, len(fds))
	for i, fd := range fds {
		p, err := resolveFD(fd, verifyIdentity)
		if err != nil {
			return err
		}
		paths[i] = p
	}
	fdPath := func(i int) string { return paths[i] }
	need := func(n int) error {
		if len(fds) < n {
			return fmt.Errorf("op %s needs %d fds, got %d", req.Op, n, len(fds))
		}
		return nil
	}
	checkTarget := func(i int) error {
		target := fdPath(i)
		cleanRoot := filepath.Clean(root)
		if !strings.HasPrefix(filepath.Clean(target), cleanRoot+string(filepath.Separator)) && filepath.Clean(target) != cleanRoot {
			return fmt.Errorf("target %q is outside the state root", target)
		}
		return nil
	}

	switch req.Op {
	case OpPing:
		return nil
	case OpBind:
		if err := need(2); err != nil {
			return err
		}
		if err := checkTarget(1); err != nil {
			return err
		}
		return unix.Mount(fdPath(0), fdPath(1), "none", req.Flags|unix.MS_BIND, "")
	case OpRemount:
		if err := need(1); err != nil {
			return err
		}
		if err := checkTarget(0); err != nil {
			return err
		}
		return unix.Mount("none", fdPath(0), "", req.Flags|unix.MS_REMOUNT, "")
	case OpMove:
		if err := need(2); err != nil {
			return err
		}
		if err := checkTarget(1); err != nil {
			return err
		}
		return unix.Mount(fdPath(0), fdPath(1), "", unix.MS_MOVE, "")
	case OpUnmount:
		if err := need(1); err != nil {
			return err
		}
		if err := checkTarget(0); err != nil {
			return err
		}
		return unix.Unmount(fdPath(0), int(req.Flags))
	case OpPropagation:
		if err := need(1); err != nil {
			return err
		}
		if err := checkTarget(0); err != nil {
			return err
		}
		return unix.Mount("none", fdPath(0), "", req.Flags, "")
	case OpSetReadonly:
		if err := need(1); err != nil {
			return err
		}
		if err := checkTarget(0); err != nil {
			return err
		}
		flags := 0
		if req.Recursive {
			flags = unix.AT_RECURSIVE
		}
		attr := &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}
		return unix.MountSetattr(unix.AT_FDCWD, fdPath(0), uint(flags), attr)
	default:
		return fmt.Errorf("unknown op %q", req.Op)
	}
}

func resolveFD(fd int, verifyIdentity bool) (string, error) {
	link := fmt.Sprintf("/proc/self/fd/%d", fd)
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("resolve fd %d: %w", fd, err)
	}
	var fdStat, pathStat unix.Stat_t
	if err := unix.Fstat(fd, &fdStat); err != nil {
		return "", fmt.Errorf("fstat fd %d: %w", fd, err)
	}
	if !verifyIdentity {
		return target, nil
	}
	if err := unix.Stat(target, &pathStat); err != nil {
		return "", fmt.Errorf("stat target %q: %w", target, err)
	}
	if fdStat.Dev != pathStat.Dev || fdStat.Ino != pathStat.Ino {
		return "", fmt.Errorf("target %q changed identity", target)
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
			return false // wait until the socket is readable
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
	}
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}
