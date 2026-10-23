# MCP Server (Optional)

DockWeave ships a Model Context Protocol server exposing **read-only**
tools for AI assistants. It never exposes mutating operations — no mounts,
grants or lifecycle changes.

## Start

```bash
dockweave mcp            # JSON-RPC 2.0 / MCP stdio transport
```

Example client configuration (any MCP-capable assistant):

```json
{
  "mcpServers": {
    "dockweave": {
      "command": "dockweave",
      "args": ["mcp"],
      "env": { "DOCKWEAVE_SOCKET": "/run/dockweave/dockweaved.sock" }
    }
  }
}
```

## Tools

| Tool | Description |
|---|---|
| `list_agents` | All agents with state |
| `get_agent` | One agent by ID or name |
| `list_workspaces` | Registered workspaces |
| `list_mounts` | Mount states (optional agent filter) |
| `get_activity` | Live activity snapshot (evidence-based) |
| `get_conflicts` | Current multi-agent file conflicts |
| `list_events` | Recent audit events |

Write-capable methods are intentionally absent; unknown tools return a
JSON-RPC error.
