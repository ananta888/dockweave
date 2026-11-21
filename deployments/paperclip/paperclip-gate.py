#!/usr/bin/env python3
"""Structured Paperclip gates (issue-thread interactions).

Usage:
  paperclip-gate.py review-request <issue-id> <addressee-agent-id> <title>
  paperclip-gate.py human-request  <issue-id> <title>
  paperclip-gate.py status         <issue-id>

`review-request` creates a confirmation card that only the addressed agent (or a
board user) may resolve. `human-request` creates one that no agent can resolve.

Prints one JSON object on stdout.
"""
import json
import os
import shutil
import re
import subprocess
import sys
import urllib.error
import urllib.request

API = (os.environ.get("PAPERCLIP_API_URL") or "http://127.0.0.1:3100").rstrip("/")
KEY = os.environ.get("PAPERCLIP_API_KEY") or ""
CLI = os.environ.get("PAPERCLIPAI_BIN") or shutil.which("paperclipai") or "paperclipai"


def cli(args):
    return subprocess.run([CLI] + args, capture_output=True, text=True)


def issue_json(issue_id):
    p = cli(["issue", "get", issue_id, "--json"])
    if p.returncode != 0:
        return {}
    try:
        return json.loads(p.stdout)
    except Exception:
        return {}


def rework(issue_id, reason):
    """Create a rework issue for the author of the first dependency."""
    review = issue_json(issue_id)
    parent = review.get("parentId")
    company = review.get("companyId")
    deps = []
    m = re.search(r"DEPENDS_ON:\s*([A-Za-z0-9_,-]+)", review.get("description") or "")
    if m:
        deps = [d for d in m.group(1).split(",") if d]
    assignee = ""
    for dep in deps:
        src = issue_json(dep)
        if src.get("assigneeAgentId"):
            assignee = src["assigneeAgentId"]
            break
    if not (company and assignee):
        return {"ok": False, "error": "no parent/assignee for rework"}
    args = ["issue", "create", "-C", company,
            "--title", "Rework: " + (review.get("title") or issue_id),
            "--description", "Review-Ergebnis:\n\n" + reason.strip() +
            "\n\nBitte nachbessern und erneut STATUS: DONE melden.",
            "--assignee-agent-id", assignee, "--json"]
    if parent:
        args += ["--parent-id", parent]
    if review.get("goalId"):
        args += ["--goal-id", review["goalId"]]
    if review.get("projectId"):
        args += ["--project-id", review["projectId"]]
    p = cli(args)
    if p.returncode != 0:
        return {"ok": False, "error": (p.stderr or p.stdout).strip()[:200]}
    try:
        data = json.loads(p.stdout)
    except Exception:
        data = {}
    cli(["agent", "wake", assignee, "-C", company, "--source", "assignment",
         "--trigger", "manual", "--reason", "Rework nach FAIL", "--json"])
    return {"ok": True, "issue": data.get("identifier") or data.get("id"), "assignee": assignee}


def call(method, path, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"Content-Type": "application/json"}
    if KEY:
        headers["Authorization"] = "Bearer " + KEY
    req = urllib.request.Request(API + path, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
        return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as exc:
        body = exc.read().decode(errors="replace")[:300]
        raise RuntimeError("HTTP %s: %s" % (exc.code, body))


def create(issue_id, title, policy, addressee=""):
    payload = {
        "kind": "request_confirmation",
        "resolverPolicy": policy,
        "title": title,
        "summary": "Freigabe fuer DockWeave-Gate",
        "continuationPolicy": "wake_assignee",
        "idempotencyKey": "dwgate:%s:%s" % (issue_id, policy),
        "payload": {
            "version": 1,
            "prompt": title,
            "acceptLabel": "PASS",
            "rejectLabel": "FAIL",
            "rejectRequiresReason": True,
            "rejectReasonLabel": "Begruendung",
        },
    }
    if addressee:
        payload["addresseeAgentId"] = addressee
    return call("POST", "/api/issues/%s/interactions" % issue_id, payload)


def list_pending(issue_id):
    data = call("GET", "/api/issues/%s/interactions" % issue_id)
    items = data if isinstance(data, list) else data.get("interactions") or data.get("data") or []
    return items


def main():
    if len(sys.argv) < 3:
        print(json.dumps({"ok": False, "error": "usage or missing API key"}))
        return 2
    action = sys.argv[1]
    issue_id = sys.argv[2]
    try:
        if action == "review-request":
            addressee, title = sys.argv[3], " ".join(sys.argv[4:]) or "Review freigeben"
            print(json.dumps({"ok": True, "interaction": create(issue_id, title, "not_creator", addressee)}))
        elif action == "human-request":
            title = " ".join(sys.argv[3:]) or "Goal freigeben"
            print(json.dumps({"ok": True, "interaction": create(issue_id, title, "human_only")}))
        elif action == "rework":
            print(json.dumps(rework(issue_id, " ".join(sys.argv[3:]))))
        elif action == "status":
            items = list_pending(issue_id)
            out = [{"id": i.get("id"), "state": i.get("state") or i.get("status"),
                    "policy": i.get("effectiveResolverPolicy") or i.get("resolverPolicy")} for i in items]
            print(json.dumps({"ok": True, "interactions": out}))
        else:
            print(json.dumps({"ok": False, "error": "unknown action"}))
            return 2
    except Exception as exc:
        print(json.dumps({"ok": False, "error": str(exc)[:400]}))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
