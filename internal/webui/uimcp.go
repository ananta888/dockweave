package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// UIHub bridges browser UI state to the MCP server and broadcasts commands.
type UIHub struct {
	mu      sync.Mutex
	clients map[chan []byte]bool
	state   []byte
}

// NewUIHub creates an empty hub.
func NewUIHub() *UIHub {
	return &UIHub{clients: map[chan []byte]bool{}}
}

// State returns the last browser state snapshot.
func (h *UIHub) State() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.state) == 0 {
		return []byte(`{"connected":false}`)
	}
	return append([]byte(nil), h.state...)
}

// Broadcast sends one command to all connected browser clients.
func (h *UIHub) Broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- b:
		default:
		}
	}
}

// ServeWS accepts the Angular bridge WebSocket.
func (h *UIHub) ServeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.clients[ch] = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-ch:
				if err := ws.Write(ctx, websocket.MessageText, msg); err != nil {
					return
				}
			}
		}
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var v any
		if json.Unmarshal(data, &v) != nil {
			continue
		}
		h.mu.Lock()
		h.state = append([]byte(nil), data...)
		h.mu.Unlock()
	}
}

// NewMCPToken returns a random UI MCP bearer token.
func NewMCPToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mcpui_" + hex.EncodeToString(b), nil
}

// MCPHandler is a minimal MCP Streamable HTTP endpoint (JSON-RPC 2.0).
type MCPHandler struct {
	token string
	hub   *UIHub
}

// NewMCPHandler constructs the endpoint. token is required.
func NewMCPHandler(token string, hub *UIHub) *MCPHandler {
	return &MCPHandler{token: token, hub: hub}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (m *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error", http.StatusBadRequest)
		return
	}
	if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		pv := "2025-06-18"
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion != "" {
			pv = p.ProtocolVersion
		}
		w.Header().Set("Mcp-Session-Id", "dockweave-ui")
		writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "dockweave-ui", "version": "1"},
		})
	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{"tools": m.tools()})
	case "tools/call":
		var p struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeRPCError(w, req.ID, -32602, "invalid params", http.StatusBadRequest)
			return
		}
		text, err := m.call(p.Name, p.Arguments)
		if err != nil {
			writeRPCResult(w, req.ID, map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}})
			return
		}
		writeRPCResult(w, req.ID, map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}})
	case "ping":
		writeRPCResult(w, req.ID, map[string]any{})
	default:
		writeRPCError(w, req.ID, -32601, "method not found", http.StatusNotFound)
	}
}

func (m *MCPHandler) authorized(r *http.Request) bool {
	tok := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(m.token)) == 1
}

func (m *MCPHandler) tools() []map[string]any {
	return []map[string]any{
		{
			"name":        "ui_get_state",
			"description": "Return what the operator currently sees in the DockWeave web UI (view, selected agent, main agent, agent list).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "ui_show_hint",
			"description": "Show a hint at a specific UI location (CSS selector, agent row or graph node id).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":        map[string]any{"type": "string"},
					"severity":    map[string]any{"type": "string", "enum": []string{"info", "warn", "error"}},
					"selector":    map[string]any{"type": "string"},
					"agent_id":    map[string]any{"type": "string"},
					"node_id":     map[string]any{"type": "string"},
					"duration_ms": map[string]any{"type": "integer"},
				},
				"required": []string{"text"},
			},
		},
		{
			"name":        "ui_select_agent",
			"description": "Select an agent in the UI.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"agent_id": map[string]any{"type": "string"}}, "required": []string{"agent_id"}},
		},
		{
			"name":        "ui_select_view",
			"description": "Switch the UI view: agent, graph, analysis, workspaces, types.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"view": map[string]any{"type": "string"}}, "required": []string{"view"}},
		},
		{
			"name":        "ui_set_main_agent",
			"description": "Set the main agent (shown in the bottom-right chat).",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"agent_id": map[string]any{"type": "string"}}, "required": []string{"agent_id"}},
		},
	}
}

func (m *MCPHandler) call(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "ui_get_state":
		return string(m.hub.State()), nil
	case "ui_show_hint":
		cmd := map[string]any{"type": "command", "action": "show_hint"}
		for k, v := range args {
			cmd[k] = v
		}
		if _, ok := cmd["duration_ms"]; !ok {
			cmd["duration_ms"] = 10000
		}
		m.hub.Broadcast(cmd)
		return "hint sent", nil
	case "ui_select_agent":
		id, _ := args["agent_id"].(string)
		if id == "" {
			return "", fmt.Errorf("agent_id is required")
		}
		m.hub.Broadcast(map[string]any{"type": "command", "action": "select_agent", "agent_id": id})
		return "selected " + id, nil
	case "ui_select_view":
		view, _ := args["view"].(string)
		if view == "" {
			return "", fmt.Errorf("view is required")
		}
		m.hub.Broadcast(map[string]any{"type": "command", "action": "select_view", "view": view})
		return "view " + view, nil
	case "ui_set_main_agent":
		id, _ := args["agent_id"].(string)
		if id == "" {
			return "", fmt.Errorf("agent_id is required")
		}
		m.hub.Broadcast(map[string]any{"type": "command", "action": "set_main_agent", "agent_id": id})
		return "main agent " + id, nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(w http.ResponseWriter, id any, code int, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}
