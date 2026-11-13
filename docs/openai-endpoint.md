# OpenAI-compatible endpoint (Mode 2)

DockWeave exposes its **running** agents as OpenAI chat models. Unlike a
one-shot adapter, a request is typed into the agent's own TUI on the
container's main TTY (`/dev/console`), so the turn is visible in the DockWeave
web terminal, the TUI and tmux, and the human can keep using the same session
afterwards. This mode is called **Mode 2**.

Both endpoints are served by `dockweaved` and proxied by the web gateway under
`/v1/`.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/models` | list running agents as models |
| `POST` | `/v1/chat/completions` | drive one agent (streaming and non-streaming) |

## Authentication

The web gateway requires the normal `dockweave_web` session cookie (or
`?token=…`). The daemon socket path uses the usual socket policy
(`ADMIN_UIDS`).

## Example

```bash
curl -s http://127.0.0.1:8790/v1/chat/completions \
  -H "Cookie: dockweave_web=$TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "demo-opencode",
    "messages": [{"role": "user", "content": "Nenne eine Farbe."}]
  }'
```

The model is the agent name (or id). The **last** `user` message is sent; prior
messages are ignored because the live TUI already holds the conversation.

Streaming is supported (`"stream": true`). The capture is turn-based, so the
content arrives as a single delta when the turn completes, followed by a
`stop` chunk and `data: [DONE]`. Keep-alive comments are emitted every 15 s
while the agent is thinking.

## How a turn is driven

1. The session attaches to the container's main TTY and keeps a background
   reader running. This is required: an unread PTY eventually blocks the TUI.
2. A virtual ANSI terminal renders the stream into a screen grid.
3. The prompt is typed, then **Enter is sent as a separate write after the
   harness has reacted**. Some TUIs (codex) drop an Enter that arrives in the
   same read as the text.
4. Multi-line prompts are wrapped in bracketed-paste sequences so embedded
   newlines do not submit early.
5. The response is extracted after the last echo of the prompt and stripped of
   TUI chrome (spinners, `+ Thought`, `Build · … tok/s`, codex status lines).
6. The turn is considered finished when the harness prints its completion
   footer (`Worked for …` for codex, `… tok/s` for opencode) plus a quiet
   period; without a footer there is a 30 s fallback.

## Operational notes

- **One turn at a time per agent.** Concurrent requests to the same agent are
  serialized by a per-session lock.
- **The TTY is resized** to the emulator grid (default 160×100) on first attach,
  because absolute cursor addressing must match. Any other terminal attached
  to the same container sees that size too.
- **Sessions are cached** per agent and dropped when the container goes away,
  or when the agent is stopped/restarted/removed through the API.
- **A leftover draft is cleared** (backspaces) only after a submit that was not
  accepted, so a human typing in the TUI is not disturbed in normal operation.
- Errors are OpenAI-shaped: `agent_no_response` and `agent_session_closed`
  return `502`, a client timeout returns `504`.

## Limitations

- `usage` token counts are reported as `0` — the harness does not expose them.
- The endpoint cannot distinguish a permission modal from "thinking"; a modal
  that stays open ends in `agent_no_response`.
- Response extraction is heuristic per harness (opencode, codex). New harnesses
  need their own completion/chrome rules.
- Capture is **viewport-bound**: the harness only paints its own screen, so an
  answer longer than the TTY height is truncated to what is visible. The
  default 100-row grid covers typical answers; raise `ScreenRows` for more.
