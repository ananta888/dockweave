#!/usr/bin/env python3
"""Close a Lead issue once all of its child issues are finished.

Usage: parent-close.py <issue-id>

Run after a worker updated its issue. If the issue has a parent and every
sibling is done/cancelled, the parent is marked done with a short summary
comment. Best effort: any error exits 0 so it never fails an agent run.
"""
import json
import os
import subprocess
import sys

CLI = os.environ.get("PAPERCLIPAI_BIN") or "/home/krusty/.local/bin/paperclipai"
FINISHED = {"done", "cancelled", "canceled"}


def run(args):
    return subprocess.run([CLI] + args, capture_output=True, text=True)


def main():
    if len(sys.argv) < 2:
        return 0
    issue_id = sys.argv[1]
    p = run(["issue", "get", issue_id, "--json"])
    if p.returncode != 0:
        return 0
    try:
        issue = json.loads(p.stdout)
    except Exception:
        return 0
    parent = issue.get("parentId")
    company = issue.get("companyId")
    if not parent or not company:
        return 0

    p = run(["issue", "list", "-C", company, "--json"])
    if p.returncode != 0:
        return 0
    try:
        issues = json.loads(p.stdout)
    except Exception:
        return 0
    if isinstance(issues, dict):
        issues = issues.get("issues") or issues.get("data") or []

    kids = [i for i in issues if i.get("parentId") == parent]
    if not kids or any((i.get("status") or "") not in FINISHED for i in kids):
        return 0

    lines = ["Alle Child-Issues sind abgeschlossen:"]
    for k in sorted(kids, key=lambda x: x.get("identifier") or ""):
        lines.append("- %s %s (%s)" % (k.get("identifier"), k.get("title") or "", k.get("status")))
    summary = "\n".join(lines)
    run(["issue", "update", parent, "--status", "done", "--comment", summary, "--json"])
    print("PARENT\t%s\tdone" % parent)
    return 0


if __name__ == "__main__":
    sys.exit(main())
