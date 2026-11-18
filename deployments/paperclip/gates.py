#!/usr/bin/env python3
"""Run the deterministic gates of a project workspace.

Usage: gates.py run <workspace-root>

Reads <workspace-root>/.paperclip/gates.json:

  {
    "version": 1,
    "review": {"required": true},
    "human_approval": {"required": false},
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
import subprocess
import sys

TAIL = 40


def tail(text, lines=TAIL):
    rows = [line for line in (text or "").strip().splitlines() if line.strip()]
    return "\n".join(rows[-lines:])


def load_config(root):
    path = os.path.join(root, ".paperclip", "gates.json")
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh), path
    except FileNotFoundError:
        return {}, path
    except Exception as exc:
        return {"error": str(exc)}, path


def run_gates(root):
    cfg, path = load_config(root)
    if cfg.get("error"):
        return {"ok": False, "results": [{"id": "config", "ok": False, "output": cfg["error"]}], "config": path}
    gates = [g for g in cfg.get("gates") or [] if (g.get("when") or "after_run") == "after_run"]
    if not gates:
        return {"ok": True, "skipped": True, "results": [], "config": path}

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
        except Exception as exc:
            results.append({"id": gid, "ok": False, "command": cmd, "output": str(exc)[:400]})
            ok = False
    return {"ok": ok, "skipped": False, "results": results, "config": path}


def main():
    if len(sys.argv) < 3 or sys.argv[1] != "run":
        print("usage: gates.py run <workspace-root>", file=sys.stderr)
        return 2
    result = run_gates(os.path.abspath(sys.argv[2]))
    print(json.dumps(result))
    return 0 if result.get("ok") else 1


if __name__ == "__main__":
    sys.exit(main())
