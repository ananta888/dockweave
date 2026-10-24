// Package api: terminal stream server.
//
// The TermServer listens on a dedicated unix socket and speaks a tiny raw
// protocol for interactive agent terminals:
//
//	Client → Server:  "ATTACH <agentID|name> [<command...>]\n"
//	Client → Server:  "\x01W <rows> <cols>\n"     (window resize, in-band)
//	Server ↔ Client:  raw bidirectional PTY stream (after successful ATTACH)
//
// Peer credentials (SO_PEERCRED) authorize the connection identically to
// the HTTP socket. The stream is bridged to a docker exec TTY session.
package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ananta888/dockweave/internal/dockerclient"
)

// TermConfig configures the terminal server.
type TermConfig struct {
	SocketPath string
	Docker     *dockerclient.Client
	// FindContainer resolves an agent ID or name to a running container ID.
	FindContainer func(ctx context.Context, idOrName string) (string, error)
	// Auth authorizes peers. Terminal access requires the admin role
	// (an interactive agent shell is never read-only).
	Auth AuthPolicy
}

// TermServer serves interactive terminal sessions.
type TermServer struct {
	cfg TermConfig
	ln  net.Listener

	mu    sync.Mutex
	conns map[net.Conn]bool
}

// NewTermServer creates the server (call Serve to start).
func NewTermServer(cfg TermConfig) *TermServer {
	return &TermServer{cfg: cfg, conns: map[net.Conn]bool{}}
}

// Serve accepts connections until the listener closes.
func (s *TermServer) Serve() error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.mu.Lock()
		s.conns[conn] = true
		s.mu.Unlock()
		go s.handle(conn)
	}
}

// Listen opens the unix socket.
func (s *TermServer) Listen() error {
	_ = os.Remove(s.cfg.SocketPath)
	ln, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("term listen %q: %w", s.cfg.SocketPath, err)
	}
	if err := os.Chmod(s.cfg.SocketPath, 0o660); err != nil {
		ln.Close()
		return fmt.Errorf("term socket chmod: %w", err)
	}
	s.ln = ln
	return nil
}

// Close shuts the listener and all live sessions down.
func (s *TermServer) Close() error {
	if s.ln != nil {
		s.ln.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.Close()
	}
	return nil
}

// ctrlPrefix marks in-band control frames. The attach client owns the local
// tty in raw mode and interprets Ctrl-A itself (screen-style escape key);
// it forwards resize events as 0x01-prefixed newline-terminated frames and
// never forwards a raw user Ctrl-A, so the server-side stream is unambiguous.
const ctrlPrefix = 0x01

// parseAttach parses an ATTACH line into agent target and command.
// No command means: join the agent's main TTY (its own UI).
func parseAttach(line string) (agent string, cmd []string, err error) {
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != "ATTACH" {
		return "", nil, fmt.Errorf("expected ATTACH <agent> [command...]")
	}
	agent = f[1]
	cmd = f[2:]
	if len(cmd) == 0 {
		cmd = nil // no command: join the agent's main TTY
	}
	return agent, cmd, nil
}

func (s *TermServer) handle(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		conn.Close()
	}()
	if err := s.authorize(conn); err != nil {
		fmt.Fprintf(conn, "ERR auth: %v\n", err)
		return
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	agent, cmd, err := parseAttach(strings.TrimSpace(line))
	if err != nil {
		fmt.Fprintf(conn, "ERR %v\n", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cid, err := s.cfg.FindContainer(ctx, agent)
	cancel()
	if err != nil || cid == "" {
		fmt.Fprintf(conn, "ERR agent %q not running\n", agent)
		return
	}
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	// No command: join the agent's *main* TTY — the user wants to see the
	// agent's own UI (e.g. the codex TUI), not a second shell. With a
	// command: a separate exec session (shell, tools, …).
	var stream io.ReadWriteCloser
	var resize func(rows, cols int)
	if len(cmd) == 0 {
		stream, err = s.cfg.Docker.Attach(sctx, cid)
		resize = func(rows, cols int) {
			rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer rcancel()
			_ = s.cfg.Docker.Resize(rctx, cid, rows, cols)
		}
	} else {
		var execID string
		stream, execID, err = s.cfg.Docker.Exec(sctx, cid, cmd, true)
		resize = func(rows, cols int) {
			rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer rcancel()
			_ = s.cfg.Docker.ExecResize(rctx, execID, rows, cols)
		}
	}
	if err != nil {
		fmt.Fprintf(conn, "ERR attach: %v\n", err)
		return
	}
	defer stream.Close()

	done := make(chan error, 2)
	go func() {
		done <- pumpInput(conn, br, stream, resize)
	}()
	go func() {
		_, err := io.Copy(conn, stream)
		done <- err
	}()
	<-done
}

// pumpInput forwards client input to the PTY, intercepting in-band control
// frames: a 0x01 byte enters control mode until '\n'. "W rows cols" resizes
// the exec TTY. Literal 0x01 bytes must be escaped by the client as
// 0x01 0x01 (sent through as one 0x01) — clients own the raw tty and escape
// user input, so no legitimate data stream contains unescaped 0x01.
func pumpInput(conn net.Conn, br *bufio.Reader, dst io.Writer, resize func(rows, cols int)) error {
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			out := buf[:n]
			for len(out) > 0 {
				if out[0] == ctrlPrefix {
					// Read the control frame up to '\n'.
					frame, rest, found := splitFrame(out)
					if !found {
						// Need more bytes: stash incomplete frame via unread.
						// Simplest: buffer until newline arrives.
						frame, err2 := readUntilNewline(br, out)
						if err2 != nil {
							return err2
						}
						handleControlFrame(frame, resize)
						out = nil
						break
					}
					handleControlFrame(frame, resize)
					out = rest
					continue
				}
				// Pass through verbatim up to next potential 0x01.
				i := indexByte(out, ctrlPrefix)
				if i < 0 {
					if _, werr := dst.Write(out); werr != nil {
						return werr
					}
					out = nil
					break
				}
				if _, werr := dst.Write(out[:i]); werr != nil {
					return werr
				}
				out = out[i:]
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// splitFrame returns the control frame (without 0x01) if a '\n' terminates
// it inside b, plus the remaining bytes.
func splitFrame(b []byte) (frame []byte, rest []byte, found bool) {
	for i := 1; i < len(b); i++ {
		if b[i] == '\n' {
			return b[1:i], b[i+1:], true
		}
	}
	return nil, b, false
}

// readUntilNewline consumes an incomplete control frame spanning reads.
func readUntilNewline(br *bufio.Reader, pending []byte) ([]byte, error) {
	frame := pending[1:]
	for {
		c, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		if c == '\n' {
			return frame, nil
		}
		frame = append(frame, c)
		if len(frame) > 128 {
			return nil, errors.New("oversized control frame")
		}
	}
}

// handleControlFrame parses "W rows cols" (window resize). Unknown frames
// are dropped. The attach client owns the raw tty and treats Ctrl-A as its
// own escape key (screen-style), so no user data contains unescaped 0x01.
func handleControlFrame(frame []byte, resize func(rows, cols int)) {
	f := strings.Fields(string(frame))
	if len(f) == 0 {
		return
	}
	switch f[0] {
	case "W":
		if len(f) == 3 {
			var rows, cols int
			if _, err := fmt.Sscanf(f[1]+" "+f[2], "%d %d", &rows, &cols); err == nil && rows > 0 && cols > 0 {
				resize(rows, cols)
			}
		}
	}
}

// authorize enforces the AuthPolicy on the terminal socket: only admin-role
// peers (SO_PEERCRED) may open an interactive agent shell.
func (s *TermServer) authorize(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("non-unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	cerr := error(nil)
	_ = raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if cerr != nil || cred == nil {
		return errors.New("no peer credentials")
	}
	if s.cfg.Auth.RoleOf(cred.Uid) != RoleAdmin {
		return fmt.Errorf("uid %d not authorized (terminal requires admin role)", cred.Uid)
	}
	return nil
}
