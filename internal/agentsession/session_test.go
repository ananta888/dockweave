package agentsession

import "testing"

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"\x1b[0mhello\x1b[1;32m world\x1b[0m": "hello world",
		"\x1b[?25lspinner\x1b[?25h":             "spinner",
		"line1\rline2":                          "line1line2",
		"plain text":                            "plain text",
		"\x1b]0;title\x07body":                  "body",
	}
	for in, want := range cases {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}
