package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ananta888/dockweave/internal/agentsession"
)

type fakeSessions struct {
	out string
	err error
	got string
}

func (f *fakeSessions) Prompt(ctx context.Context, idOrName, prompt string) (string, error) {
	f.got = prompt
	return f.out, f.err
}

func (f *fakeSessions) Close(string) {}

func TestStreamAgent(t *testing.T) {
	fs := &fakeSessions{out: "Hallo Welt"}
	s := &Server{Sessions: fs}
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()

	s.streamAgent(rec, req, "chatcmpl-x", 1, "demo", "ag_1", "hi")

	body := rec.Body.String()
	for _, want := range []string{
		`"object":"chat.completion.chunk"`,
		`"role":"assistant"`,
		`"content":"Hallo Welt"`,
		`"finish_reason":"stop"`,
		"data: [DONE]",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream body missing %q\n%s", want, body)
		}
	}
	if fs.got != "hi" {
		t.Errorf("prompt = %q, want hi", fs.got)
	}
}

func TestStreamAgentError(t *testing.T) {
	fs := &fakeSessions{err: agentsession.ErrNoResponse}
	s := &Server{Sessions: fs}
	rec := httptest.NewRecorder()
	s.streamAgent(rec, httptest.NewRequest("POST", "/v1/chat/completions", nil), "id", 1, "m", "a", "hi")

	body := rec.Body.String()
	if !strings.Contains(body, "agent_error") || !strings.Contains(body, "data: [DONE]") {
		t.Errorf("error stream body = %q", body)
	}
}

func TestOpenAIFailMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
		code string
	}{
		{agentsession.ErrNoResponse, 502, "agent_no_response"},
		{agentsession.ErrSessionClosed, 502, "agent_session_closed"},
		{context.DeadlineExceeded, 504, "timeout"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		(&Server{}).openAIFail(rec, c.err)
		if rec.Code != c.want {
			t.Errorf("%v: status %d, want %d", c.err, rec.Code, c.want)
		}
		if !strings.Contains(rec.Body.String(), c.code) {
			t.Errorf("%v: body %q missing %q", c.err, rec.Body.String(), c.code)
		}
	}
}

var _ = errors.Is
