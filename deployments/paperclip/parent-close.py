#!/usr/bin/env python3
"""Close a Lead issue only when the gates agree.

Usage: parent-close.py <issue-id> [workspace-root]

Checks, in order:
  1. every child issue is done/cancelled
  2. review: the child matching review.issue_match is done and its last comment
     contains "VERDICT: PASS" (skipped when review.required is false)
  3. human approval: when human_approval.required, an accepted human_only
     interaction exists on the parent - otherwise it is created and the parent
     stays in_review

Prints PARENT/WAITING/ERR lines. Never fails the agent run.
"""
import json
import os
import subprocess
import sys

CLI = os.environ.get("PAPERCLIPAI_BIN") or "/home/krusty/.local/bin/paperclipai"
HELPER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "paperclip-gate.py")
FINISHED = {"done", "cancelled", "canceled"}


def run(args):
    return subprocess.run([CLI] + args, capture_output=True, text=True)


def load_config(root):
    if not root:
        return {}
    for rel in (".paperclip/gates.json", ".paperclip/gate.json"):
        path = os.path.join(root, rel)
        try:
            with open(path, encoding="utf-8") as fh:
                return json.load(fh)
        except FileNotFoundError:
            continue
        except Exception:
            return {}
    return {}


def comments(ident):
    p = run(["issue", "comments", ident, "--json"])
    if p.returncode != 0:
        return ""
    try:
        data = json.loads(p.stdout)
    except Exception:
        return ""
    return "\n".join(str(c.get("body") or "") for c in data)


def gate(action, *args):
    if not os.path.exists(HELPER):
        return {}
    p = subprocess.run([sys.executable, HELPER, action] + list(args),
                       capture_output=True, text=True)
    try:
        return json.loads(p.stdout or "{}")
    except Exception:
        return {}


def main():
    if len(sys.argv) < 2:
        return 0
    issue_id = sys.argv[1]
    root = sys.argv[2] if len(sys.argv) > 2 else ""
    cfg = load_config(root)

    p = run(["issue", "get", issue_id, "--json"])
    if p.returncode != 0:
        return 0
    try:
        issue = json.loads(p.stdout)
    except Exception:
        return 0
    company = issue.get("companyId")
    if not company:
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

    kids = [i for i in issues if i.get("parentId") == issue_id]
    if not kids or any((i.get("status") or "") not in FINISHED for i in kids):
        return 0

    review_cfg = cfg.get("review") or {}
    if review_cfg.get("required", True):
        needle = (review_cfg.get("issue_match") or "review").lower()
        review_issues = [k for k in kids if needle in (k.get("title") or "").lower()]
        if not review_issues:
            print("WAITING\treview\tno review issue")
            return 0
        if any((k.get("status") or "") not in FINISHED for k in review_issues):
            print("WAITING\treview\topen")
            return 0
        verdicts = [comments(k.get("identifier") or k.get("id")) for k in review_issues]
        if not any("VERDICT: PASS" in v.upper() for v in verdicts):
            print("WAITING\treview\tno PASS verdict")
            return 0

    approval = (cfg.get("human_approval") or {}).get("required", False)
    if approval:
        state = gate("status", issue_id)
        items = state.get("interactions") or []
        accepted = [i for i in items if (i.get("state") or "") in ("accepted", "approved", "resolved")]
        if not accepted:
            pending = [i for i in items if (i.get("state") or "") in ("pending", "open")]
            if not pending:
                gate("human-request", issue_id, "Goal abschliessen?")
            run(["issue", "update", issue_id, "--status", "in_review",
                 "--comment", "Alle Kinder gruen. Warte auf menschliche Freigabe (Interaction im Inbox).",
                 "--json"])
            print("WAITING\thuman\tapproval requested")
            return 0

    lines = ["Alle Child-Issues sind abgeschlossen:"]
    for k in sorted(kids, key=lambda x: x.get("identifier") or ""):
        lines.append("- %s %s (%s)" % (k.get("identifier"), k.get("title") or "", k.get("status")))
    if review_cfg.get("required", True):
        lines.append("\nReview: VERDICT PASS")
    if approval:
        lines.append("Menschliche Freigabe: erteilt")
    run(["issue", "update", issue_id, "--status", "done",
         "--comment", "\n".join(lines), "--json"])
    print("PARENT\t%s\tdone" % issue_id)
    return 0


if __name__ == "__main__":
    sys.exit(main())
