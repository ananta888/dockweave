// Package mcp implements a minimal Model Context Protocol server (stdio
// transport) exposing READ-ONLY DockWeave tools. It runs unprivileged and
// talks to the daemon exclusively through the public API socket.
//
// Protocol: JSON-RPC 2.0 over newline-delimited JSON on stdin/stdout,
// compatible with the MCP stdio transport (initialize / tools/list /
// tools/call / ping).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ananta888/dockweave/pkg/sdk"
)

// Server is the MCP stdio server.
type Server struct {
	Client *sdk.Client
	// Out/In default to os.Stdout/os.Stdin (injectable for tests).
	In  io.Reader
	Out io.Writer
}

// latest protocol version this server speaks.
const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes.
const (
	errParse     = -32700
	errMethod    = -32601
	errInvalidPr = -32602
	errInternal  = -32603
)

// Serve runs until the input stream ends.
func (s *Server) Serve(ctx context.Context) error {
	in := s.In
	out := s.Out
	if in == nil || out == nil {
		return fmt.Errorf("mcp: In/Out must be set")
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.reply(out, nil, nil, &rpcError{errParse, "parse error"})
			continue
		}
		// Notifications (no id) get no response.
		if len(req.ID) == 0 {
			continue
		}
		result, rerr := s.dispatch(ctx, req)
		s.reply(out, req.ID, result, rerr)
	}
	return sc.Err()
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "dockweave", "version": "1"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{errInvalidPr, "invalid params"}
		}
		return s.callTool(ctx, p.Name, p.Arguments)
	default:
		return nil, &rpcError{errMethod, "method not found: " + req.Method}
	}
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func toolDefs() []toolDef {
	obj := func(props map[string]any) any {
		return map[string]any{"type": "object", "properties": props}
	}
	return []toolDef{
		{Name: "list_agents", Description: "List all DockWeave agents with state", InputSchema: obj(nil)},
		{Name: "get_agent", Description: "Get one agent by ID or name", InputSchema: obj(map[string]any{
			"id": map[string]any{"type": "string"},
		})},
		{Name: "list_workspaces", Description: "List registered workspaces", InputSchema: obj(nil)},
		{Name: "list_mounts", Description: "List mount states (optionally per agent)", InputSchema: obj(map[string]any{
			"agent": map[string]any{"type": "string"},
		})},
		{Name: "get_activity", Description: "Live activity snapshot of all agents", InputSchema: obj(nil)},
		{Name: "get_conflicts", Description: "Current multi-agent file conflicts", InputSchema: obj(nil)},
		{Name: "list_events", Description: "Recent audit events", InputSchema: obj(map[string]any{
			"limit": map[string]any{"type": "integer"},
		})},
	}
}

func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) (any, *rpcError) {
	var a map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &rpcError{errInvalidPr, "invalid arguments"}
		}
	}
	var payload any
	var err error
	switch name {
	case "list_agents":
		payload, err = s.Client.ListAgents(ctx)
	case "get_agent":
		id, _ := a["id"].(string)
		if id == "" {
			return nil, &rpcError{errInvalidPr, "id required"}
		}
		payload, err = s.Client.GetAgent(ctx, id)
	case "list_workspaces":
		payload, err = s.Client.ListWorkspaces(ctx)
	case "list_mounts":
		agent, _ := a["agent"].(string)
		payload, err = s.Client.ListMounts(ctx, agent)
	case "get_activity":
		payload, err = s.Client.Activity(ctx)
	case "get_conflicts":
		payload, err = s.Client.Conflicts(ctx)
	case "list_events":
		limit := 50
		if v, ok := a["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		payload, err = s.Client.Events(ctx, limit)
	default:
		return nil, &rpcError{errMethod, "unknown tool: " + name}
	}
	if err != nil {
		return textResult("error: " + err.Error()), nil
	}
	b, merr := json.MarshalIndent(payload, "", "  ")
	if merr != nil {
		return nil, &rpcError{errInternal, merr.Error()}
	}
	return textResult(string(b)), nil
}

func textResult(text string) any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
}

func (s *Server) reply(w io.Writer, id json.RawMessage, result any, rerr *rpcError) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		resp["error"] = rerr
	} else {
		resp["result"] = result
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}

// unused helper guard
var _ = strings.TrimSpace
