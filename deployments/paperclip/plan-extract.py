#!/usr/bin/env python3
"""Extract the Lead's JSON work breakdown from an agent answer.

Reads the agent answer on stdin and prints a compact JSON object containing a
"tasks" list on stdout. Prints nothing if no plan can be found.
"""
import json
import re
import sys


def fenced(raw):
    for block in re.findall(r"```(?:json)?\s*(.*?)```", raw, re.S):
        block = block.strip()
        if block.startswith("{") and block.endswith("}"):
            try:
                return json.loads(block)
            except Exception:
                continue
    return None


def bare(raw):
    dec = json.JSONDecoder()
    for i, ch in enumerate(raw):
        if ch != "{":
            continue
        try:
            obj, _ = dec.raw_decode(raw[i:])
        except Exception:
            continue
        if isinstance(obj, dict) and isinstance(obj.get("tasks"), list):
            return obj
    return None


def main():
    raw = sys.stdin.read()
    plan = fenced(raw) or bare(raw)
    if not plan or not isinstance(plan.get("tasks"), list):
        return 0
    plan["tasks"] = [t for t in plan["tasks"] if isinstance(t, dict)]
    if not plan["tasks"]:
        return 0
    print(json.dumps(plan))
    return 0


if __name__ == "__main__":
    sys.exit(main())
