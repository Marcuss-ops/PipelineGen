#!/usr/bin/env python3
"""Validate the runner contract for smoke and strict boxer scenarios."""

from __future__ import annotations

import argparse
import copy
import json
import re
import sqlite3
import sys
from pathlib import Path
from typing import Any, Iterable


MODES = {"smoke", "strict"}
STRICT_STATUSES = {"completed", "SUCCEEDED"}
SMOKE_STATUSES = STRICT_STATUSES | {"SUCCEEDED_WITH_WARNINGS"}


def _walk(value: Any) -> Iterable[dict[str, Any]]:
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from _walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from _walk(child)


def _first_job_status(response: dict[str, Any]) -> str:
    status = response.get("status")
    if isinstance(status, str) and status:
        return status
    job = response.get("job")
    if isinstance(job, dict) and isinstance(job.get("status"), str):
        return job["status"]
    return ""


def _warnings(response: dict[str, Any]) -> list[str]:
    result: list[str] = []
    for obj in _walk(response):
        value = obj.get("warnings")
        if isinstance(value, list):
            result.extend(str(item) for item in value if str(item).strip())
        elif isinstance(value, str) and value.strip():
            result.append(value)
    return result


def _fallbacks(response: dict[str, Any]) -> list[str]:
    result: list[str] = []
    for obj in _walk(response):
        if obj.get("fallback") is True:
            result.append(str(obj.get("asset_id", "<unknown>")))
    return result


def _bound_ids(response: dict[str, Any]) -> list[tuple[str, str]]:
    """Return stock asset IDs and supplied clip IDs from output bindings."""
    result: set[tuple[str, str]] = set()
    for obj in _walk(response):
        for field in ("asset_id", "clip_id"):
            bound_id = obj.get(field)
            if isinstance(bound_id, str) and bound_id.strip():
                # Strict mode deliberately fails closed: a binding without
                # drive/source metadata must not evade lifecycle validation.
                result.add((field, bound_id.strip()))
    return sorted(result)


def _asset_ids(response: dict[str, Any]) -> list[str]:
    return [bound_id for _, bound_id in _bound_ids(response)]


def prepare_payload(payload: dict[str, Any], mode: str) -> dict[str, Any]:
    """Strip runner metadata and enforce the quality-gate mode contract."""
    if mode not in MODES:
        raise ValueError(f"invalid runner mode: {mode!r}")
    prepared = copy.deepcopy(payload)
    prepared.pop("_runner", None)
    if mode == "strict":
        items = prepared.get("items")
        if not isinstance(items, list):
            raise ValueError("scenario payload must contain an items array")
        for item in items:
            if not isinstance(item, dict):
                raise ValueError("scenario items must be objects")
            script_params = item.setdefault("script_params", {})
            if not isinstance(script_params, dict):
                raise ValueError("scenario item script_params must be an object")
            script_params["skip_quality_gate"] = False
    return prepared


def validate_response(
    response: dict[str, Any],
    mode: str,
    db_path: str | None = None,
    allowed_warning_regex: str = "",
) -> list[str]:
    if mode not in MODES:
        return [f"invalid runner mode: {mode!r}"]

    errors: list[str] = []
    status = _first_job_status(response)
    accepted_statuses = STRICT_STATUSES if mode == "strict" else SMOKE_STATUSES
    if status not in accepted_statuses:
        errors.append(f"job status {status!r} is not accepted in {mode} mode")

    warning_statuses = [
        str(obj.get("status"))
        for obj in _walk(response)
        if obj.get("status") == "SUCCEEDED_WITH_WARNINGS"
    ]
    if mode == "strict" and warning_statuses:
        errors.append("SUCCEEDED_WITH_WARNINGS is forbidden in strict mode")

    warnings = _warnings(response)
    if mode == "strict" and warnings:
        errors.extend(f"warning is forbidden in strict mode: {warning}" for warning in warnings)
    elif mode == "smoke":
        if warning_statuses and not warnings:
            errors.append("SUCCEEDED_WITH_WARNINGS has no explicit warning permitted by smoke policy")
        elif warnings:
            if not allowed_warning_regex:
                errors.extend(f"warning is not allowed in smoke mode: {warning}" for warning in warnings)
            else:
                matcher = re.compile(allowed_warning_regex, re.IGNORECASE)
                disallowed = [warning for warning in warnings if not matcher.search(warning)]
                errors.extend(f"warning is not allowed in smoke mode: {warning}" for warning in disallowed)

    fallbacks = _fallbacks(response)
    if mode == "strict" and fallbacks:
        errors.append(f"fallback binding is forbidden in strict mode: {', '.join(fallbacks)}")

    if mode == "strict" and db_path:
        connection = sqlite3.connect(db_path)
        try:
            columns = {row[1] for row in connection.execute("PRAGMA table_info(media_assets)")}
            if "lifecycle_state" not in columns:
                errors.append("media_assets.lifecycle_state is required for strict asset validation")
            else:
                for asset_id in _asset_ids(response):
                    row = connection.execute(
                        "SELECT lifecycle_state FROM media_assets WHERE id = ?", (asset_id,)
                    ).fetchone()
                    if row is None:
                        errors.append(f"strict asset {asset_id!r} is missing from SQLite")
                    elif str(row[0]).strip().upper() != "ACTIVE":
                        errors.append(
                            f"strict asset {asset_id!r} has lifecycle_state {row[0]!r}, expected ACTIVE"
                        )
        finally:
            connection.close()

    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    validate = sub.add_parser("validate")
    validate.add_argument("--mode", required=True, choices=sorted(MODES))
    validate.add_argument("--response", required=True, type=Path)
    validate.add_argument("--db", default="")
    validate.add_argument("--allowed-warning-regex", default="")
    prepare = sub.add_parser("prepare")
    prepare.add_argument("--mode", required=True, choices=sorted(MODES))
    prepare.add_argument("--input", required=True, type=Path)
    prepare.add_argument("--output", required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        if args.command == "prepare":
            payload = json.loads(args.input.read_text(encoding="utf-8"))
            if not isinstance(payload, dict):
                raise ValueError("scenario payload must be an object")
            prepared = prepare_payload(payload, args.mode)
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(prepared, indent=2) + "\n", encoding="utf-8")
            return 0

        response = json.loads(args.response.read_text(encoding="utf-8"))
        if not isinstance(response, dict):
            raise ValueError("response JSON must be an object")
        errors = validate_response(
            response,
            args.mode,
            args.db or None,
            args.allowed_warning_regex,
        )
    except (OSError, json.JSONDecodeError, sqlite3.Error, ValueError, re.error) as exc:
        print(f"runner policy error: {exc}", file=sys.stderr)
        return 2
    if errors:
        for error in errors:
            print(f"runner policy: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
