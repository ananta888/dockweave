#!/usr/bin/env python3
"""DockWeave pipeline helper for the Paperclip wrapper (ADR 0002).

Usage:
  pipeline.py precheck <agent> [--issue ID]
      Ask the daemon whether <agent> may take pipeline work. Exit 0 when
      allowed, 3 when blocked. An unreachable daemon or an invalid answer
      counts as blocked (fail-closed). With --issue the block is reported on
      the Paperclip issue once per reason set.
  pipeline.py handle-409 <agent> [--issue ID] < endpoint-response
      Treat a 409 pipeline_blocked answer of /v1/chat/completions like a
      blocked precheck. Exit 3 for pipeline_blocked, 1 for anything else.
  pipeline.py route --upstream <agent> [--issue ID] [--run-json FILE]
      After a gate run of the upstream agent: wake released downstream
      agents (allow / wake:<agent>), open exactly one rework issue per gate
      run for `rework`, and report `deny`. Prints one line per action.

Environment:
  DOCKWEAVE_CLI                    dockweave CLI (default: PATH lookup)
  PAPERCLIPAI_BIN                  paperclipai CLI (default: PATH lookup)
  PAPERCLIP_COMPANY_ID             company for wake/issue create
  PAPERCLIP_AGENT_ID               Paperclip id of the agent running now
  DOCKWEAVE_PAPERCLIP_AGENT_MAP    JSON file {dockweave-agent-id-or-name: paperclip-agent-id}
  DOCKWEAVE_PIPELINE_STATE_DIR     idempotency markers (default $XDG_STATE_HOME/dockweave/pipeline)
  DOCKWEAVE_PIPELINE_BLOCKED_STATUS  Paperclip status for a blocked issue (default todo)

The Paperclip <-> DockWeave agent mapping is explicit; an unmapped agent is
reported (UNMAPPED) and never guessed.
"""
import hashlib
import fcntl
import json
import os
import re
import shutil
import subprocess
import sys
from contextlib import contextmanager

BLOCKED = 3


def dockweave_cli():
    cand = os.environ.get("DOCKWEAVE_CLI", "").strip()
    if cand:
        return cand
    return shutil.which("dockweave") or ""


def paperclip_cli():
    cand = os.environ.get("PAPERCLIPAI_BIN", "").strip()
    if cand:
        return cand
    return shutil.which("paperclipai") or ""


def run(cmd, timeout=120):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired as exc:
        return subprocess.CompletedProcess(cmd, 124, "", str(exc))
    except OSError as exc:
        return subprocess.CompletedProcess(cmd, 127, "", str(exc))


def dw(args):
    """Run the dockweave CLI with --json; return (returncode, parsed|None, raw)."""
    cli = dockweave_cli()
    if not cli:
        return 127, None, "dockweave CLI not found (set DOCKWEAVE_CLI)"
    p = run([cli] + args + ["--json"])
    try:
        data = json.loads(p.stdout) if p.stdout.strip() else None
    except json.JSONDecodeError:
        data = None
    return p.returncode, data, (p.stderr or p.stdout).strip()


def valid_action(action):
    return isinstance(action, str) and (action in ("allow", "deny", "rework") or
                                       re.fullmatch(r"wake:[A-Za-z0-9][A-Za-z0-9._-]{0,127}", action) is not None)


def valid_admission(data, agent):
    if not isinstance(data, dict) or type(data.get("allowed")) is not bool:
        return False
    if not isinstance(data.get("agent_id"), str) or not data["agent_id"]:
        return False
    if agent not in (data.get("agent"), data["agent_id"]):
        return False
    if not valid_action(data.get("action")) or not isinstance(data.get("edges"), list):
        return False
    for edge in data["edges"]:
        if not isinstance(edge, dict) or type(edge.get("satisfied")) is not bool:
            return False
        if any(not isinstance(edge.get(k, ""), str) for k in
               ("gate_id", "run_id", "code", "reason", "message", "upstream_agent_id", "edge_id")):
            return False
        if not valid_action(edge.get("action")):
            return False
    return not data["allowed"] or (all(e["satisfied"] for e in data["edges"]) and
                                   (data["action"] == "allow" or data["action"].startswith("wake:")))


def unavailable(agent, reason):
    return {"agent": agent, "allowed": False, "action": "deny", "edges": [{
        "gate_id": "", "code": "unavailable", "satisfied": False, "action": "deny",
        "message": "pipeline admission unavailable: " + reason[:300]}]}


def admission(agent):
    """Return an admission dict; failures become a synthetic blocked answer."""
    rc, data, raw = dw(["pipeline", "status", agent])
    if valid_admission(data, agent):
        if rc == 0 and data["allowed"]:
            return data
        if rc in (0, BLOCKED):
            data["allowed"] = False if rc == BLOCKED else data["allowed"]
            return data
    return unavailable(agent, raw or "invalid answer")


def blocking(adm):
    return [e for e in adm.get("edges") or [] if not e.get("satisfied")]


def describe(adm):
    lines = ["Pipeline-Sperre fuer %s (Aktion: %s):" % (adm.get("agent") or adm.get("agent_id") or "?", adm.get("action") or "deny")]
    for e in blocking(adm):
        lines.append("- Gate %s: %s%s%s" % (
            e.get("gate_id") or "-", e.get("code") or "?",
            (" (Lauf %s)" % e["run_id"]) if e.get("run_id") else "",
            (" - " + e["message"]) if e.get("message") else ""))
    return "\n".join(lines)


def state_dir():
    d = os.environ.get("DOCKWEAVE_PIPELINE_STATE_DIR", "").strip()
    if not d:
        base = os.environ.get("XDG_STATE_HOME") or os.path.join(os.path.expanduser("~"), ".local", "state")
        d = os.path.join(base, "dockweave", "pipeline")
    os.makedirs(d, exist_ok=True)
    return d


@contextmanager
def action_state(key):
    """Serialize local workers; pending records survive interruption for reconciliation."""
    name = hashlib.sha256(key.encode()).hexdigest()[:32]
    path = os.path.join(state_dir(), name)
    fd = os.open(path + ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            with open(path, encoding="utf-8") as stream:
                data = json.load(stream)
            if not isinstance(data, dict):
                data = {"status": "pending"}
        except FileNotFoundError:
            data = {}
        except json.JSONDecodeError:
            data = {"status": "pending"}  # legacy marker: reconcile, never assume success
        yield path, data


def save_state(path, data):
    temporary = path + ".tmp"
    fd = os.open(temporary, os.O_CREAT | os.O_TRUNC | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(data, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    directory = os.open(os.path.dirname(path), os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def perform_once(key, command):
    with action_state(key) as (path, state):
        if state.get("status") == "done":
            return True, ""
        if state.get("status") == "pending":
            return False, "previous attempt has unknown outcome; reconcile before retry"
        save_state(path, {"status": "pending"})
        result = run(command)
        if result.returncode == 0:
            save_state(path, {"status": "done"})
            return True, ""
        if result.returncode != 124:
            save_state(path, {"status": "retry"})
        return False, (result.stderr or result.stdout or "command failed")[:200]


def report_blocked(adm, issue):
    text = describe(adm)
    print(text)
    if not issue:
        return
    pc = paperclip_cli()
    if not pc:
        print("WARN\tpaperclipai not found; issue %s not updated" % issue, file=sys.stderr)
        return
    codes = ",".join(sorted("%s=%s:%s" % (e.get("gate_id"), e.get("code"), e.get("run_id") or "") for e in blocking(adm)))
    status = os.environ.get("DOCKWEAVE_PIPELINE_BLOCKED_STATUS", "todo").strip() or "todo"
    args = ["issue", "update", issue, "--status", status, "--json"]
    key = "blocked|%s|%s" % (issue, codes)
    with action_state(key) as (path, state):
        if state.get("status") == "pending":
            print("ERR\tcomment outcome unknown; reconcile before retry", file=sys.stderr)
            return
        comment = state.get("status") != "done"
        if comment:
            args[4:4] = ["--comment", text + "\n\nDer Agent arbeitet erst, wenn das Gate auf dem aktuellen Stand bestanden ist."]
            save_state(path, {"status": "pending"})
        result = run([pc] + args)
        if comment and result.returncode != 124:
            save_state(path, {"status": "done" if result.returncode == 0 else "retry"})
        if result.returncode != 0:
            print("ERR\tblocked issue update failed", file=sys.stderr)


def arg(flag, argv, default=""):
    if flag in argv:
        i = argv.index(flag)
        if i + 1 < len(argv):
            return argv[i + 1]
    return default


def cmd_precheck(argv):
    if not argv or argv[0].startswith("--"):
        print("usage: pipeline.py precheck <agent> [--issue ID]", file=sys.stderr)
        return 2
    adm = admission(argv[0])
    if adm.get("allowed") is True:
        print("ALLOWED\t%s\t%s" % (argv[0], adm.get("action") or "allow"))
        return 0
    report_blocked(adm, arg("--issue", argv))
    return BLOCKED


def cmd_handle_409(argv):
    if not argv or argv[0].startswith("--"):
        print("usage: pipeline.py handle-409 <agent> [--issue ID]", file=sys.stderr)
        return 2
    raw = sys.stdin.read()
    try:
        body = json.loads(raw)
    except json.JSONDecodeError:
        return 1
    err = body.get("error") if isinstance(body, dict) else None
    if not isinstance(err, dict) or err.get("code") != "pipeline_blocked":
        return 1
    adm = body.get("admission") or {"agent": argv[0], "allowed": False, "action": err.get("action") or "deny",
                                    "edges": err.get("reasons") or []}
    if not valid_admission(adm, argv[0]):
        adm = unavailable(argv[0], "invalid pipeline_blocked response")
    adm["allowed"] = False
    report_blocked(adm, arg("--issue", argv))
    return BLOCKED


def load_map():
    path = os.environ.get("DOCKWEAVE_PAPERCLIP_AGENT_MAP", "").strip()
    if not path:
        return {}
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
        return {str(k): str(v) for k, v in data.items()} if isinstance(data, dict) else {}
    except Exception as exc:  # noqa: BLE001
        print("WARN\tagent map %s unreadable: %s" % (path, exc), file=sys.stderr)
        return {}


def paperclip_id(mapping, *keys):
    for k in keys:
        if k and k in mapping:
            return mapping[k]
    return ""


def company_of(issue):
    company = os.environ.get("PAPERCLIP_COMPANY_ID", "").strip()
    parent = ""
    pc = paperclip_cli()
    if issue and pc:
        p = run([pc, "issue", "get", issue, "--json"])
        try:
            d = json.loads(p.stdout)
            company = company or d.get("companyId") or ""
            parent = d.get("parentId") or ""
        except Exception:
            pass
    return company, parent


def wake(pc_agent, company, reason):
    pc = paperclip_cli()
    if not (pc and pc_agent and company):
        return False
    p = run([pc, "agent", "wake", pc_agent, "-C", company, "--source", "assignment",
             "--trigger", "manual", "--reason", reason, "--json"])
    return p.returncode == 0


def rework(pc_upstream, company, parent, gate, run_id, reason):
    pc = paperclip_cli()
    if not (pc and pc_upstream and company and gate and run_id):
        return "", "paperclipai, company, upstream mapping or gate run missing"
    tag = "[gate %s / run %s]" % (gate, run_id)
    key = "rework|%s|%s|%s|%s" % (company, pc_upstream, gate, run_id)
    with action_state(key) as (path, state):
        ident = state.get("id", "") if state.get("status") == "done" else ""
        if not ident:
            listed = run([pc, "issue", "list", "-C", company, "--json"])
            if listed.returncode != 0:
                return "", "cannot reconcile existing rework issues"
            try:
                items = json.loads(listed.stdout)
            except json.JSONDecodeError:
                return "", "invalid issue list"
            if isinstance(items, dict):
                items = items.get("issues", items.get("data"))
            if not isinstance(items, list) or any(not isinstance(it, dict) for it in items):
                return "", "invalid issue list"
            for item in items:
                if tag in str(item.get("title", "")):
                    ident = item.get("identifier") or item.get("id") or ""
                    break
            if ident:
                save_state(path, {"status": "done", "id": ident})
            elif state.get("status") == "pending":
                return "", "creation outcome unknown; reconcile before retry"
            else:
                args = [pc, "issue", "create", "-C", company,
                        "--title", "Rework: Gate %s %s" % (gate, tag),
                        "--description", "Gate %s, run %s: %s" % (gate, run_id, reason),
                        "--assignee-agent-id", pc_upstream, "--json"]
                if parent:
                    args += ["--parent-id", parent]
                save_state(path, {"status": "pending"})
                created = run(args)
                if created.returncode != 0:
                    if created.returncode != 124:
                        save_state(path, {"status": "retry"})
                    return "", (created.stderr or created.stdout or "create failed")[:200]
                try:
                    result = json.loads(created.stdout)
                except json.JSONDecodeError:
                    return "", "creation response unknown; reconcile before retry"
                if isinstance(result, dict):
                    ident = result.get("identifier") or result.get("id") or ""
                if not isinstance(ident, str) or not ident:
                    return "", "created issue identity unknown; reconcile before retry"
                save_state(path, {"status": "done", "id": ident})
    ok, error = wake_once(key + "|wake", pc_upstream, company, "Rework nach Gate %s" % gate)
    return (ident, "") if ok else ("", "issue %s exists; wake failed: %s" % (ident, error))


def wake_once(key, pc_agent, company, reason):
    pc = paperclip_cli()
    if not (pc and pc_agent and company):
        return False, "paperclipai, company or agent mapping missing"
    return perform_once(key, [pc, "agent", "wake", pc_agent, "-C", company,
                              "--source", "assignment", "--trigger", "manual",
                              "--reason", reason, "--json"])


def cmd_route(argv):
    upstream = arg("--upstream", argv)
    issue = arg("--issue", argv)
    if not upstream:
        print("usage: pipeline.py route --upstream <agent> [--issue ID] [--run-json FILE]", file=sys.stderr)
        return 2
    run_info = {}
    rj = arg("--run-json", argv)
    if rj:
        try:
            with (sys.stdin if rj == "-" else open(rj, encoding="utf-8")) as fh:
                run_info = json.load(fh)
        except Exception:
            print("ERR\tinvalid run JSON", file=sys.stderr)
            return 1
        if not isinstance(run_info, dict):
            return 1
    rc, state, raw = dw(["pipeline", "list"])
    if rc != 0 or not isinstance(state, dict) or not isinstance(state.get("edges"), list):
        print("ERR\tpipeline list failed: %s" % raw[:200])
        return 1
    for e in state["edges"]:
        # upstream_agent_id is optional in the daemon model (omitempty);
        # downstream_agent_id is mandatory.
        if (not isinstance(e, dict) or not isinstance(e.get("downstream_agent_id"), str)
                or not e["downstream_agent_id"]
                or not isinstance(e.get("upstream_agent_id", ""), str)):
            print("ERR\tinvalid pipeline edge")
            return 1
    if not state["edges"]:
        # Nothing to route. Checked before resolving the upstream so that
        # targets unknown to the daemon (legacy dockweave-<cid>) are not errors.
        print("NONE\tno pipeline edges")
        return 0
    up = admission(upstream)
    up_id = up.get("agent_id") or ""
    if not up_id:
        print("ERR\tupstream agent %s unknown to the daemon" % upstream)
        return 1
    downs = []
    for e in state["edges"]:
        if e.get("upstream_agent_id") == up_id and e.get("downstream_agent_id") not in downs:
            downs.append(e.get("downstream_agent_id"))
    if not downs:
        print("NONE\tno pipeline edges from %s" % upstream)
        return 0

    mapping = load_map()
    company, parent = company_of(issue)
    run_id = run_info.get("id") or ""
    failed = False
    for down in downs:
        adm = admission(down)
        if not adm.get("agent_id"):
            print("ERR\tdownstream admission unavailable: %s" % down)
            failed = True
            continue
        name = adm.get("agent") or down
        mine = [e for e in adm.get("edges") or [] if e.get("upstream_agent_id") == up_id]
        if adm.get("allowed") is True:
            action = adm.get("action") or "allow"
            target = action.split(":", 1)[1] if action.startswith("wake:") else down
            target_name = name if target == down else target
            pc_target = paperclip_id(mapping, target, target_name)
            if not pc_target:
                print("UNMAPPED\t%s\tno Paperclip agent mapped (DOCKWEAVE_PAPERCLIP_AGENT_MAP)" % target_name)
                failed = True
                continue
            evidence = ",".join(sorted(e.get("run_id", "") for e in mine)) or run_id
            key = "wake|%s|%s|%s|%s|%s" % (company, up_id, pc_target, state.get("revision"), evidence)
            ok, error = wake_once(key, pc_target, company, "Pipeline frei: Gate nach %s bestanden" % upstream)
            failed = failed or not ok
            if not ok:
                print("ERR\twake: " + error, file=sys.stderr)
            print("%s\t%s\t%s" % ("WAKE" if ok else "ERR", target_name, action))
            continue
        for e in mine:
            if e.get("satisfied"):
                continue
            if e.get("action") == "rework" and e.get("code") not in ("tamper", "unavailable", "fingerprint_unavailable"):
                pc_up = paperclip_id(mapping, up_id, upstream)
                ident, err = rework(pc_up, company, parent, e.get("gate_id"), e.get("run_id") or run_id,
                                    e.get("reason") or e.get("message") or "")
                failed = failed or not ident
                print("REWORK\t%s\t%s" % (e.get("gate_id"), ident) if ident else "ERR\trework %s: %s" % (e.get("gate_id"), err))
            else:
                print("DENY\t%s\t%s\t%s\t%s" % (name, e.get("gate_id"), e.get("code"), e.get("reason") or e.get("message") or ""))
    return 1 if failed else 0


def main():
    if len(sys.argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    cmd, argv = sys.argv[1], sys.argv[2:]
    if cmd == "precheck":
        return cmd_precheck(argv)
    if cmd == "handle-409":
        return cmd_handle_409(argv)
    if cmd == "route":
        return cmd_route(argv)
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError) as exc:
        print("ERR\tpipeline state failure: %s" % exc, file=sys.stderr)
        sys.exit(1)
