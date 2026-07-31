#!/usr/bin/env python3
"""Validation and atomic publication helpers for boxer job reports."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any, Iterable


TERMINAL_STATUSES = {
    "completed",
    "SUCCEEDED",
    "SUCCEEDED_WITH_WARNINGS",
    "failed",
    "FAILED",
    "cancelled",
    "CANCELLED",
    "dead_letter",
    "DEAD_LETTER",
}


def _dicts(value: Any) -> Iterable[dict[str, Any]]:
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from _dicts(child)
    elif isinstance(value, list):
        for child in value:
            yield from _dicts(child)


def job_status(payload: dict[str, Any]) -> str:
    """Read the job status from common API/full-response envelopes."""
    paths = (
        ("status",),
        ("job", "status"),
        ("result", "status"),
        ("job", "result", "status"),
    )
    for path in paths:
        value: Any = payload
        for key in path:
            if not isinstance(value, dict):
                value = None
                break
            value = value.get(key)
        if isinstance(value, str) and value.strip():
            return value.strip()
    return ""


def select_terminal_parent(responses: Iterable[dict[str, Any]]) -> dict[str, Any]:
    """Select the first terminal /full response, ignoring stale RUNNING data."""
    last_status = ""
    for response in responses:
        last_status = job_status(response)
        if last_status in TERMINAL_STATUSES:
            return response
    raise ValueError(f"parent full response never became terminal (last={last_status!r})")


def child_job_ids(payload: dict[str, Any]) -> list[str]:
    paths = (
        ("result", "data", "child_job_ids"),
        ("result", "child_job_ids"),
        ("job", "result", "data", "child_job_ids"),
        ("job", "result", "child_job_ids"),
    )
    for path in paths:
        value: Any = payload
        for key in path:
            if not isinstance(value, dict):
                value = None
                break
            value = value.get(key)
        if isinstance(value, list):
            return [str(item).strip() for item in value if str(item).strip()]
    return []


def has_result(payload: dict[str, Any]) -> bool:
    """Require a non-empty result envelope, not merely a status."""
    for obj in _dicts(payload):
        result = obj.get("result")
        if isinstance(result, dict) and result:
            return True
    return False


def validate_parent_and_children(
    parent: dict[str, Any],
    children: dict[str, dict[str, Any]],
    expected_child_ids: list[str] | None = None,
) -> list[str]:
    """Fail closed unless parent and every expected child are complete."""
    errors: list[str] = []
    parent_status = job_status(parent)
    if parent_status not in TERMINAL_STATUSES:
        errors.append(
            f"parent job status {parent_status!r} is not terminal; refusing report publication"
        )

    expected = [str(item).strip() for item in (expected_child_ids or []) if str(item).strip()]
    actual = child_job_ids(parent)
    if expected:
        if len(actual) != len(set(actual)):
            errors.append("parent child_job_ids contains duplicates")
        if sorted(actual) != sorted(expected):
            errors.append(
                f"parent child_job_ids changed or are incomplete: expected {expected!r}, got {actual!r}"
            )
    if expected and len(children) != len(expected):
        errors.append(f"child response count {len(children)} does not match expected {len(expected)}")

    for child_id in expected or sorted(children):
        child = children.get(child_id)
        if child is None:
            errors.append(f"missing full response for child job {child_id!r}")
            continue
        status = job_status(child)
        if status not in TERMINAL_STATUSES:
            errors.append(f"child job {child_id!r} status {status!r} is not terminal")
        if not has_result(child):
            errors.append(f"child job {child_id!r} has no result payload")
    return errors


def atomic_copy(source: Path, destination: Path) -> None:
    """Publish a file via a same-directory temporary and atomic replace."""
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(f".{destination.name}.tmp")
    temporary.write_bytes(source.read_bytes())
    temporary.replace(destination)


def archive_incomplete(paths: Iterable[Path], directory: Path, stamp: str) -> list[Path]:
    """Move failed-attempt artifacts into an incomplete evidence directory."""
    directory.mkdir(parents=True, exist_ok=True)
    archived: list[Path] = []
    for path in paths:
        if not path.exists():
            continue
        target = directory / f"{stamp}_{path.name}"
        path.replace(target)
        archived.append(target)
    return archived


def _main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    validate = sub.add_parser("validate")
    validate.add_argument("--parent", required=True, type=Path)
    validate.add_argument("--children-dir", required=True, type=Path)
    validate.add_argument("--expected-child-ids", required=True, type=Path)
    args = parser.parse_args(argv)

    try:
        parent = json.loads(args.parent.read_text(encoding="utf-8"))
        expected = [
            line.strip()
            for line in args.expected_child_ids.read_text(encoding="utf-8").splitlines()
            if line.strip()
        ]
        children: dict[str, dict[str, Any]] = {}
        for child_id in expected:
            path = args.children_dir / f"{child_id}.json"
            if path.exists():
                value = json.loads(path.read_text(encoding="utf-8"))
                if isinstance(value, dict):
                    children[child_id] = value
        errors = validate_parent_and_children(parent, children, expected)
    except (OSError, json.JSONDecodeError, TypeError, AttributeError) as exc:
        print(f"report publication validation error: {exc}", flush=True)
        return 2
    if errors:
        for error in errors:
            print(f"report publication: {error}", flush=True)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(_main())
