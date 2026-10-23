# Interactive Agent Terminals

## CLI

```bash
dockweave agent attach codex-01              # login shell
dockweave agent attach codex-01 -- python3   # specific command
```

## TUI

Agents view → select with ↑/↓ → `t` opens a full-screen terminal on the
selected agent (implemented via terminal handover to `dockweave agent attach`).

## Client escape key

`Ctrl-A` is the client escape key (screen-style):

| Keys | Action |
|---|---|
| `Ctrl-A` `Q` | detach and quit |
| `Ctrl-A` `W` | force window-resize ping |
| `Ctrl-A` `Ctrl-A` | (dropped — literal Ctrl-A cannot traverse control framing) |

## Protocol & security

- Separate unix socket `/run/dockweave/dockweaved.term.sock` (mode 0660).
- `SO_PEERCRED` authorization identical to the HTTP socket (root + socket group).
- Raw bidirectional PTY stream bridged to `docker exec -it`.
- In-band resize frames: `\x01W <rows> <cols>\n`; the client owns the raw
  tty and never forwards user Ctrl-A bytes, keeping the stream unambiguous.
- No `docker.sock` exposure to agents; all exec happens in the daemon.

## Limitations

- Sequential sessions (one terminal at a time per client; the TUI hands the
  terminal over and redraws on return — no split panes in v1).
- Literal `Ctrl-A` input cannot be delivered (documented client limitation).
