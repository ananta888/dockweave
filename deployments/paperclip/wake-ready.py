#!/usr/bin/env python3
"""Wake only the issues whose blockers are done.

Usage: wake-ready.py COMPANY [PARENT_ISSUE_ID]

Without a parent id every open issue of the company is considered. Issues that
are still `todo` and have no open blocker are woken through their assignee.
Prints one READY line per wakeup.
"""
import json
import os
import subprocess
import sys

CLI = os.environ.get("PAPERCLIPAI_BIN") or "/home/krusty/.local/bin/paperclipai"
DONE = {"done", "cancelled", "canceled"}


def run(args, local=False):
    env = dict(os.environ)
    if local:
        for key in ("PAPERCLIP_API_KEY", "PAPERCLIP_AGENT_ID", "PAPERCLIP_RUN_ID"):
            env.pop(key, None)
    return subprocess.run([CLI] + args, capture_output=True, text=True, env=env)


def list_issues(company):
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
    if len(sys.argv) < 2:
        print("usage: wake-ready.py COMPANY [PARENT]", file=sys.stderr)
        return 2
    company = sys.argv[1]
    parent = sys.argv[2] if len(sys.argv) > 2 else ""
    issues = list_issues(company)
    if not issues:
        return 0
    status = {i.get("id"): (i.get("status") or "") for i in issues}
    by_id = {i.get("id"): i for i in issues}

    woke = 0
    for issue in issues:
        if parent and issue.get("parentId") != parent:
            continue
        if (issue.get("status") or "") != "todo":
            continue
        blockers = issue.get("blockedByIssueIds") or []
        if not blockers:
            # The list projection may omit blockers: ask for the full issue.
            g = run(["issue", "get", issue.get("id"), "--json"])
            if g.returncode == 0:
                try:
                    detail = json.loads(g.stdout)
                except Exception:
                    detail = {}
                ids = detail.get("blockedByIssueIds") or []
                if not ids:
                    ids = [b.get("id") for b in detail.get("blockedBy") or [] if b.get("id")]
                blockers = ids
        if any(status.get(b) not in DONE for b in blockers):
            continue
        agent = issue.get("assigneeAgentId")
        if not agent:
            continue
        w = run(["agent", "wake", agent, "-C", company,
                 "--source", "assignment", "--trigger", "manual",
                 "--reason", "Unblocked: %s" % (issue.get("identifier") or issue.get("id")),
                 "--json"])
        if w.returncode != 0:
            w = run(["agent", "wake", agent, "-C", company,
                     "--source", "assignment", "--trigger", "manual",
                     "--reason", "Unblocked: %s" % (issue.get("identifier") or issue.get("id")),
                     "--json"], local=True)
        if w.returncode == 0:
            names = {i.get("id"): i.get("name") for i in issues}
            print("READY\t%s\t%s" % (issue.get("identifier") or issue.get("id"), names.get(agent) or agent))
            woke += 1
        else:
            print("ERR\t%s\t%s" % (issue.get("identifier") or issue.get("id"),
                                   (w.stderr or w.stdout).strip()[:160]))
    return 0 if woke or True else 1


if __name__ == "__main__":
    sys.exit(main())
