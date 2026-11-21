#!/usr/bin/env python3
"""Refuse to work on an issue whose blockers are still open.

Usage: blocked-check.py <issue-id>

Exit 0 when the issue may run (no open blockers), 1 when it is blocked and 2
on a usage error. Paperclip may wake an assignee as soon as an issue is
created, so this is the execution-time dependency gate; `wake-ready.py`
handles the ordering separately.

Fail-closed: if the paperclipai CLI is missing or fails, returns invalid
JSON, or a blocker cannot be read, the issue counts as blocked (exit 1, the
reason goes to stderr). DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN=1 restores the old
"let it run" behaviour for those error cases (open blockers still block).
"""
import json
import os
import shutil
import subprocess
import sys

DONE = {"done", "cancelled", "canceled"}


def cli_path():
    return os.environ.get("PAPERCLIPAI_BIN", "").strip() or shutil.which("paperclipai") or ""


def cli(args):
    path = cli_path()
    if not path:
        raise RuntimeError("paperclipai CLI not found (set PAPERCLIPAI_BIN)")
    try:
        return subprocess.run([path] + args, capture_output=True, text=True, timeout=60)
    except Exception as exc:  # noqa: BLE001
        raise RuntimeError("paperclipai failed: %s" % exc) from exc


def issue_json(issue_id):
    p = cli(["issue", "get", issue_id, "--json"])
    if p.returncode != 0:
        raise RuntimeError("issue get %s failed: %s" % (issue_id, (p.stderr or p.stdout).strip()[:200]))
    try:
        data = json.loads(p.stdout)
    except Exception as exc:
        raise RuntimeError("issue get %s returned invalid JSON: %s" % (issue_id, exc)) from exc
    if not isinstance(data, dict):
        raise RuntimeError("issue get %s returned no object" % issue_id)
    return data


def fail_open():
    return os.environ.get("DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN", "").strip().lower() in ("1", "true", "yes")


def main():
    if len(sys.argv) < 2 or not sys.argv[1].strip():
        print("usage: blocked-check.py <issue-id>", file=sys.stderr)
        return 2
    issue_id = sys.argv[1].strip()
    try:
        issue = issue_json(issue_id)
        blockers = issue.get("blockedByIssueIds") or [b.get("id") for b in issue.get("blockedBy") or [] if b.get("id")]
        open_blockers = []
        for bid in blockers:
            status = issue_json(bid).get("status") or ""
            if status not in DONE:
                open_blockers.append("%s=%s" % (bid, status or "?"))
    except RuntimeError as exc:
        if fail_open():
            print("blocked-check: %s (fail-open by DOCKWEAVE_BLOCKED_CHECK_FAIL_OPEN)" % exc, file=sys.stderr)
            return 0
        print("blocked-check: %s; treating %s as blocked" % (exc, issue_id), file=sys.stderr)
        print("BLOCKED\t%s\tcheck-failed" % issue_id)
        return 1
    if not open_blockers:
        return 0
    # Put the issue back into the queue so the recovery watchdog stays quiet and
    # `wake-ready.py` can wake it again once the blockers are done.
    try:
        cli(["issue", "update", issue_id, "--status", "todo", "--json"])
    except RuntimeError as exc:
        print("blocked-check: could not re-queue %s: %s" % (issue_id, exc), file=sys.stderr)
    print("BLOCKED\t%s\t%s" % (issue_id, ",".join(open_blockers)))
    return 1


if __name__ == "__main__":
    sys.exit(main())
