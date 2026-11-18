#!/usr/bin/env python3
"""Refuse to work on an issue whose blockers are still open.

Usage: blocked-check.py <issue-id>

Exit 0 when the issue may run (or has no blockers), 1 when it is still blocked.
Paperclip may wake an assignee as soon as an issue is created, so this is the
execution-time dependency gate; `wake-ready.py` handles the ordering separately.
"""
import json
import subprocess
import sys

CLI = "/home/krusty/.local/bin/paperclipai"
DONE = {"done", "cancelled", "canceled"}


def cli(args):
    return subprocess.run([CLI] + args, capture_output=True, text=True)


def main():
    if len(sys.argv) < 2:
        return 0
    issue_id = sys.argv[1]
    p = cli(["issue", "get", issue_id, "--json"])
    if p.returncode != 0:
        return 0
    try:
        issue = json.loads(p.stdout)
    except Exception:
        return 0
    blockers = issue.get("blockedByIssueIds") or [b.get("id") for b in issue.get("blockedBy") or []]
    if not blockers:
        return 0
    open_blockers = []
    for bid in blockers:
        b = cli(["issue", "get", bid, "--json"])
        try:
            status = json.loads(b.stdout).get("status") or ""
        except Exception:
            continue
        if status not in DONE:
            open_blockers.append(status)
    if not open_blockers:
        return 0
    # Put the issue back into the queue so the recovery watchdog stays quiet and
    # `wake-ready.py` can wake it again once the blockers are done.
    cli(["issue", "update", issue_id, "--status", "todo", "--json"])
    print("BLOCKED\t%s" % issue_id)
    return 1


if __name__ == "__main__":
    sys.exit(main())
