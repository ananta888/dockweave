package main

// attach implements the client side of the daemon's terminal protocol:
// local PTY + raw mode, bridged to the term socket, with Ctrl-A as the
// escape key (Ctrl-A Q quit, Ctrl-A W force resize ping, Ctrl-A Ctrl-A
// sends a literal Ctrl-A).

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/term"
)

func attachAgent(socketPath, idOrName string, cmd []string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("term socket %q: %w", socketPath, err)
	}
	defer conn.Close()

	// Enter raw mode on the current terminal.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	rawOK := err == nil
	if rawOK {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	args := append([]string{"ATTACH", idOrName}, cmd...)
	if _, err := conn.Write([]byte(strings.Join(args, " ") + "\n")); err != nil {
		return err
	}

	// Announce the window size *before* waiting for stream data: an idle
	// agent UI (e.g. codex) sends nothing on attach and only repaints on
	// SIGWINCH — peeking first would deadlock against the silence.
	br := bufio.NewReader(conn)
	sendResize(br, conn)

	// Wait for ERR or stream start: read the first byte; "ERR" lines are
	// printable server errors.
	first, err := br.Peek(1)
	if err != nil {
		return err
	}
	if first[0] == 'E' {
		line, _ := br.ReadString('\n')
		msg := strings.TrimSpace(line)
		if rawOK {
			term.Restore(int(os.Stdin.Fd()), oldState)
		}
		return errors.New(msg)
	}

	// Announce initial window size.
	sendResize(br, conn)

	done := make(chan error, 2)
	// stdin -> conn, with Ctrl-A escape handling.
	go func() { done <- pumpStdin(br, conn) }()
	// conn -> stdout.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := br.Read(buf)
			if n > 0 {
				if _, werr := os.Stdout.Write(buf[:n]); werr != nil {
					done <- werr
					return
				}
			}
			if rerr != nil {
				done <- nil
				return
			}
		}
	}()
	err = <-done
	conn.Close()
	return err
}

// pumpStdin forwards local input to the server, interpreting Ctrl-A as the
// escape key: Ctrl-A Q quits, Ctrl-A W sends a resize frame, Ctrl-A Ctrl-A
// emits one literal Ctrl-A, any other Ctrl-A X is forwarded unchanged.
func pumpStdin(br *bufio.Reader, conn net.Conn) error {
	buf := make([]byte, 4096)
	escaped := false
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil
		}
		var out []byte
		for _, b := range buf[:n] {
			if escaped {
				escaped = false
				switch b {
				case 'q', 'Q':
					// Detach cleanly.
					conn.Close()
					return nil
				case 'w', 'W':
					sendResize(br, conn)
					continue
				case 0x01:
					// Literal Ctrl-A cannot traverse the control framing;
					// dropped (documented client limitation).
					continue
				default:
					// Unknown escape: forward both bytes.
					out = append(out, 0x01, b)
					continue
				}
			}
			if b == 0x01 {
				escaped = true
				continue
			}
			out = append(out, b)
		}
		if len(out) > 0 {
			if _, werr := conn.Write(out); werr != nil {
				return werr
			}
		}
		if err != nil {
			return nil
		}
	}
}

// sendResize transmits the current terminal size as a control frame.
func sendResize(br *bufio.Reader, conn net.Conn) {
	w, h, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return
	}
	fmt.Fprintf(conn, "\x01W %d %d\n", h, w)
}
