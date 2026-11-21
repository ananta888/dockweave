#!/bin/sh
# dockweave-run — paperclip process-adapter entrypoint for DockWeave.
#
# Default mode (MODEL): the task is sent to the DockWeave OpenAI-compatible
# endpoint over the daemon's unix socket. The endpoint drives the live agent
# (native backend where available, TUI otherwise), so paperclip tasks are
# visible in the agent's TUI and return clean structured text.
#
# Orchestration mode (args: ["developer","orchestrate"]): the agent is the
# Lead/CEO. It returns a JSON work breakdown; this wrapper creates child
# issues, assigns them to the role agents, wakes them, and leaves the parent
# in_progress. That is the delegation step Paperclip expects from a CEO agent.
#
# Legacy mode (container starting with "dockweave-"): the old `docker exec`
# one-shot path is kept for backwards compatibility.
#
# Adapter config:
#   command: /path/to/dockweave-run.sh
#   args:    ["demo-opencode"]                     # normal worker
#   or args: ["developer", "orchestrate"]          # Lead/CEO
#   or args: ["dockweave-<cid>", "opencode", "/memory/.local/bin/opencode"]
#
# Configuration comes from the environment, optionally preloaded from
# ${DOCKWEAVE_PAPERCLIP_ENV:-~/.config/dockweave/paperclip.env} (KEY=VALUE
# lines). There are no user- or project-specific defaults; see README.md for
# every variable.
#
# Pipeline (ADR 0002): before a model turn the wrapper asks the daemon
# whether this agent may take pipeline work (fail-closed); a blocked agent is
# reported on the issue and no turn is spent. After a DONE the gates run with
# this agent as upstream and released downstream agents are woken (or a
# rework issue is opened).
set -u

ENV_FILE="${DOCKWEAVE_PAPERCLIP_ENV:-$HOME/.config/dockweave/paperclip.env}"
if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

MODEL_OR_CONTAINER="${1:-${DOCKWEAVE_MODEL:-demo-opencode}}"
MODE="${2:-}"
HARNESS_BIN="${3:-/memory/.local/bin/codex}"
API_URL="${PAPERCLIP_API_URL:-http://127.0.0.1:3100}"
API_KEY="${PAPERCLIP_API_KEY:-}"
SOCKET="${DOCKWEAVE_SOCKET:-/run/dockweave/dockweaved.sock}"
TIMEOUT="${PAPERCLIP_PROCESS_TIMEOUT:-${DOCKWEAVE_TIMEOUT:-240}}"
if [ "$MODE" = "orchestrate" ] && [ -z "${PAPERCLIP_PROCESS_TIMEOUT:-}" ] && [ -z "${DOCKWEAVE_TIMEOUT:-}" ]; then
  TIMEOUT=540
fi
ROLES_FILE="${DOCKWEAVE_PAPERCLIP_ROLES:-$HOME/.config/dockweave/paperclip-roles.json}"
# Container-side path of the shared workspace (used in the Lead prompt).
WORKSPACE="${DOCKWEAVE_PAPERCLIP_WORKSPACE:-/workspace}"
# Host path of the project workspace for gates; required for the DONE gate.
HOST_WORKSPACE="${DOCKWEAVE_HOST_WORKSPACE:-}"
# Absolute CLI path: the process adapter's PATH is not the login shell PATH.
PAPERCLIPAI="${PAPERCLIPAI_BIN:-$(command -v paperclipai 2>/dev/null || true)}"
if [ -z "$PAPERCLIPAI" ]; then
  echo "[dockweave-run] paperclipai CLI not found: set PAPERCLIPAI_BIN" >&2
fi
export PAPERCLIPAI_BIN="$PAPERCLIPAI"
DW_CLI="${DOCKWEAVE_CLI:-$(command -v dockweave 2>/dev/null || true)}"
[ -n "$DW_CLI" ] && export DOCKWEAVE_CLI="$DW_CLI"
HDIR="${DOCKWEAVE_PAPERCLIP_HELPER_DIR:-$(dirname "$0")}"

# 1. Fetch the assigned task (first inbox item via the heartbeat inbox).
TASK=""
ISSUE_ID=""
if [ -n "$API_KEY" ]; then
  INBOX=$(curl -s --max-time 15 -H "Authorization: Bearer $API_KEY" \
    "$API_URL/api/agents/me/inbox-lite" 2>/dev/null || true)
  TASK=$(printf '%s' "$INBOX" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = d.get("items") if isinstance(d, dict) else d
if not items:
    sys.exit(0)
it = items[0]
ident = it.get("identifier") or ""
title = it.get("title") or ""
desc = it.get("description") or ""
if not desc and it.get("summary"):
    desc = it.get("summary") or ""
head = (ident + ": " + title) if ident else title
print(head + ("\n\n" + desc if desc else ""))
' 2>/dev/null)
  ISSUE_ID=$(printf '%s' "$INBOX" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
items = d.get("items") if isinstance(d, dict) else d
if items:
    print(items[0].get("id") or "")
' 2>/dev/null)
fi
if [ -z "$TASK" ]; then
  if [ -n "${DOCKWEAVE_DEFAULT_TASK:-}" ]; then
    TASK="$DOCKWEAVE_DEFAULT_TASK"
  else
    echo "[dockweave-run] no assigned task; heartbeat no-op" >&2
    exit 0
  fi
fi

echo "[dockweave-run] task: $TASK" >&2

# Execution-time dependency gate: Paperclip wakes assignees on creation, so an
# issue with open blockers must not consume a model turn.
if [ "$MODE" != "orchestrate" ] && [ -n "$ISSUE_ID" ]; then
  if ! python3 "$HDIR/blocked-check.py" "$ISSUE_ID"; then
    echo "[dockweave-run] blockers open (or blocker check failed); issue queued again" >&2
    exit 0
  fi
fi

# Pipeline admission (ADR 0002): a downstream agent only works while every
# incoming gate passed on the current workspace. Unreachable daemon or an
# invalid answer = blocked. The daemon enforces the same rule at the endpoint.
case "$MODEL_OR_CONTAINER" in
  dockweave-*) ;; # legacy docker exec path: not a DockWeave agent name
  *)
    if ! python3 "$HDIR/pipeline.py" precheck "$MODEL_OR_CONTAINER" --issue "$ISSUE_ID" >&2; then
      echo "[dockweave-run] pipeline blocked for $MODEL_OR_CONTAINER; no model turn" >&2
      exit 0
    fi
    ;;
esac

# pre_run gates run before the agent; a failing pre_run gate prevents the
# run (fail-closed). Skipped when no pre_run gate is configured.
if [ "$MODE" != "orchestrate" ] && [ -n "$HOST_WORKSPACE" ]; then
  case "$MODEL_OR_CONTAINER" in
    dockweave-*) ;;
    *)
      PRE_JSON=$(DOCKWEAVE_ISSUE_ID="$ISSUE_ID" python3 "$HDIR/gates.py" run "$HOST_WORKSPACE" --phase pre_run 2>/dev/null || true)
      PRE_OK=$(printf '%s' "$PRE_JSON" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
    print("yes" if d.get("ok") else "no")
except Exception:
    print("no")' 2>/dev/null)
      if [ "$PRE_OK" != "yes" ]; then
        echo "[dockweave-run] pre_run gate failed; agent not started" >&2
        if [ -n "$ISSUE_ID" ] && [ -n "$PAPERCLIPAI" ]; then
          "$PAPERCLIPAI" issue update "$ISSUE_ID" --status todo \
            --comment "pre_run-Gate fehlgeschlagen; der Agent wurde nicht gestartet.

$(printf '%s' "$PRE_JSON" | cut -c1-1500)" --json >/dev/null 2>&1 || true
        fi
        exit 0
      fi
      ;;
  esac
fi

case "$MODEL_OR_CONTAINER" in
  dockweave-*)
    # Legacy one-shot docker exec path.
    case "$MODE" in
      codex)
        printf '%s' "$TASK" | docker exec -i "$MODEL_OR_CONTAINER" "$HARNESS_BIN" exec \
          --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox
        ;;
      opencode)
        printf '%s' "$TASK" | docker exec -i "$MODEL_OR_CONTAINER" "$HARNESS_BIN" run
        ;;
      *)
        printf '%s' "$TASK" | docker exec -i "$MODEL_OR_CONTAINER" \
          sh -c 'read p; echo "harness@$(hostname) processed: $p"'
        ;;
    esac
    exit $?
    ;;
esac

# Guard: a Lead issue that already has children only settles its gates.
if [ "$MODE" = "orchestrate" ] && [ -n "$ISSUE_ID" ]; then
  HELPER_DIR_EARLY="${DOCKWEAVE_PAPERCLIP_HELPER_DIR:-$(dirname "$0")}"
  HAS_KIDS=$(python3 -c 'import json,subprocess,sys
parent, company = sys.argv[1], sys.argv[2]
p = subprocess.run([sys.argv[3], "issue", "list", "-C", company, "--json"], capture_output=True, text=True)
try:
    items = json.loads(p.stdout)
except Exception:
    items = []
if isinstance(items, dict):
    items = items.get("issues") or items.get("data") or []
print(sum(1 for i in items if i.get("parentId") == parent))' "$ISSUE_ID" "${PAPERCLIP_COMPANY_ID:-}" "$PAPERCLIPAI" 2>/dev/null)
  if [ "${HAS_KIDS:-0}" != "0" ]; then
    echo "[dockweave-run] parent already delegated (${HAS_KIDS} children); settling gates only" >&2
    python3 "$HELPER_DIR_EARLY/parent-close.py" "$ISSUE_ID" "$HOST_WORKSPACE" 2>/dev/null || true
    exit 0
  fi
fi

PROMPT="$TASK"
if [ "$MODE" = "orchestrate" ]; then
  PROMPT=$(python3 - "$TASK" "$WORKSPACE" <<'EOF'
import sys
task, ws = sys.argv[1], sys.argv[2]
print(task + """

Du bist der Lead/CEO des DockWeave-Engineering-Teams. Zerlege das Goal in konkrete Child-Issues fuer die Rollen backend, frontend, reviewer und platform.
Gemeinsames Arbeitsverzeichnis: """ + ws + """

Antworte NUR mit einem JSON-Objekt in einem ```json-Block:
{"tasks":[{"title":"...","description":"...","role":"backend","depends_on":[]}]}
Jede Aufgabe braucht title, description und role. Keine Prosa ausserhalb des JSON-Blocks.
""")
EOF
)
fi

if [ "$MODE" != "orchestrate" ]; then
  PROMPT="$PROMPT

Abschluss-Protokoll: Pruefe deine Arbeit (Build/Tests, falls vorhanden) und beende
deine Antwort mit genau einer Statuszeile:
  STATUS: DONE        - Aufgabe vollstaendig umgesetzt und geprueft
  STATUS: BLOCKED     - du brauchst eine Entscheidung oder fehlende Rechte
  STATUS: IN_PROGRESS - Teilarbeit erledigt, Rest offen
Ohne diese Zeile gilt die Aufgabe als IN_PROGRESS."
  REVIEWER_ID=$(python3 -c 'import json,sys
try:
    print(json.load(open(sys.argv[1])).get("reviewer") or "")
except Exception:
    print("")' "$ROLES_FILE" 2>/dev/null)
  if [ -n "${PAPERCLIP_AGENT_ID:-}" ] && [ "$PAPERCLIP_AGENT_ID" = "$REVIEWER_ID" ]; then
    PROMPT="$PROMPT

Review-Vertrag: Pruefe die Arbeit der anderen Rollen (Build, Tests, Code).
Beende deine Antwort zusaetzlich mit:
  VERDICT: PASS   - freigegeben
  VERDICT: FAIL   - nicht freigegeben
  REASON: <ein Satz Begruendung>
Bei FAIL wird automatisch ein Rework-Issue beim Autor angelegt."
  fi
fi

# 2. Send the task through the DockWeave endpoint (unix socket, no token).
BODY=$(python3 -c 'import json,sys; print(json.dumps({
  "model": sys.argv[1],
  "messages": [{"role": "user", "content": sys.argv[2]}]
}))' "$MODEL_OR_CONTAINER" "$PROMPT") || exit 1

RESP=$(curl -sS --max-time "$TIMEOUT" --unix-socket "$SOCKET" \
  -X POST http://localhost/v1/chat/completions \
  -H 'Content-Type: application/json' -H "X-DockWeave-Issue: $ISSUE_ID" -d "$BODY" 2>&1)
STATUS=$?
if [ "$STATUS" != 0 ]; then
  echo "[dockweave-run] endpoint request failed (curl $STATUS): $RESP" >&2
  exit 1
fi
# The daemon enforces the pipeline even if the precheck was skipped or raced.
printf '%s' "$RESP" | python3 "$HDIR/pipeline.py" handle-409 "$MODEL_OR_CONTAINER" --issue "$ISSUE_ID" >&2
if [ $? -eq 3 ]; then
  echo "[dockweave-run] endpoint refused: pipeline_blocked" >&2
  exit 0
fi

CONTENT=$(printf '%s' "$RESP" | python3 -c 'import json,sys
raw = sys.stdin.read()
try:
    d = json.loads(raw)
    print(d["choices"][0]["message"]["content"])
except Exception as e:
    print("[dockweave-run] invalid endpoint response: %s" % e, file=sys.stderr)
    print(raw[:500], file=sys.stderr)
    sys.exit(1)
') || exit 1
printf '%s\n' "$CONTENT"

HELPER_DIR="${DOCKWEAVE_PAPERCLIP_HELPER_DIR:-$(dirname "$0")}"
export PAPERCLIPAI_BIN="$PAPERCLIPAI"

if [ "$MODE" = "orchestrate" ] && [ -n "$ISSUE_ID" ] && [ -f "$ROLES_FILE" ]; then
  # A woken Lead may just be confirming an accepted gate: close and stop.
  PRE=$(python3 "$HELPER_DIR/parent-close.py" "$ISSUE_ID" "$HOST_WORKSPACE" 2>/dev/null || true)
  if printf '%s' "$PRE" | grep -q '^PARENT'; then
    echo "[dockweave-run] $PRE" >&2
    exit 0
  fi

  # Extract the JSON work breakdown from the answer and create child issues.
  PLAN=$(printf '%s' "$CONTENT" | python3 "$HELPER_DIR/plan-extract.py" 2>/dev/null || true)
  if [ -n "$PLAN" ]; then
    ISSUE_JSON=$("$PAPERCLIPAI" issue get "$ISSUE_ID" --json 2>/dev/null || true)
    META=$(printf '%s' "$ISSUE_JSON" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("\t\t"); sys.exit(0)
print("%s\t%s\t%s" % (d.get("companyId") or "", d.get("goalId") or "", d.get("projectId") or ""))' 2>/dev/null)
    COMPANY=$(printf '%s' "$META" | cut -f1)
    GOAL=$(printf '%s' "$META" | cut -f2)
    PROJECT=$(printf '%s' "$META" | cut -f3)
    [ -n "$COMPANY" ] || COMPANY="${PAPERCLIP_COMPANY_ID:-}"

    CHILDREN=$(printf '%s' "$PLAN" | python3 "$HELPER_DIR/plan-delegate.py" \
      "$ROLES_FILE" "$COMPANY" "$ISSUE_ID" "$GOAL" "$PROJECT" "$WORKSPACE" 2>/dev/null || true)

    if printf '%s' "$CHILDREN" | grep -q '^CHILD'; then
      SUMMARY=$(printf 'Lead-Plan erstellt. Child-Issues:\n%s\n\n%s' "$CHILDREN" "$CONTENT")
      "$PAPERCLIPAI" issue update "$ISSUE_ID" --status in_progress --comment "$SUMMARY" --json >/dev/null 2>&1 || true
      echo "[dockweave-run] delegated child issues:" >&2
      printf '%s\n' "$CHILDREN" >&2
      python3 "$HELPER_DIR/wake-ready.py" "$COMPANY" "$ISSUE_ID" 2>/dev/null || true
      exit 0
    fi
    echo "[dockweave-run] plan found but no child issue could be created" >&2
    printf '%s\n' "$CHILDREN" >&2
  else
    echo "[dockweave-run] no JSON plan in the Lead answer" >&2
  fi
  python3 "$HELPER_DIR/parent-close.py" "$ISSUE_ID" "$HOST_WORKSPACE" 2>/dev/null || true
fi

# Record the answer AND change the source state. Paperclip marks an issue
# blocked ("recovery needed") when a successful run leaves the source state
# unchanged, so the update is part of a successful run.
if [ -n "$ISSUE_ID" ] && [ -x "$PAPERCLIPAI" ]; then
  STATUS="${DOCKWEAVE_PAPERCLIP_STATUS:-}"
  if [ -z "$STATUS" ]; then
    STATUS=$(printf '%s' "$CONTENT" | python3 -c 'import re,sys
raw = sys.stdin.read().upper()
hits = re.findall(r"STATUS:\s*(DONE|BLOCKED|IN_PROGRESS|IN PROGRESS)", raw)
mapping = {"DONE": "done", "BLOCKED": "blocked",
           "IN_PROGRESS": "in_progress", "IN PROGRESS": "in_progress"}
print(mapping.get(hits[-1], "in_progress") if hits else "in_progress")' 2>/dev/null)
    [ -n "$STATUS" ] || STATUS=in_progress
  fi

  VERDICT=$(printf '%s' "$CONTENT" | python3 -c 'import re,sys
m = re.findall(r"VERDICT:\s*(PASS|FAIL)", sys.stdin.read().upper())
print(m[-1] if m else "")' 2>/dev/null)

  NOTE=""

  # Deterministic gate: a self-reported DONE only counts if the project gate passes.
  if [ "$MODE" != "orchestrate" ] && [ "$STATUS" = "done" ]; then
    if [ -z "$HOST_WORKSPACE" ]; then
      GATE_JSON='{"ok":false,"results":[{"id":"config","ok":false,"output":"DOCKWEAVE_HOST_WORKSPACE is not set; the DONE gate cannot run"}]}'
    else
      GATE_JSON=$(DOCKWEAVE_ISSUE_ID="$ISSUE_ID" DOCKWEAVE_UPSTREAM_AGENT="$MODEL_OR_CONTAINER" \
        python3 "$HELPER_DIR/gates.py" run "$HOST_WORKSPACE" --phase after_run,post_run 2>/dev/null || true)
    fi
    GATE_OK=$(printf '%s' "$GATE_JSON" | python3 -c 'import json,sys
try:
    print("yes" if json.load(sys.stdin).get("ok") else "no")
except Exception:
    print("unknown")' 2>/dev/null)
    # Route the pipeline after the gate run: wake released downstream agents,
    # open a rework issue for `rework`, report `deny` (ADR 0002, D-2).
    if [ -n "$HOST_WORKSPACE" ]; then
      ROUTE=$(printf '%s' "$GATE_JSON" | python3 "$HELPER_DIR/pipeline.py" route \
        --upstream "$MODEL_OR_CONTAINER" --issue "$ISSUE_ID" --run-json - 2>/dev/null || true)
      if [ -n "$ROUTE" ] && ! printf '%s' "$ROUTE" | grep -q '^NONE'; then
        echo "[dockweave-run] pipeline routing: $ROUTE" >&2
        NOTE="$NOTE

Pipeline:
$ROUTE"
      fi
    fi
    # Anything but a confirmed pass (including an unreadable runner answer)
    # keeps the issue open (fail-closed).
    if [ "$GATE_OK" != "yes" ]; then
      STATUS=in_progress
      NOTE=$(printf '%s' "$GATE_JSON" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    print(""); sys.exit(0)
lines = ["", "Gate fehlgeschlagen - STATUS: DONE wird nicht akzeptiert."]
for r in d.get("results", []):
    if r.get("ok"):
        continue
    lines.append("")
    lines.append("### Gate %s (exit %s)" % (r.get("id"), r.get("exit")))
    lines.append("```")
    lines.append(r.get("output") or "")
    lines.append("```")
print("\n".join(lines))' 2>/dev/null)
      echo "[dockweave-run] gate failed -> in_progress" >&2
    fi
  fi

  # Reviewer verdict drives the review gate and rework.
  if [ "$VERDICT" = "FAIL" ]; then
    STATUS=in_progress
    REASON=$(printf '%s' "$CONTENT" | python3 -c 'import re,sys
raw = sys.stdin.read()
m = re.search(r"REASON:\s*(.+)", raw, re.I)
print(m.group(1).strip() if m else "Review FAIL")' 2>/dev/null)
    REWORK=$(python3 "$HELPER_DIR/paperclip-gate.py" rework "$ISSUE_ID" "$REASON" 2>/dev/null || true)
    echo "[dockweave-run] review FAIL: $REWORK" >&2
    # Mirror the verdict into DockWeave (idempotent, audited evidence).
    if [ -n "$DW_CLI" ] && [ -x "$DW_CLI" ]; then
      "$DW_CLI" gate review "$ISSUE_ID" --verdict FAIL --note "$REASON" --json >/dev/null 2>&1 || true
    fi
    NOTE="$NOTE\n\nRework: $REWORK"
  fi

  COMMENT="$CONTENT"
  [ -n "$NOTE" ] && COMMENT="$CONTENT$NOTE"
  if [ -z "$(printf '%s' "$COMMENT" | tr -d '[:space:]')" ]; then
    COMMENT="(keine Textantwort vom Agenten)"
    [ "$STATUS" = "done" ] && STATUS=in_progress
  fi

  UPD_OUT=$("$PAPERCLIPAI" issue update "$ISSUE_ID" --status "$STATUS" --comment "$COMMENT" --json 2>&1)
  if [ $? -ne 0 ]; then
    echo "[dockweave-run] could not update paperclip issue $ISSUE_ID: $(printf '%s' "$UPD_OUT" | tr '\n' ' ' | cut -c1-300)" >&2
  fi

  # Unblock dependents, then close the parent if every gate agrees.
  ISSUE_JSON=$("$PAPERCLIPAI" issue get "$ISSUE_ID" --json 2>/dev/null || true)
  PARENT_ID=$(printf '%s' "$ISSUE_JSON" | python3 -c 'import json,sys
try:
    print(json.load(sys.stdin).get("parentId") or "")
except Exception:
    print("")' 2>/dev/null)
  COMPANY_ID=$(printf '%s' "$ISSUE_JSON" | python3 -c 'import json,sys
try:
    print(json.load(sys.stdin).get("companyId") or "")
except Exception:
    print("")' 2>/dev/null)
  if [ -n "$PARENT_ID" ] && [ -n "$COMPANY_ID" ]; then
    python3 "$HELPER_DIR/wake-ready.py" "$COMPANY_ID" "$PARENT_ID" 2>/dev/null || true
    python3 "$HELPER_DIR/parent-close.py" "$ISSUE_ID" "$HOST_WORKSPACE" 2>/dev/null || true
  fi
fi
