#!/usr/bin/env python3
"""Deterministic repository checks for the Keycloak/OIDC guide skill."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from datetime import date
from pathlib import Path
from urllib.parse import unquote


REQUIRED_FILES = (
    "SKILL.md",
    "agents/openai.yaml",
    "README.md",
    "README.de.md",
    "references/foundations.md",
    "references/audit-and-journaling.md",
    "references/privacy-and-retention.md",
    "references/delegated-administration.md",
    "references/authoritative-sources.md",
    "references/de/grundlagen.md",
    "references/de/audit-und-journaling.md",
    "references/de/datenschutz-und-aufbewahrung.md",
    "references/de/delegierte-administration.md",
    "references/de/quellen.md",
    "tests/prompt-evals.json",
)

LANGUAGE_PAIRS = (
    ("README.md", "README.de.md"),
    ("references/foundations.md", "references/de/grundlagen.md"),
    ("references/audit-and-journaling.md", "references/de/audit-und-journaling.md"),
    ("references/privacy-and-retention.md", "references/de/datenschutz-und-aufbewahrung.md"),
    ("references/delegated-administration.md", "references/de/delegierte-administration.md"),
    ("references/authoritative-sources.md", "references/de/quellen.md"),
)

REQUIRED_HEADINGS = {
    "references/foundations.md": "Safe first explanation",
    "references/de/grundlagen.md": "Erste sichere Erklärung",
    "references/audit-and-journaling.md": "Profile data in logs and journals",
    "references/de/audit-und-journaling.md": "Profildaten in Logs und Journalen",
    "references/privacy-and-retention.md": "Legal consequences of additional copies",
    "references/de/datenschutz-und-aufbewahrung.md": "Rechtliche Folgen zusätzlicher Kopien",
    "references/delegated-administration.md": "The newly created or alternate account route",
    "references/de/delegierte-administration.md": "Der Weg über ein neues oder alternatives Konto",
}

LOCAL_LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
HEADING_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$", re.MULTILINE)


def read_text(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def check_required_files(root: Path, errors: list[str]) -> None:
    for relative in REQUIRED_FILES:
        if not (root / relative).is_file():
            errors.append(f"missing required file: {relative}")


def parse_frontmatter(skill_file: Path, errors: list[str]) -> dict[str, str]:
    lines = read_text(skill_file).splitlines()
    if not lines or lines[0] != "---":
        errors.append("SKILL.md must start with YAML frontmatter")
        return {}
    try:
        end = lines.index("---", 1)
    except ValueError:
        errors.append("SKILL.md frontmatter has no closing delimiter")
        return {}

    metadata: dict[str, str] = {}
    for line in lines[1:end]:
        if not line.strip():
            continue
        if ":" not in line:
            errors.append(f"invalid SKILL.md frontmatter line: {line!r}")
            continue
        key, value = line.split(":", 1)
        metadata[key.strip()] = value.strip().strip('"').strip("'")
    return metadata


def check_skill_metadata(root: Path, errors: list[str]) -> None:
    metadata = parse_frontmatter(root / "SKILL.md", errors)
    if set(metadata) != {"name", "description"}:
        errors.append("SKILL.md frontmatter must contain only name and description")

    name = metadata.get("name", "")
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name):
        errors.append("skill name must use lowercase letters, digits, and single hyphens")
    if len(name) > 64:
        errors.append("skill name must not exceed 64 characters")
    if root.name != name:
        errors.append(f"skill directory {root.name!r} does not match skill name {name!r}")

    description = metadata.get("description", "")
    if not description:
        errors.append("skill description must not be empty")
    if len(description) > 1024:
        errors.append("skill description must not exceed 1024 characters")
    if "<" in description or ">" in description:
        errors.append("skill description must not contain XML-style tags")


def parse_quoted_openai_yaml(path: Path, errors: list[str]) -> dict[str, str]:
    text = read_text(path)
    if not re.search(r"^interface:\s*$", text, re.MULTILINE):
        errors.append("agents/openai.yaml must contain an interface mapping")

    values: dict[str, str] = {}
    pattern = re.compile(
        r'^  (display_name|short_description|default_prompt):\s+"((?:[^"\\]|\\.)*)"\s*$',
        re.MULTILINE,
    )
    for key, escaped_value in pattern.findall(text):
        try:
            values[key] = json.loads(f'"{escaped_value}"')
        except json.JSONDecodeError as exc:
            errors.append(f"invalid quoted value for {key} in agents/openai.yaml: {exc}")

    expected = {"display_name", "short_description", "default_prompt"}
    if set(values) != expected:
        missing = ", ".join(sorted(expected - set(values))) or "none"
        errors.append(f"agents/openai.yaml is missing valid quoted interface fields: {missing}")
    return values


def check_openai_yaml(root: Path, errors: list[str]) -> None:
    values = parse_quoted_openai_yaml(root / "agents/openai.yaml", errors)
    short_description = values.get("short_description", "")
    if short_description and not 25 <= len(short_description) <= 64:
        errors.append("agents/openai.yaml short_description must be 25-64 characters")

    default_prompt = values.get("default_prompt", "")
    skill_name = parse_frontmatter(root / "SKILL.md", []).get("name", "")
    if skill_name and f"${skill_name}" not in default_prompt:
        errors.append("agents/openai.yaml default_prompt must mention the skill with $skill-name")


def normalize_local_target(raw_target: str) -> str | None:
    target = raw_target.strip()
    if target.startswith("<") and ">" in target:
        target = target[1 : target.index(">")]
    elif " " in target:
        target = target.split(" ", 1)[0]
    target = unquote(target)
    if target.startswith(("#", "http://", "https://", "mailto:", "data:")):
        return None
    return target.split("#", 1)[0].split("?", 1)[0] or None


def check_local_links(root: Path, errors: list[str]) -> None:
    for markdown_file in sorted(root.rglob("*.md")):
        for raw_target in LOCAL_LINK_RE.findall(read_text(markdown_file)):
            target = normalize_local_target(raw_target)
            if target is None:
                continue
            resolved = (markdown_file.parent / target).resolve()
            try:
                resolved.relative_to(root.resolve())
            except ValueError:
                errors.append(f"local link escapes repository: {markdown_file.relative_to(root)} -> {target}")
                continue
            if not resolved.exists():
                errors.append(f"broken local link: {markdown_file.relative_to(root)} -> {target}")


def structure_signature(text: str) -> tuple[list[int], int, int]:
    heading_levels = [len(marker) for marker, _ in HEADING_RE.findall(text)]
    fence_count = sum(1 for line in text.splitlines() if line.strip().startswith("```"))
    table_separators = sum(
        1
        for line in text.splitlines()
        if line.lstrip().startswith("|") and re.search(r"\|\s*:?-{3,}:?\s*\|", line)
    )
    return heading_levels, fence_count, table_separators


def check_language_parity(root: Path, errors: list[str]) -> None:
    for english, german in LANGUAGE_PAIRS:
        english_signature = structure_signature(read_text(root / english))
        german_signature = structure_signature(read_text(root / german))
        if english_signature != german_signature:
            errors.append(
                f"bilingual structure differs: {english} {english_signature} != "
                f"{german} {german_signature}"
            )

    for relative, required_heading in REQUIRED_HEADINGS.items():
        headings = {title for _, title in HEADING_RE.findall(read_text(root / relative))}
        if required_heading not in headings:
            errors.append(f"{relative} is missing required heading: {required_heading}")


def check_source_review_dates(root: Path, errors: list[str]) -> None:
    source_files = {
        "references/authoritative-sources.md": r"\*\*Last reviewed: (\d{4}-\d{2}-\d{2})\*\*",
        "references/de/quellen.md": r"\*\*Zuletzt geprüft: (\d{4}-\d{2}-\d{2})\*\*",
    }
    parsed: dict[str, date] = {}
    for relative, pattern in source_files.items():
        match = re.search(pattern, read_text(root / relative))
        if not match:
            errors.append(f"{relative} must contain an ISO review date")
            continue
        try:
            parsed[relative] = date.fromisoformat(match.group(1))
        except ValueError:
            errors.append(f"{relative} contains an invalid review date")

    if len(set(parsed.values())) > 1:
        errors.append("English and German source review dates must match")
    for relative, reviewed in parsed.items():
        age = (date.today() - reviewed).days
        if age < -1:
            errors.append(f"{relative} review date is more than one day in the future")
        elif age > 90:
            errors.append(f"{relative} source review is stale ({age} days; maximum 90)")


def check_prompt_evals(root: Path, errors: list[str]) -> None:
    path = root / "tests/prompt-evals.json"
    try:
        payload = json.loads(read_text(path))
    except (json.JSONDecodeError, OSError) as exc:
        errors.append(f"cannot parse tests/prompt-evals.json: {exc}")
        return

    if payload.get("schema_version") != 1:
        errors.append("prompt eval schema_version must be 1")
    cases = payload.get("cases")
    if not isinstance(cases, list) or not cases:
        errors.append("prompt evals must contain a non-empty cases list")
        return

    ids: set[str] = set()
    counts: Counter[tuple[str, bool]] = Counter()
    positive_topics: set[str] = set()
    for index, case in enumerate(cases):
        label = f"prompt eval case {index + 1}"
        if not isinstance(case, dict):
            errors.append(f"{label} must be an object")
            continue
        case_id = case.get("id")
        if not isinstance(case_id, str) or not re.fullmatch(r"[a-z0-9_]+", case_id):
            errors.append(f"{label} has an invalid id")
        elif case_id in ids:
            errors.append(f"duplicate prompt eval id: {case_id}")
        else:
            ids.add(case_id)

        language = case.get("language")
        should_trigger = case.get("should_trigger")
        if language not in {"en", "de"}:
            errors.append(f"{label} language must be en or de")
        if not isinstance(should_trigger, bool):
            errors.append(f"{label} should_trigger must be boolean")
            continue
        if language in {"en", "de"}:
            counts[(language, should_trigger)] += 1
        if not isinstance(case.get("prompt"), str) or not case["prompt"].strip():
            errors.append(f"{label} prompt must be non-empty")

        topics = case.get("expected_topics", [])
        if should_trigger:
            if not isinstance(topics, list) or not topics or not all(
                isinstance(topic, str) and topic for topic in topics
            ):
                errors.append(f"{label} needs non-empty expected_topics")
            else:
                positive_topics.update(topics)
        elif not isinstance(case.get("reason"), str) or not case["reason"].strip():
            errors.append(f"{label} needs a reason for not triggering")

    for language in ("en", "de"):
        if counts[(language, True)] < 4:
            errors.append(f"prompt evals need at least four positive {language} cases")
        if counts[(language, False)] < 2:
            errors.append(f"prompt evals need at least two negative {language} cases")

    required_topics = {
        "profile_data_boundary",
        "copy_lifecycle",
        "legal_consequences",
        "application_audit",
        "new_account_escalation",
        "oidc_validation",
        "retention",
    }
    missing_topics = sorted(required_topics - positive_topics)
    if missing_topics:
        errors.append(f"prompt evals do not cover topics: {', '.join(missing_topics)}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", nargs="?", default=".", help="skill repository root")
    args = parser.parse_args()
    root = Path(args.root).resolve()

    errors: list[str] = []
    check_required_files(root, errors)
    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        return 1

    check_skill_metadata(root, errors)
    check_openai_yaml(root, errors)
    check_local_links(root, errors)
    check_language_parity(root, errors)
    check_source_review_dates(root, errors)
    check_prompt_evals(root, errors)

    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        print(f"Validation failed with {len(errors)} error(s).")
        return 1

    print("Validation passed: metadata, local links, bilingual structure, source dates, and prompt evals.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
