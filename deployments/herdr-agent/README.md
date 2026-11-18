# DockWeave herdr runtime

The `herdr` runtime runs the agent's harness inside a herdr-managed pane. The
container's main TTY is the herdr client, so the DockWeave web terminal and the
native TUI attach show the same live agent.

## Build

```bash
cp "$(command -v herdr)" deployments/herdr-agent/herdr
docker build -t dockweave/herdr-agent deployments/herdr-agent
```

## Create the agent

```bash
dockweave agent create herdr-codex \
  --type codex --image dockweave/herdr-agent --runtime herdr \
  --cmd codex --cmd --dangerously-bypass-approvals-and-sandbox
dockweave agent start herdr-codex
```

The entrypoint:

1. starts `herdr server`,
2. creates a workspace/pane,
3. starts the harness as a herdr agent (`herdr agent start --kind <type>`),
4. focuses the agent pane,
5. attaches the main TTY to herdr (and reattaches if the client detaches).

## Harness credentials

The herdr image contains the Codex CLI. For the DockWeave broker path, write
the same config/token as a normal Codex agent into the agent's memory:

```
/memory/.codex/config.toml              # model_providers.dockweave
/memory/.config/dockweave/codex-token   # dws_… session
```

Then grant the agent in the broker:

```bash
dockweave credential grant herdr-codex codex-main --models '*' --rpm 60 --concurrent 2
dockweave broker session create herdr-codex codex-main --ttl 8h   # write to the token file
```

## Control

```bash
docker exec <cid> env HOME=/memory herdr agent list
docker exec <cid> env HOME=/memory herdr agent prompt herdr-codex "..." --wait
docker exec <cid> env HOME=/memory herdr pane read <pane>
```

The main TTY is the herdr TUI; the focused pane is the agent. `Ctrl-B` enters
herdr prefix mode for pane/session control.

## Notes

- `--cmd` (verbatim argv) is preferred for herdr agents; the entrypoint strips
  a leading binary name because `herdr agent start --kind` launches the
  canonical binary itself.
- Herdr keeps its server socket under `/memory/.config/herdr`, so sessions
  survive container restarts.
- Broker sessions are short-lived; add a refresh timer for long-running herdr
  agents (same pattern as `deployments/credential-broker`).
