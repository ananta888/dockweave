#!/bin/sh
# dockweave-herdr — herdr runtime entrypoint for DockWeave agents.
#
# Runs inside the DockWeave sandbox container. Starts the herdr headless
# server, creates a workspace pane and launches the harness as a
# herdr-managed agent. The container stays the security boundary; herdr owns
# the terminal/session inside it.
set -u

# herdr keeps config + session under $HOME/.config/herdr; DockWeave mounts
# long-term memory at /memory, so force HOME there to persist herdr and
# harness (codex) state across container recreates.
export HOME="/memory"
mkdir -p "$HOME/.config/herdr"

KIND="${DOCKWEAVE_HARNESS_KIND:-codex}"
NAME="${DOCKWEAVE_HARNESS_NAME:-agent}"
CWD="${DOCKWEAVE_HARNESS_CWD:-/workspace}"

# 1. Start the headless herdr server.
herdr server &
SERVER_PID=$!
trap 'herdr server stop >/dev/null 2>&1; kill "$SERVER_PID" 2>/dev/null' TERM INT

# 2. Wait for the server socket to become ready.
ready=0
for _ in $(seq 1 100); do
  if herdr status 2>/dev/null | grep -q 'status: running'; then ready=1; break; fi
  sleep 0.2
done
if [ "$ready" != 1 ]; then
  echo "dockweave-herdr: herdr server did not become ready" >&2
  exit 1
fi

# 3. Create the workspace and read the auto-created pane id.
out="$(herdr workspace create --cwd "$CWD" 2>/dev/null || true)"
pane="$(printf '%s' "$out" | grep -oE '"pane_id":"[^"]+"' | head -1 | cut -d'"' -f4)"
[ -z "$pane" ] && pane="w1:p1"

# 4. Launch the harness as a herdr-managed agent in the pane. Best-effort:
#    the harness may require interactive auth; herdr still owns and tracks
#    the pane in that case.
if [ -n "${DOCKWEAVE_HARNESS_ARGS:-}" ]; then
  herdr agent start "$NAME" --kind "$KIND" --pane "$pane" --timeout 30000 -- $DOCKWEAVE_HARNESS_ARGS 2>/dev/null || true
else
  herdr agent start "$NAME" --kind "$KIND" --pane "$pane" --timeout 30000 2>/dev/null || true
fi

echo "dockweave-herdr: herdr server running, agent=$NAME kind=$KIND pane=$pane" >&2

# 5. Keep the server in the foreground.
wait "$SERVER_PID"
