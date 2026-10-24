package main

// attach implements the client side of the daemon's terminal protocol:
// local PTY + raw mode, bridged to the term socket, with Ctrl-A as the
// escape key (Ctrl-A Q quit, Ctrl-A W force resize ping, Ctrl-A Ctrl-A
// sends a literal Ctrl-A).

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

func dbg(msg string) {
	if os.Getenv("DOCKWEAVE_DEBUG") == "" {
		return
	}
	f, _ := os.OpenFile("/tmp/attach-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if f != nil {
		fmt.Fprintln(f, msg)
		f.Close()
	}
}

func attachAgent(socketPath, idOrName string, cmd []string) error {
	dbg("dial " + socketPath)
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("term socket %q: %w", socketPath, err)
	}
	defer conn.Close()

	dbg("dialled, entering raw mode")
	// Enter raw mode on the current terminal.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	dbg(fmt.Sprintf("makeraw: %v", err))
	rawOK := err == nil
	if rawOK {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
	}

	args := append([]string{"ATTACH", idOrName}, cmd...)
	dbg("writing attach line")
	if _, err := conn.Write([]byte(strings.Join(args, " ") + "\n")); err != nil {
		dbg("write failed: " + err.Error())
		return err
	}

	// Start both pumps immediately and announce the size afterwards: the
	// agent UI may be silent (it only repaints on SIGWINCH or after its
	// terminal queries are answered), so waiting for output before the
	// pumps run would deadlock. The output pump answers terminal queries
	// and surfaces leading ERR lines from the server.
	br := bufio.NewReader(conn)
	errCh := make(chan error, 2)
	go func() { errCh <- pumpStdin(br, conn) }()
	go func() { errCh <- pumpOutput(conn, br) }()
	// The resize wake must arrive *after* the server attached the stream
	// to the container TTY (attach setup is well under 200ms).
	time.Sleep(250 * time.Millisecond)
	sendResize(br, conn)

	err = <-errCh
	conn.Close()
	return err
}

// pumpOutput streams server output to stdout. It answers terminal queries
// (so agent repaints never depend on focus routing) and turns a leading
// "ERR ..." line into an error return.
func pumpOutput(conn net.Conn, br *bufio.Reader) error {
	dbg("pump started")
	buf := make([]byte, 32*1024)
	var pending []byte
	var sawData bool
	for {
		n, rerr := br.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if !sawData {
				sawData = true
				trimmed := bytes.TrimLeft(chunk, "\r\n")
				if bytes.HasPrefix(trimmed, []byte("ERR ")) {
					line := trimmed
					if i := bytes.IndexByte(line, '\n'); i >= 0 {
						line = line[:i]
					}
					return errors.New(string(line))
				}
			}
			if _, werr := os.Stdout.Write(chunk); werr != nil {
				return werr
			}
			pending = append(pending, chunk...)
			var reply []byte
			pending, reply = answerTerminalQueries(pending)
			if len(reply) > 0 {
				_, _ = conn.Write(reply)
				dbg(fmt.Sprintf("reply: %q", reply))
			}
		}
		if rerr != nil {
			return nil
		}
	}
}

// answerTerminalQueries scans a growing output buffer for terminal
// queries from the agent UI and returns the replies to send back, plus
// the unconsumed remainder (queries may span reads).
func answerTerminalQueries(pending []byte) (rest, reply []byte) {
	w, h, _ := term.GetSize(int(os.Stdin.Fd()))
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	for {
		i := bytes.IndexByte(pending, 0x1b)
		if i < 0 {
			// No escape left: keep only a tail as scan state for sequences
			// split across reads (the full output already reached stdout).
			if len(pending) > 8 {
				pending = pending[len(pending)-8:]
			}
			return pending, reply
		}
		if i > 0 {
			pending = pending[i:]
		}
		rest = pending
		switch {
		case bytes.HasPrefix(pending, []byte("\x1b[6n")): // DSR: cursor position
			reply = append(reply, []byte("\x1b[1;1R")...)
			pending = pending[4:]
		case bytes.HasPrefix(pending, []byte("\x1b[14t")): // text area in pixels
			reply = append(reply, []byte(fmt.Sprintf("\x1b[4;%d;%dt", h*16, w*8))...)
			pending = pending[5:]
		case bytes.HasPrefix(pending, []byte("\x1b[18t")): // text area in chars
			reply = append(reply, []byte(fmt.Sprintf("\x1b[8;%d;%dt", h, w))...)
			pending = pending[5:]
		case bytes.HasPrefix(pending, []byte("\x1b]11;")): // OSC 11 background
			if end := bytes.IndexByte(pending, 0x07); end >= 0 {
				pending = pending[end+1:]
			} else if end := bytes.Index(pending, []byte("\x1b\\")); end >= 0 {
				pending = pending[end+2:]
			} else {
				return rest, reply // incomplete OSC, wait for more
			}
			reply = append(reply, []byte("\x1b]11;rgb:0000/0000/0000\x1b\\")...)
		default:
			// Unknown escape: skip it so the byte scan advances.
			pending = pending[1:]
		}
	}
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

// sendResize transmits the current terminal size as a control frame. A
// nudge frame (rows+1) is sent first: TUIs only repaint on SIGWINCH, and
// the kernel only signals when the size *changes*. Attaching at the size
// the agent already has would otherwise show a blank pane.
func sendResize(br *bufio.Reader, conn net.Conn) {
	w, h, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return
	}
	// Wake the agent UI with a resize to a *different* size and let it
	// repaint before settling to the true size: sending both frames
	// back-to-back lets their signals coalesce and the repaint is lost
	// (observed: idle TUIs stay blank when the size is already correct).
	dbg("resize wake")
	_, _ = fmt.Fprintf(conn, "\x01W %d %d\n", h+21, w+7)
	time.Sleep(150 * time.Millisecond)
	_, _ = fmt.Fprintf(conn, "\x01W %d %d\n", h, w)
}
