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
	"encoding/json"
	"net/http"
	"strings"
	"time"
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
	if req.Stream {
		writeErr(w, http.StatusBadRequest, "invalid", errStreamingUnsupported)
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

	content, err := s.Sessions.Prompt(r.Context(), agent.ID, prompt)
	if err != nil {
		s.fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-" + time.Now().UTC().Format("20060102150405.000000000"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	})
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
	errStreamingUnsupported     = &staticError{"streaming is not supported"}
)

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }
