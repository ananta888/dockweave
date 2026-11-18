package mounthelper

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// Client talks to dockweave-mountd.
type Client struct {
	Socket string
	// Timeout per request (default 30s).
	Timeout time.Duration
}

// Available reports whether the helper socket exists.
func (c *Client) Available() bool {
	if c == nil || c.Socket == "" {
		return false
	}
	return pathExists(c.Socket)
}

// Do sends one operation with its directory file descriptors.
func (c *Client) Do(ctx context.Context, req Request, fds []int) error {
	if c == nil || c.Socket == "" {
		return fmt.Errorf("mountd: no socket configured")
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return fmt.Errorf("mountd dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}

	raw, err := encode(req)
	if err != nil {
		return err
	}
	if len(fds) == 0 {
		if _, err := conn.Write(raw); err != nil {
			return fmt.Errorf("mountd write: %w", err)
		}
	} else {
		sc, err := conn.(*net.UnixConn).SyscallConn()
		if err != nil {
			return fmt.Errorf("mountd syscall conn: %w", err)
		}
		oob := unix.UnixRights(fds...)
		var sendErr error
		if err := sc.Write(func(fd uintptr) bool {
			sendErr = unix.Sendmsg(int(fd), raw, oob, nil, 0)
			if sendErr == unix.EAGAIN || sendErr == unix.EWOULDBLOCK {
				return false
			}
			return true
		}); err != nil {
			return fmt.Errorf("mountd raw conn: %w", err)
		}
		if sendErr != nil {
			return fmt.Errorf("mountd sendmsg: %w", sendErr)
		}
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("mountd read: %w", err)
	}
	return decodeResponse(buf[:n])
}

func pathExists(p string) bool {
	var st unix.Stat_t
	return unix.Lstat(p, &st) == nil
}
