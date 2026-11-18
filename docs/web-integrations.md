# Web integrations: herdr & paperclip

The Angular UI exposes both integrations in the **Integrations** tab and adds
runtime presets to the agent dialog.

## Architecture (SOLID / DDD)

| Layer | Location | Responsibility |
|---|---|---|
| Domain | `ui/web/src/app/core/domain/integrations.ts` | Runtime/herdr/paperclip types, no HTTP |
| Application services | `core/services/runtime-presets.service.ts`, `herdr.service.ts`, `paperclip.service.ts` | Use cases, single responsibility |
| Infrastructure | `core/api.service.ts`, `HttpClient` | Transport only |
| Presentation | `components/integrations/*` | Orchestration and rendering |

Components depend on application services, never on endpoints directly. Runtime
defaults (image/command/type) live in one place (`RuntimePresetsService`).

## Agent creation

The create dialog has a **Runtime** selector:

- `docker` — direct harness (default)
- `herdr` — sets image `dockweave/herdr-agent`, type `codex`, argv command
- `paperclip` — heartbeat runtime marker

For `herdr`, the command is sent as **argv** (not `sh -c`) because herdr starts
the canonical binary itself.

## Herdr panel

- Agent picker (agents with `runtime=herdr`)
- Live status from herdr (`agent list`, `pane list`)
- **Prepare broker access**: creates/reuses a `codex-main` grant, mints an 8h
  `dws_` session, writes `~/.codex/config.toml` and the token file into the
  agent's memory (never returns the token), then the agent must be restarted.
- **Prompt**: uses the daemon's native herdr backend (`herdr agent prompt
  --wait`) and returns the answer from the Codex rollout.
- **Terminal**: embeds the existing xterm.js terminal, which shows the herdr UI
  (the container's main TTY).

API: `GET/POST /api/v1/agents/{id}/herdr/{status,prompt,prepare}`.

## Paperclip panel

- Embeds the **original Paperclip UI** in an iframe
  (`DOCKWEAVE_PAPERCLIP_URL`, default `http://127.0.0.1:3100`).
- Quick actions via the web gateway's local CLI adapter:
  - company/agents/issues status
- **Ziel-Agent**: assigns a DockWeave agent to the Paperclip process agent (`adapterConfig.args[0]`); applies to the next wake.
  - create a task assigned to an agent
  - wake an agent
- API (same-origin, cookie auth):
  - `GET /paperclip/config`
  - `GET /paperclip/status?company=`
  - `POST /paperclip/issues`
  - `POST /paperclip/agents/{id}/wake`

The paperclip panel is an adapter: the original UI remains the source of truth;
the quick actions are conveniences.

### DockWeave agents as Paperclip assignees

The panel lists DockWeave agents and creates a Paperclip process agent
`DW-<name>` for each (`adapterConfig.args = ["<name>"]`). Paperclip's
**Choose assignee** then selects the DockWeave agent 1:1. **Sync all** does this
for every running DockWeave agent; existing `DW-*` agents are updated.

## Notes

- `herdr prepare` requires `BROKER_ADMIN_SOCKET` in `/etc/dockweave/dockweaved.conf` so the daemon can mint the session.

- The herdr prepare endpoint is currently Codex-only (`codex-main` credential).
- Herdr prompts require the agent to be started through the herdr entrypoint
  (pane + rollout present).
- The paperclip iframe is local-only; if the UI is opened from another host,
  configure `DOCKWEAVE_PAPERCLIP_URL` accordingly.
