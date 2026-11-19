#!/usr/bin/env python3
"""Run the deterministic gates of a project workspace.

Usage: gates.py run <workspace-root>

Preferred path: forward the run to the DockWeave daemon (`dockweave gate run`),
which executes the gates in a short-lived sandbox container (no network, no
broker/docker socket, read-only rootfs, workspace copy). This removes the old
behaviour of executing agent-controlled commands as the host user.

The host fallback (`subprocess.run(shell=True)` in the workspace) only runs when
DOCKWEAVE_GATE_HOST_FALLBACK=1 is set explicitly.

Reads <workspace-root>/.paperclip/gates.json:

  {
    "version": 1,
    "review": {"required": true},
    "human_approval": {"required": false},
    "runner_image": "node:22-bookworm",
    "gates": [
      {"id": "build", "when": "after_run", "command": "npm run build",
       "cwd": ".", "timeout_sec": 300}
    ]
  }

Prints one JSON object: {"ok": bool, "skipped": bool, "results": [...]}.
Exit code 0 when every gate passed (or none is configured), 1 otherwise.
"""
import json
import os
import shutil
import subprocess
import sys

TAIL = 40


def tail(text, lines=TAIL):
    rows = [line for line in (text or "").strip().splitlines() if line.strip()]
    return "\n".join(rows[-lines:])


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


def run_sandbox(root, timeout_sec):
    cli = dockweave_cli()
    if not cli:
        return None
    try:
        proc = subprocess.run(
            [cli, "gate", "run", root, "--json", "--timeout", str(timeout_sec)],
            capture_output=True, text=True, timeout=timeout_sec + 120,
        )
    except Exception as exc:  # noqa: BLE001 - report, never execute on the host
        return {"ok": False, "skipped": False,
                "results": [{"id": "gate-runner", "ok": False, "output": str(exc)[:2000]}]}
    if proc.returncode != 0:
        return {"ok": False, "skipped": False,
                "results": [{"id": "gate-runner", "ok": False,
                             "output": tail(proc.stderr or proc.stdout, 20)}]}
    try:
        run = json.loads(proc.stdout)
    except Exception:
        return {"ok": False, "skipped": False,
                "results": [{"id": "gate-runner", "ok": False,
                             "output": "gate runner returned non-JSON output"}]}

    results = []
    for item in run.get("results") or []:
        status = item.get("status") or ""
        results.append({
            "id": item.get("id") or "gate",
            "ok": status == "passed",
            "exit": item.get("exit", -1),
            "command": item.get("command") or "",
            "output": item.get("output") or item.get("reason") or "",
        })
    status = run.get("status") or "error"
    ok = status in ("passed", "skipped") and not run.get("tamper")
    out = {
        "ok": ok,
        "skipped": status == "skipped",
        "results": results,
        "source": "dockweave-gate-runner",
        "status": status,
    }
    for key in ("id", "reason", "warnings", "diff", "image", "image_digest", "tamper", "duration_ms"):
        if run.get(key):
            out[key] = run[key]
    if not results and run.get("reason"):
        out["results"] = [{"id": "gate-runner", "ok": False, "output": run["reason"]}]
    return out


def load_config(root):
    path = os.path.join(root, ".paperclip", "gates.json")
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh), path
    except FileNotFoundError:
        return {}, path
    except Exception as exc:
        return {"error": str(exc)}, path


def run_host_fallback(root):
    """Legacy host execution. Only used with DOCKWEAVE_GATE_HOST_FALLBACK=1."""
    cfg, path = load_config(root)
    if cfg.get("error"):
        return {"ok": False, "results": [{"id": "config", "ok": False, "output": cfg["error"]}], "config": path}
    gates = [g for g in cfg.get("gates") or [] if (g.get("when") or "after_run") == "after_run"]
    if not gates:
        return {"ok": True, "skipped": True, "results": [], "config": path, "source": "host-fallback"}

    results = []
    ok = True
    for gate in gates:
        gid = gate.get("id") or "gate"
        cmd = (gate.get("command") or "").strip()
        cwd = os.path.join(root, gate.get("cwd") or ".")
        timeout = int(gate.get("timeout_sec") or 300)
        if not cmd:
            results.append({"id": gid, "ok": False, "output": "no command configured"})
            ok = False
            continue
        try:
            proc = subprocess.run(cmd, shell=True, cwd=cwd, capture_output=True,
                                  text=True, timeout=timeout)
            out = (proc.stdout or "") + (proc.stderr or "")
            passed = proc.returncode == 0
            results.append({
                "id": gid,
                "ok": passed,
                "exit": proc.returncode,
                "command": cmd,
                "output": tail(out),
            })
            ok = ok and passed
        except subprocess.TimeoutExpired:
            results.append({"id": gid, "ok": False, "timeout": timeout,
                            "command": cmd, "output": "timeout after %ss" % timeout})
            ok = False
    return {"ok": ok, "skipped": False, "results": results, "config": path, "source": "host-fallback"}


def main():
    if len(sys.argv) < 3 or sys.argv[1] != "run":
        print("usage: gates.py run <workspace-root>", file=sys.stderr)
        return 2
    root = os.path.abspath(sys.argv[2])
    timeout = int(os.environ.get("DOCKWEAVE_GATE_TIMEOUT", "900"))

    if os.environ.get("DOCKWEAVE_GATE_HOST_FALLBACK", "") in ("1", "true", "yes"):
        result = run_host_fallback(root)
    else:
        result = run_sandbox(root, timeout)
        if result is None:
            result = {
                "ok": False, "skipped": False, "source": "dockweave-gate-runner",
                "results": [{
                    "id": "gate-runner", "ok": False,
                    "output": "dockweave CLI not found; refusing host execution "
                              "(set DOCKWEAVE_GATE_HOST_FALLBACK=1 to opt in)",
                }],
            }

    print(json.dumps(result))
    return 0 if result.get("ok") else 1


if __name__ == "__main__":
    sys.exit(main())
