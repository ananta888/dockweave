#!/usr/bin/env python3
"""Close a Lead issue only when the gates agree.

Usage: parent-close.py <issue-id> [workspace-root]

Checks, in order:
  1. every child issue is done/cancelled
  2. review: the child matching review.issue_match is done and carries a
     "VERDICT: PASS" comment from the assigned reviewer agent or a human board
     user (skipped when review.required is false). A self-authored or
     unattributed PASS no longer counts unless
     DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW=1 is set.
  3. human approval: when human_approval.required, an accepted human_only
     interaction exists on the parent AND a DockWeave-side approval
     (`dockweave gate approve <issue>`) is recorded. Otherwise the interaction
     is created and the parent stays in_review.

Prints PARENT/WAITING/ERR lines. Never fails the agent run.
"""
import json
import os
import shutil
import subprocess
import sys

CLI = os.environ.get("PAPERCLIPAI_BIN") or shutil.which("paperclipai") or "paperclipai"
HELPER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "paperclip-gate.py")
FINISHED = {"done", "cancelled", "canceled"}


def dockweave_cli():
    candidate = os.environ.get("DOCKWEAVE_CLI", "")
    if candidate:
        return candidate
    found = shutil.which("dockweave")
    if found:
        return found
    home = os.path.expanduser("~")
    for path in (os.path.join(home, ".local", "bin", "dockweave"), "/usr/local/bin/dockweave"):
        if os.path.exists(path):
            return path
    return ""


def dockweave_review(issue_id, verdict, note=""):
    cli = dockweave_cli()
    if not cli:
        return False, "dockweave CLI not found"
    cmd = [cli, "gate", "review", issue_id, "--verdict", verdict, "--json"]
    if note:
        cmd += ["--note", note]
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    except Exception as exc:  # noqa: BLE001
        return False, str(exc)[:200]
    if proc.returncode != 0:
        return False, (proc.stderr or proc.stdout).strip()[:200]
    return True, ""


def dockweave_approval(issue_id):
    cli = dockweave_cli()
    if not cli:
        return None
    try:
        proc = subprocess.run([cli, "gate", "approval", issue_id, "--json"],
                              capture_output=True, text=True, timeout=30)
    except Exception:
        return None
    if proc.returncode != 0:
        return None
    try:
        return json.loads(proc.stdout)
    except Exception:
        return None


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


def comment_items(ident):
    p = run(["issue", "comments", ident, "--json"])
    if p.returncode != 0:
        return []
    try:
        data = json.loads(p.stdout)
    except Exception:
        return []
    return data if isinstance(data, list) else data.get("comments") or data.get("data") or []


def review_verdict(review_issue):
    """Return (passed, detail) for an attributed VERDICT: PASS comment."""
    ident = review_issue.get("identifier") or review_issue.get("id")
    assignee = review_issue.get("assigneeAgentId")
    allow_unattributed = os.environ.get("DOCKWEAVE_ALLOW_UNATTRIBUTED_REVIEW", "") in ("1", "true", "yes")
    seen_unattributed = False
    for comment in comment_items(ident):
        body = str(comment.get("body") or "").upper()
        if "VERDICT: PASS" not in body:
            continue
        author_agent = comment.get("authorAgentId")
        author_user = comment.get("authorUserId")
        if author_agent and assignee and author_agent == assignee:
            return True, "reviewer-agent"
        if author_user and author_user not in ("local-board", "system", ""):
            return True, "human:" + str(author_user)
        if allow_unattributed:
            return True, "unattributed (override)"
        seen_unattributed = True
    if seen_unattributed:
        return False, "PASS is not attributed to the assigned reviewer"
    return False, "no PASS verdict"


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
        for review in review_issues:
            passed, detail = review_verdict(review)
            if not passed:
                print("WAITING\treview\t%s (%s)" % (detail, review.get("identifier") or review.get("id")))
                return 0
            ident = review.get("identifier") or review.get("id")
            ok, why = dockweave_review(ident, "PASS")
            if not ok:
                print("WAITING\treview\tDockWeave review sync failed: %s" % why)
                return 0

    approval = (cfg.get("human_approval") or {}).get("required", False)
    if approval:
        state = gate("status", issue_id)
        items = state.get("interactions") or []
        accepted = [i for i in items if (i.get("state") or "") in ("accepted", "approved", "resolved")]
        dockweave_ok = dockweave_approval(issue_id) is not None
        if not accepted or not dockweave_ok:
            if not accepted:
                pending = [i for i in items if (i.get("state") or "") in ("pending", "open")]
                if not pending:
                    gate("human-request", issue_id, "Goal abschliessen?")
            missing = []
            if not accepted:
                missing.append("Paperclip-Interaction")
            if not dockweave_ok:
                missing.append("DockWeave-Freigabe (`dockweave gate approve %s`)" % issue_id)
            run(["issue", "update", issue_id, "--status", "in_review",
                 "--comment", "Alle Kinder gruen. Warte auf: " + ", ".join(missing) + ".",
                 "--json"])
            print("WAITING\thuman\tapproval missing: %s" % ", ".join(missing))
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
