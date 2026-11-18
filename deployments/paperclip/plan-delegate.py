#!/usr/bin/env python3
"""Create the Lead's child issues in Paperclip and wake the assigned agents.

Usage:
  plan-extract.py | plan-delegate.py ROLES_FILE COMPANY PARENT_ISSUE GOAL PROJECT WORKSPACE

Every created issue gets the parent + goal + project of the Lead issue, the
role agent from ROLES_FILE as assignee, and the shared workspace in the
description. Existing children with the same title are reused, so re-running a
Lead heartbeat stays idempotent instead of duplicating work.
"""
import json
import os
import subprocess
import sys

CLI = os.environ.get("PAPERCLIPAI_BIN") or "/home/krusty/.local/bin/paperclipai"


def run(args, local=False):
    env = dict(os.environ)
    if local:
        # Agent run keys are scoped to the running agent; waking another agent
        # needs the local board identity instead.
        for key in ("PAPERCLIP_API_KEY", "PAPERCLIP_AGENT_ID", "PAPERCLIP_RUN_ID"):
            env.pop(key, None)
    return subprocess.run([CLI] + args, capture_output=True, text=True, env=env)


def existing_children(company, parent):
    p = run(["issue", "list", "-C", company, "--json"])
    if p.returncode != 0:
        return {}
    try:
        items = json.loads(p.stdout)
    except Exception:
        return {}
    if isinstance(items, dict):
        items = items.get("issues") or items.get("data") or []
    out = {}
    for it in items:
        if it.get("parentId") == parent:
            out[(it.get("title") or "").strip()] = it.get("identifier") or it.get("id")
    return out


def main():
    if len(sys.argv) < 7:
        print("usage: plan-delegate.py ROLES COMPANY PARENT GOAL PROJECT WORKSPACE", file=sys.stderr)
        return 2
    roles_file, company, parent, goal, project, workspace = sys.argv[1:7]
    try:
        roles = json.load(open(roles_file))
    except Exception as exc:
        print("ERR\troles\t%s" % exc)
        return 1
    try:
        plan = json.load(sys.stdin)
    except Exception as exc:
        print("ERR\tplan\t%s" % exc)
        return 1

    known = existing_children(company, parent)
    created = 0
    for task in plan.get("tasks", []):
        role = (task.get("role") or "").strip().lower()
        agent = roles.get(role)
        title = (task.get("title") or "").strip()
        if not agent or not title:
            print("SKIP\t%s\tmissing role or agent" % (role or "?"))
            continue
        if title in known:
            print("CHILD\t%s\t%s\treused" % (known[title], role))
            continue

        desc = ["Arbeitsverzeichnis: %s" % workspace, ""]
        body = (task.get("description") or "").strip()
        if body:
            desc.append(body)
        deps = [d for d in task.get("depends_on") or [] if isinstance(d, str) and d.strip()]
        if deps:
            desc += ["", "Abhaengigkeiten (erst nach diesen Issues abschliessen):"]
            desc += ["- %s" % d.strip() for d in deps]
        desc += [
            "",
            "Regeln: nur Dateien unter %s aendern, keine parallelen Schreibzugriffe" % workspace,
            "auf dieselben Dateien, Aenderungen klein halten und am Ende kurz zusammenfassen,",
            "was fertig ist und was offen bleibt.",
        ]

        args = [
            "issue", "create", "-C", company,
            "--title", title,
            "--description", "\n".join(desc),
            "--assignee-agent-id", agent,
            "--parent-id", parent,
            "--json",
        ]
        if goal:
            args += ["--goal-id", goal]
        if project:
            args += ["--project-id", project]
        p = run(args)
        if p.returncode != 0:
            print("ERR\t%s\t%s" % (role, (p.stderr or p.stdout).strip()[:200]))
            continue
        try:
            data = json.loads(p.stdout)
        except Exception:
            data = {}
        ident = data.get("identifier") or data.get("id") or "?"
        wake_args = [
            "agent", "wake", agent, "-C", company,
            "--source", "assignment", "--trigger", "manual",
            "--reason", "Delegated by Lead", "--json",
        ]
        wake = run(wake_args)
        if wake.returncode != 0:
            wake = run(wake_args, local=True)
        note = "woken"
        if wake.returncode != 0:
            note = "wake-failed:%s" % " ".join((wake.stderr or wake.stdout or "").split())[:120]
        print("CHILD\t%s\t%s\t%s" % (ident, role, note))
        created += 1
    return 0 if created or known else 1


if __name__ == "__main__":
    sys.exit(main())
