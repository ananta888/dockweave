// OpenAI-compatible endpoint: exposes running DockWeave agents as models.
//
//	GET  /v1/models               → agent list as OpenAI models
//	POST /v1/chat/completions     → drive one agent, OpenAI-format response
//
// The endpoint reuses the agent's live terminal session (agentsession), so a
// prompt sent through the API is also visible in the DockWeave web terminal /
// TUI and the session stays manually usable.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ananta888/dockweave/internal/agentsession"
	"github.com/ananta888/dockweave/internal/model"
)

// openAIModels lists running agents as OpenAI models.
func (s *Server) openAIModels(w http.ResponseWriter, r *http.Request) {
	agents, err := s.Agents.List(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	data := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		if a.State != model.AgentRunning {
			continue // a stopped agent cannot be driven through its TTY
		}
		data = append(data, map[string]any{
			"id":       a.Name,
			"object":   "model",
			"created":  a.CreatedAt.Unix(),
			"owned_by": "dockweave",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

type openAIChatReq struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIChatCompletions drives one agent with the last user message and
// returns the response in OpenAI chat.completions shape.
func (s *Server) openAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	if s.Sessions == nil {
		writeErr(w, http.StatusNotImplemented, "not_implemented", errAgentSessionsUnavailable)
		return
	}
	var req openAIChatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err)
		return
	}
	if req.Model == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errNoModel)
		return
	}
	prompt := lastUserMessage(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		writeErr(w, http.StatusBadRequest, "invalid", errNoPrompt)
		return
	}

	// Resolve the model to an agent (name or id).
	agent, err := s.Agents.Get(r.Context(), req.Model)
	if err != nil {
		writeErr(w, http.StatusNotFound, "model_not_found", err)
		return
	}

	id := "chatcmpl-" + time.Now().UTC().Format("20060102150405.000000000")
	created := time.Now().Unix()

	if req.Stream {
		s.streamAgent(w, r, id, created, req.Model, agent.ID, prompt)
		return
	}

	content, err := s.Sessions.Prompt(r.Context(), agent.ID, prompt)
	if err != nil {
		s.openAIFail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
}

// streamAgent emits the captured response as an SSE stream. The capture itself
// is not incremental (the agent's TUI renders a whole turn), so the content is
// delivered as one delta when the turn completes, followed by a stop chunk.
// Keep-alive comments prevent proxies and clients from timing out while the
// agent is thinking.
func (s *Server) streamAgent(w http.ResponseWriter, r *http.Request, id string, created int64, model, agentID, prompt string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", errors.New("streaming unsupported by server"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeData := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			}},
		}
	}

	if !writeData(chunk(map[string]any{"role": "assistant"}, nil)) {
		return
	}

	type result struct {
		content string
		err     error
	}
	done := make(chan result, 1)
	ctx := r.Context()
	go func() {
		c, err := s.Sessions.Prompt(ctx, agentID, prompt)
		done <- result{content: c, err: err}
	}()

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			fl.Flush()
		case res := <-done:
			if res.err != nil {
				b, _ := json.Marshal(map[string]any{
					"error": map[string]string{"message": res.err.Error(), "type": "agent_error"},
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				fl.Flush()
				return
			}
			if !writeData(chunk(map[string]any{"content": res.content}, nil)) {
				return
			}
			if !writeData(chunk(map[string]any{}, "stop")) {
				return
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			fl.Flush()
			return
		}
	}
}

// openAIFail maps agent-session failures to OpenAI-style HTTP errors.
func (s *Server) openAIFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentsession.ErrNoResponse):
		writeErr(w, http.StatusBadGateway, "agent_no_response", err)
	case errors.Is(err, agentsession.ErrSessionClosed):
		writeErr(w, http.StatusBadGateway, "agent_session_closed", err)
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "timeout", err)
	default:
		s.fail(w, err)
	}
}

func lastUserMessage(msgs []openAIMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// Errors for the OpenAI endpoint.
var (
	errAgentSessionsUnavailable = &staticError{"agent sessions are not configured on this daemon"}
	errNoModel                  = &staticError{"missing model (agent name)"}
	errNoPrompt                 = &staticError{"missing user message"}
)

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }

// AgentSessions is the subset of agentsession.Manager used by the OpenAI
// endpoint. Keeping it an interface makes the handlers testable.
type AgentSessions interface {
	Prompt(ctx context.Context, idOrName, prompt string) (string, error)
	// Close releases a cached session; it is a no-op if none exists.
	Close(idOrName string)
}
