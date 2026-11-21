#!/usr/bin/env python3
# /// script
# requires-python = ">=3.12"
# dependencies = ["jsonschema==4.26.0", "pyyaml==6.0.3"]
# ///
"""Validate real daemon responses against docs/api/openapi.yaml (RF-022).

Run: uv run scripts/validate_openapi.py   (or python3 with jsonschema+pyyaml)

Records the responses with the Go test TestRecordPipelineResponsesForOpenAPI
(real handlers and store) and checks each one against the schema documented
for its path, method and status. An undocumented JSON response fails.
"""
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml
from jsonschema import Draft202012Validator, FormatChecker

ROOT = Path(__file__).resolve().parents[1]


def response_schema(spec, path, method, status):
    item = spec["paths"].get(path)
    if item is None:
        raise LookupError(f"path {path} is not documented")
    op = item.get(method.lower())
    if op is None:
        raise LookupError(f"{method} {path} is not documented")
    resp = op.get("responses", {}).get(str(status))
    if resp is None:
        raise LookupError(f"{method} {path} {status} is not documented")
    if "$ref" in resp:
        resp = spec["components"]["responses"][resp["$ref"].rsplit("/", 1)[-1]]
    schema = resp.get("content", {}).get("application/json", {}).get("schema")
    if schema is None:
        raise LookupError(f"{method} {path} {status} has no application/json schema")
    # Resolve #/components/... refs against the spec by embedding it.
    return dict(schema, components=spec["components"])


def validate(spec, fixtures):
    errors = []
    for fx in fixtures:
        label = f"{fx['method']} {fx['path']} {fx['status']}"
        try:
            schema = response_schema(spec, fx["path"], fx["method"], fx["status"])
        except LookupError as exc:
            errors.append(str(exc))
            continue
        validator = Draft202012Validator(schema, format_checker=FormatChecker())
        for err in validator.iter_errors(fx["body"]):
            errors.append(f"{label}: {'/'.join(map(str, err.absolute_path)) or '<root>'}: {err.message}")
    return errors


def record(directory):
    env = dict(os.environ, DOCKWEAVE_OPENAPI_FIXTURES=directory)
    subprocess.run(["go", "test", "-count=1", "-run", "TestRecordPipelineResponsesForOpenAPI", "./internal/api"],
                   cwd=ROOT, env=env, check=True)
    return json.loads((Path(directory) / "pipeline-responses.json").read_text())


def main():
    spec = yaml.safe_load((ROOT / "docs/api/openapi.yaml").read_text())
    with tempfile.TemporaryDirectory() as tmp:
        fixtures = record(tmp)
    errors = validate(spec, fixtures)
    for e in errors:
        print("FAIL", e)
    if errors:
        return 1
    print(f"PASS {len(fixtures)} real responses match docs/api/openapi.yaml")
    return 0


if __name__ == "__main__":
    sys.exit(main())
