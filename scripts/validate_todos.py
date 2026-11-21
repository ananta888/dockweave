#!/usr/bin/env python3
# /// script
# requires-python = ">=3.12"
# dependencies = ["jsonschema==4.26.0"]
# ///
"""Run: uv run scripts/validate_todos.py [--archives] [track.json ...]."""
import argparse
import json
from collections import Counter
from pathlib import Path

from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parents[1]


class TrackError(ValueError):
    """A track has inconsistent task references or aggregate state."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise TrackError(message)


def validate_track(data: dict) -> None:
    schema = json.loads((ROOT / "todo/todo.track.schema.json").read_text())
    Draft202012Validator(schema).validate(data)
    tasks = data["tasks"]
    ids = {task["id"] for task in tasks}
    require(len(ids) == len(tasks), "duplicate task id")
    by_id = {task["id"]: task for task in tasks}
    visited: set[str] = set()
    visiting: set[str] = set()

    def visit(task_id: str) -> None:
        require(task_id in ids, f"missing dependency: {task_id}")
        require(task_id not in visiting, f"dependency cycle: {task_id}")
        if task_id in visited:
            return
        visiting.add(task_id)
        for dependency in by_id[task_id].get("depends_on", []):
            visit(dependency)
        visiting.remove(task_id)
        visited.add(task_id)

    for task in tasks:
        for field in ("status", "priority", "risk"):
            require(task[field] in data[field + "_scale"], f"{task['id']}: invalid {field}")
        progress = task.get("progress_percent", 0)
        require((task["status"] == "done") == (progress == 100), f"{task['id']}: status/progress mismatch")
        visit(task["id"])
    milestones = data["milestones"]
    require(len({m["id"] for m in milestones}) == len(milestones), "duplicate milestone id")
    memberships = Counter(t for m in milestones for t in m["task_ids"])
    require(memberships == Counter(ids), "each task must belong to exactly one milestone")
    for milestone in milestones:
        for task_id in milestone["task_ids"]:
            require(by_id[task_id].get("milestone", milestone["id"]) == milestone["id"], "wrong milestone membership")
    summary = data["tasks_status_summary"]
    require(summary["total"] == len(tasks), "wrong total")
    for field in ("status", "priority", "risk"):
        counts = Counter(task[field] for task in tasks)
        require(Counter(summary["by_" + field]) == counts, f"wrong {field} summary")
    done = sum(task["status"] == "done" for task in tasks)
    percentage = 100 * done / len(tasks) if tasks else 0
    require(abs(summary["progress_percent_done"] - percentage) <= 0.01, "wrong done percentage")
    critical = data.get("critical_path_tasks", [])
    require(set(critical) <= ids and len(critical) == len(set(critical)), "invalid critical path")
    critical_done = sum(by_id[t]["status"] == "done" for t in critical)
    require(summary["critical_path"] == {"total": len(critical), "done": critical_done,
                                         "remaining": len(critical) - critical_done}, "wrong critical summary")
    milestone_counts = Counter(m["status"] for m in milestones)
    require(summary["milestones"]["total"] == len(milestones), "wrong milestone total")
    require(Counter({k: v for k, v in summary["milestones"].items() if k != "total"}) == milestone_counts,
            "wrong milestone status summary")
    types = data.get("tasks_type_summary")
    if types:
        require(types["total"] == len(tasks), "wrong type total")
        for kind, count in Counter(t["type"] for t in tasks).items():
            actual = types["by_type"].get(kind)
            require((actual.get("total") if isinstance(actual, dict) else actual) == count, "wrong type summary")
        require(set(types["by_type"]) == {t["type"] for t in tasks}, "unexpected type summary")
    progress_summary = data.get("progress_summary")
    if progress_summary:
        for status in ("todo", "in_progress", "partial", "blocked", "done"):
            key = "todo_remaining" if status == "todo" else status
            require(progress_summary[key] == sum(t["status"] == status for t in tasks), "wrong progress summary")
    order = data.get("recommended_execution_order")
    if order is not None:
        require(set(order) == ids and len(order) == len(ids), "incomplete execution order")
        positions = {task_id: i for i, task_id in enumerate(order)}
        for task in tasks:
            require(all(positions[d] < positions[task["id"]] for d in task.get("depends_on", [])),
                    "execution order violates dependencies")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", type=Path, nargs="*")
    parser.add_argument("--archives", action="store_true", help="also schema-check historic files in their original format")
    args = parser.parse_args()
    paths = args.paths or sorted(p for p in (ROOT / "todo").glob("todo.*.json") if not p.name.endswith(".schema.json"))
    for path in paths:
        validate_track(json.loads(path.read_text()))
        print(f"PASS {path.relative_to(ROOT) if path.is_relative_to(ROOT) else path}")
    if args.archives:
        for path in sorted((ROOT / "todo/archive").glob("*.json")):
            data = json.loads(path.read_text())
            name = "todo.track.schema.json" if "track" in data else (
                "todo.legacy.schema.json" if "domain" in data else "todo.schema.json")
            Draft202012Validator(json.loads((ROOT / "todo" / name).read_text())).validate(data)
            print(f"PASS schema {path.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
