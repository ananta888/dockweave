package main

import (
	"bytes"
	"testing"
)

func TestAnswerTerminalQueries(t *testing.T) {
	// The agent UI blocks on these queries before painting; the client
	// must answer them so repainting never depends on terminal focus.
	pending := []byte("hello \x1b[6n world \x1b[14t\x1b]11;?\x1b\\ tail")
	rest, reply := answerTerminalQueries(pending)
	if len(rest) == 0 || !bytes.HasSuffix(rest, []byte(" tail")) {
		t.Fatalf("rest = %q", rest)
	}
	for _, want := range []string{"\x1b[1;1R", "\x1b[4;", "\x1b]11;rgb:"} {
		if !bytes.Contains(reply, []byte(want)) {
			t.Fatalf("reply missing %q: %q", want, reply)
		}
	}
	// Incomplete OSC stays pending.
	rest2, reply2 := answerTerminalQueries([]byte("\x1b]11;par"))
	if len(reply2) != 0 || !bytes.Contains(rest2, []byte("par")) {
		t.Fatalf("incomplete OSC must wait: %q %q", rest2, reply2)
	}
	// Plain text needs no reply; rest keeps at most a scan tail.
	rest3, reply3 := answerTerminalQueries([]byte("just text"))
	if len(reply3) != 0 || !bytes.HasSuffix([]byte("just text"), rest3) {
		t.Fatalf("plain text mishandled: %q %q", rest3, reply3)
	}
}
