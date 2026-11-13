package agentsession

import "testing"

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"\x1b[0mhello\x1b[1;32m world\x1b[0m": "hello world",
		"\x1b[?25lspinner\x1b[?25h":           "spinner",
		"line1\rline2":                        "line1line2",
		"plain text":                          "plain text",
		"\x1b]0;title\x07body":                "body",
	}
	for in, want := range cases {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScreenRendering(t *testing.T) {
	s := NewScreen(5, 20)
	s.Feed([]byte("hello"))
	if got := s.Text(); got != "hello" {
		t.Errorf("Text() = %q, want %q", got, "hello")
	}
}

func TestScreenCursorAndNewline(t *testing.T) {
	s := NewScreen(5, 20)
	s.Feed([]byte("line1\r\nline2"))
	if got, want := s.Text(), "line1\nline2"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestScreenEraseLine(t *testing.T) {
	s := NewScreen(5, 20)
	s.Feed([]byte("AAAA\r\nBBBB"))
	// Move to row 1, col 1, erase to end of line -> "AAAA" becomes empty.
	s.Feed([]byte("\x1b[1;1H\x1b[K"))
	if got, want := s.Text(), "\nBBBB"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}

func TestScreenScroll(t *testing.T) {
	s := NewScreen(3, 20)
	s.Feed([]byte("a\r\nb\r\nc\r\nd"))
	if got, want := s.Text(), "b\nc\nd"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if got, want := s.TextWithScrollback(), "a\nb\nc\nd"; got != want {
		t.Errorf("TextWithScrollback() = %q, want %q", got, want)
	}
}

func TestDiffScreens(t *testing.T) {
	before := "h1\nh2\nh3"
	after := "h2\nh3\nprompt\nresp1\nresp2"
	if got, want := diffScreens(before, after), "prompt\nresp1\nresp2"; got != want {
		t.Errorf("diffScreens = %q, want %q", got, want)
	}
}

func TestExtractAfterPrompt(t *testing.T) {
	screen := "old round\n┃  Begrüße mich\nHallo\n\n┃  Bitte tu was\n┃\n\n   Antwort hier\n/workspace/x ctrl+p commands"
	if got, want := extractAfterPrompt(screen, "Bitte tu was"), "Antwort hier"; got != want {
		t.Errorf("extractAfterPrompt = %q, want %q", got, want)
	}
}

func TestExtractCodexChrome(t *testing.T) {
	screen := "› Sag hallo\n\n• Hallo! Schön, dass du da bist.\n\n  Worked for 2s • 8:22 AM\n\n› Ask Codex to do anything\n\n  ← for agents · ? for shortcuts"
	if got, want := extractAfterPrompt(screen, "Sag hallo"), "Hallo! Schön, dass du da bist."; got != want {
		t.Errorf("extractAfterPrompt = %q, want %q", got, want)
	}
}

func TestExtractMultilinePrompt(t *testing.T) {
	// The echoed prompt spans two lines; anchoring on the first line would
	// leak the second prompt line into the response.
	screen := "┃  Antworte nur mit OK.\n┃  Zweite Zeile Kontext.\n\n   OK\n/workspace/x"
	if got, want := extractAfterPrompt(screen, "Antworte nur mit OK.\nZweite Zeile Kontext."), "OK"; got != want {
		t.Errorf("extractAfterPrompt = %q, want %q", got, want)
	}
}

func TestExtractAnchorScrolledAway(t *testing.T) {
	// The prompt echo is not visible (long answer scrolled the viewport); the
	// fallback must return only the current turn, not earlier history.
	screen := "old answer\nBuild · Big Pickle · 1s · 2 tok/s\ncurrent answer 1\ncurrent answer 2\nBuild · Big Pickle · 2s · 3 tok/s"
	got := extractAfterPrompt(screen, "prompt that scrolled away")
	want := "current answer 1\ncurrent answer 2"
	if got != want {
		t.Errorf("extractAfterPrompt = %q, want %q", got, want)
	}
}

func TestExtractNoAnchorNoFooter(t *testing.T) {
	if got := extractAfterPrompt("some unrelated history", "missing prompt"); got != "" {
		t.Errorf("extractAfterPrompt = %q, want empty", got)
	}
}

func TestHasCompletion(t *testing.T) {
	if !hasCompletion("answer\nBuild · Big Pickle · 1.3s · 25.5 tok/s") {
		t.Error("opencode completion footer not detected")
	}
	if !hasCompletion("answer\nWorked for 2s • 8:22 AM") {
		t.Error("codex completion footer not detected")
	}
	if hasCompletion("answer") {
		t.Error("plain answer must not count as completion")
	}
}
