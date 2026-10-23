package api

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestParseAttach(t *testing.T) {
	a, cmd, err := parseAttach("ATTACH codex-01")
	if err != nil || a != "codex-01" || cmd != nil {
		t.Fatalf("basic attach: %v %v %v", a, cmd, err)
	}
	a, cmd, err = parseAttach("ATTACH ag_1 /bin/bash -lc ls")
	if err != nil || a != "ag_1" || len(cmd) != 3 || cmd[0] != "/bin/bash" {
		t.Fatalf("attach with cmd: %v %v %v", a, cmd, err)
	}
	if _, _, err := parseAttach("EXEC ag_1"); err == nil {
		t.Fatal("non-ATTACH must fail")
	}
	if _, _, err := parseAttach("ATTACH"); err == nil {
		t.Fatal("missing agent must fail")
	}
}

func TestPumpInputPassthrough(t *testing.T) {
	var dst bytes.Buffer
	resized := false
	in := "hello\nworld\x00" + string([]byte{0x02, 0x03})
	err := pumpInput(nil, bufio.NewReader(strings.NewReader(in)), &dst, func(rows, cols int) {
		resized = true
	})
	if err != nil {
		t.Fatal(err)
	}
	if dst.String() != in {
		t.Fatalf("passthrough corrupted: %q", dst.String())
	}
	if resized {
		t.Fatal("no control frame sent but resize fired")
	}
}

func TestPumpInputResizeFrame(t *testing.T) {
	var dst bytes.Buffer
	var gotRows, gotCols int
	resized := false
	in := "pre" + "\x01W 40 120\n" + "post"
	err := pumpInput(nil, bufio.NewReader(strings.NewReader(in)), &dst, func(rows, cols int) {
		resized, gotRows, gotCols = true, rows, cols
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resized || gotRows != 40 || gotCols != 120 {
		t.Fatalf("resize frame not handled: %v %d %d", resized, gotRows, gotCols)
	}
	if dst.String() != "prepost" {
		t.Fatalf("control frame leaked into stream: %q", dst.String())
	}
}

func TestPumpInputSplitControlFrame(t *testing.T) {
	// Control frame split across two reads (emulated via slow reader chunks).
	var dst bytes.Buffer
	var gotRows int
	in := "\x01W 2" + "4 80\nDATA"
	err := pumpInput(nil, bufio.NewReader(strings.NewReader(in)), &dst, func(rows, cols int) {
		gotRows = rows
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRows != 24 {
		t.Fatalf("split frame not parsed: rows=%d", gotRows)
	}
	if dst.String() != "DATA" {
		t.Fatalf("payload wrong: %q", dst.String())
	}
}

func TestPumpInputGarbageControlFrame(t *testing.T) {
	var dst bytes.Buffer
	in := "\x01NOTACOMMAND xyz\nok"
	if err := pumpInput(nil, bufio.NewReader(strings.NewReader(in)), &dst, func(int, int) {}); err != nil {
		t.Fatal(err)
	}
	if dst.String() != "ok" {
		t.Fatalf("unknown frame must be dropped, payload corrupted: %q", dst.String())
	}
}

func TestTermServerErrHandshake(t *testing.T) {
	// In-memory server without docker: handshake error path.
	dir := t.TempDir()
	ts := NewTermServer(TermConfig{SocketPath: dir + "/term.sock"})
	if err := ts.Listen(); err != nil {
		t.Fatal(err)
	}
	go ts.Serve()
	defer ts.Close()
	conn, err := net.Dial("unix", dir+"/term.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GARBAGE\n"))
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "ERR") {
		t.Fatalf("expected ERR, got %q", buf[:n])
	}
}
