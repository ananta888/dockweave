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
import json
import os
import shutil
import subprocess
import sys

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
    except Exception as exc:  # noqa: BLE001 - reported as failure
        return subprocess.CompletedProcess(cmd, 127, "", str(exc))


def dw(args):
    """Run the dockweave CLI with --json; return (returncode, parsed|None, raw)."""
    cli = dockweave_cli()
    if not cli:
        return 127, None, "dockweave CLI not found (set DOCKWEAVE_CLI)"
    p = run([cli] + args + ["--json"])
    try:
        data = json.loads(p.stdout) if p.stdout.strip() else None
    except Exception:
        data = None
    return p.returncode, data, (p.stderr or p.stdout).strip()


def admission(agent):
    """Return an admission dict; failures become a synthetic blocked answer."""
    rc, data, raw = dw(["pipeline", "status", agent])
    if isinstance(data, dict) and isinstance(data.get("allowed"), bool) and "edges" in data:
        if rc == 0 and data["allowed"]:
            return data
        if rc in (0, BLOCKED):
            data["allowed"] = False if rc == BLOCKED else data["allowed"]
            return data
    return {
        "agent": agent, "allowed": False, "action": "deny",
        "edges": [{"gate_id": "", "code": "unavailable", "satisfied": False,
                   "message": "pipeline admission unavailable: %s" % (raw[:300] or "no answer")}],
    }


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


def once(key):
    """True the first time key is seen (idempotency marker)."""
    name = hashlib.sha256(key.encode()).hexdigest()[:32]
    path = os.path.join(state_dir(), name)
    try:
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError:
        return False
    with os.fdopen(fd, "w") as fh:
        fh.write(key + "\n")
    return True


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
    # Comment once per reason set; repeated heartbeats only re-queue.
    if once("blocked|%s|%s" % (issue, codes)):
        args[4:4] = ["--comment", text + "\n\nDer Agent arbeitet erst, wenn das Gate auf dem aktuellen Stand bestanden ist."]
    run([pc] + args)


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
    except Exception:
        return 1
    err = body.get("error") if isinstance(body, dict) else None
    if not isinstance(err, dict) or err.get("code") != "pipeline_blocked":
        return 1
    adm = body.get("admission") or {"agent": argv[0], "allowed": False, "action": err.get("action") or "deny",
                                    "edges": err.get("reasons") or []}
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
    if not (pc and pc_upstream and company):
        return "", "paperclipai, company or upstream mapping missing"
    tag = "[gate %s / run %s]" % (gate, run_id or "-")
    # Idempotent per gate run: local marker plus a title tag lookup.
    p = run([pc, "issue", "list", "-C", company, "--json"])
    try:
        items = json.loads(p.stdout)
        if isinstance(items, dict):
            items = items.get("issues") or items.get("data") or []
    except Exception:
        items = []
    for it in items or []:
        if tag in (it.get("title") or ""):
            return it.get("identifier") or it.get("id") or "existing", ""
    if not once("rework|%s|%s|%s" % (company, gate, run_id)):
        return "existing", ""
    args = [pc, "issue", "create", "-C", company,
            "--title", "Rework: Gate %s fehlgeschlagen %s" % (gate, tag),
            "--description", "Das Gate %s hat den Lauf %s nicht bestanden.\n\n%s\n\n"
            "Bitte nachbessern und erneut STATUS: DONE melden." % (gate, run_id or "-", reason or ""),
            "--assignee-agent-id", pc_upstream, "--json"]
    if parent:
        args += ["--parent-id", parent]
    c = run(args)
    if c.returncode != 0:
        return "", (c.stderr or c.stdout).strip()[:200]
    try:
        d = json.loads(c.stdout)
    except Exception:
        d = {}
    wake(pc_upstream, company, "Rework nach Gate %s" % gate)
    return d.get("identifier") or d.get("id") or "created", ""


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
            run_info = {}
    up = admission(upstream)
    up_id = up.get("agent_id") or ""
    if not up_id:
        print("ERR\tupstream agent %s unknown to the daemon" % upstream)
        return 1
    rc, state, raw = dw(["pipeline", "list"])
    if rc != 0 or not isinstance(state, dict):
        print("ERR\tpipeline list failed: %s" % raw[:200])
        return 1
    downs = []
    for e in state.get("edges") or []:
        if e.get("upstream_agent_id") == up_id and e.get("downstream_agent_id") not in downs:
            downs.append(e.get("downstream_agent_id"))
    if not downs:
        print("NONE\tno pipeline edges from %s" % upstream)
        return 0

    mapping = load_map()
    company, parent = company_of(issue)
    run_id = run_info.get("id") or ""
    for down in downs:
        adm = admission(down)
        name = adm.get("agent") or down
        mine = [e for e in adm.get("edges") or [] if e.get("upstream_agent_id") == up_id]
        if adm.get("allowed") is True:
            action = adm.get("action") or "allow"
            target = action.split(":", 1)[1] if action.startswith("wake:") else down
            target_name = name if target == down else target
            pc_target = paperclip_id(mapping, target, target_name)
            if not pc_target:
                print("UNMAPPED\t%s\tno Paperclip agent mapped (DOCKWEAVE_PAPERCLIP_AGENT_MAP)" % target_name)
                continue
            ok = wake(pc_target, company, "Pipeline frei: Gate nach %s bestanden" % upstream)
            print("%s\t%s\t%s" % ("WAKE" if ok else "ERR", target_name, action))
            continue
        for e in mine:
            if e.get("satisfied"):
                continue
            if e.get("code") == "failed" and e.get("action") == "rework":
                pc_up = paperclip_id(mapping, up_id, upstream) or os.environ.get("PAPERCLIP_AGENT_ID", "")
                ident, err = rework(pc_up, company, parent, e.get("gate_id"), e.get("run_id") or run_id,
                                    e.get("reason") or e.get("message") or "")
                print("REWORK\t%s\t%s" % (e.get("gate_id"), ident) if ident else "ERR\trework %s: %s" % (e.get("gate_id"), err))
            else:
                print("DENY\t%s\t%s\t%s\t%s" % (name, e.get("gate_id"), e.get("code"), e.get("reason") or e.get("message") or ""))
    return 0


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
    sys.exit(main())
