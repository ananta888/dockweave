#!/usr/bin/env python3
"""Create the Lead's child issues in Paperclip, wire dependencies, wake what is ready.

Usage:
  plan-extract.py | plan-delegate.py ROLES_FILE COMPANY PARENT_ISSUE GOAL PROJECT WORKSPACE

Every issue gets the parent/goal/project of the Lead issue, the role agent as
assignee and a machine readable dependency marker:

  DEPENDS_ON: DOC-23,DOC-24

The real dependency edges are written to Paperclip as blockedByIssueIds. Only
issues without open blockers are woken, so the team works in order instead of
starting nine tasks at once.
"""
import json
import os
import subprocess
import sys
import urllib.request

CLI = os.environ.get("PAPERCLIPAI_BIN") or "/home/krusty/.local/bin/paperclipai"
API = (os.environ.get("PAPERCLIP_API_URL") or "http://127.0.0.1:3100").rstrip("/")
KEY = os.environ.get("PAPERCLIP_API_KEY") or ""


def api(method, path, payload):
    """Agent-authenticated API call (the CLI has no flag for blockers)."""
    req = urllib.request.Request(
        API + path,
        data=json.dumps(payload).encode(),
        method=method,
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + KEY},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        raw = resp.read()
    return json.loads(raw) if raw else {}


def run(args, local=False):
    env = dict(os.environ)
    if local:
        for key in ("PAPERCLIP_API_KEY", "PAPERCLIP_AGENT_ID", "PAPERCLIP_RUN_ID"):
            env.pop(key, None)
    return subprocess.run([CLI] + args, capture_output=True, text=True, env=env)


def load_issues(company):
    p = run(["issue", "list", "-C", company, "--json"])
    if p.returncode != 0:
        return []
    try:
        items = json.loads(p.stdout)
    except Exception:
        return []
    if isinstance(items, dict):
        items = items.get("issues") or items.get("data") or []
    return items


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

    issues = load_issues(company)
    by_title = {(i.get("title") or "").strip(): i for i in issues if i.get("parentId") == parent}

    created = {}
    for task in plan.get("tasks", []):
        role = (task.get("role") or "").strip().lower()
        agent = roles.get(role)
        title = (task.get("title") or "").strip()
        if not agent or not title:
            print("SKIP\t%s\tmissing role or agent" % (role or "?"))
            continue
        existing = by_title.get(title)
        if existing:
            created[title] = existing.get("id")
            print("CHILD\t%s\t%s\treused" % (existing.get("identifier") or existing.get("id"), role))
            continue

        desc = ["Arbeitsverzeichnis: %s" % workspace, ""]
        body = (task.get("description") or "").strip()
        if body:
            desc.append(body)
        deps = [d.strip() for d in task.get("depends_on") or [] if isinstance(d, str) and d.strip()]
        if deps:
            desc += ["", "Abhaengigkeiten (erst nach diesen Issues abschliessen):"]
            desc += ["- %s" % d for d in deps]
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
        created[title] = data.get("id")
        print("CHILD\t%s\t%s\tcreated" % (ident, role))

    # Second pass: real blockers + machine readable marker.
    for task in plan.get("tasks", []):
        title = (task.get("title") or "").strip()
        issue_id = created.get(title)
        if not issue_id:
            continue
        deps = [d.strip() for d in task.get("depends_on") or [] if isinstance(d, str) and d.strip()]
        dep_ids = [created[d] for d in deps if created.get(d)]
        if deps:
            marker = "DEPENDS_ON: " + ",".join(
                (by_title.get(d, {}).get("identifier") or created[d]) for d in deps if created.get(d)
            )
            run(["issue", "comment", issue_id, "--body",
                 marker + "\n(automatisch gesetzt aus dem Lead-Plan)"])
        if dep_ids and KEY:
            try:
                api("PATCH", "/api/issues/" + issue_id, {"blockedByIssueIds": dep_ids})
            except Exception as exc:
                print("ERR\tdeps\t%s\t%s" % (title[:40], str(exc)[:160]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
