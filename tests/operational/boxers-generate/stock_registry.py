#!/usr/bin/env python3
"""Resolve and materialize the single boxer stock registry."""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
from pathlib import Path
from typing import Any


ROLES = {"fight", "interview", "training"}
REQUIRED_PACQUIAO_ROLES = ("fight", "interview", "training")


def _text(value: Any) -> str:
    return str(value or "").strip()


def _norm(value: Any) -> str:
    return " ".join(_text(value).casefold().replace("_", " ").split())


def load_json(path: str | os.PathLike[str]) -> dict[str, Any]:
    with open(path, encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"JSON object required: {path}")
    return value


def _asset_entries(registry: dict[str, Any]):
    boxers = registry.get("boxers")
    scene_order = registry.get("scene_order")
    if not isinstance(boxers, dict) or not boxers:
        raise ValueError("registry.boxers must be a non-empty object")
    if not isinstance(scene_order, list) or not scene_order:
        raise ValueError("registry.scene_order must be a non-empty array")
    if len(set(scene_order)) != len(scene_order):
        raise ValueError("registry.scene_order contains duplicate subjects")
    for boxer_key in scene_order:
        if boxer_key not in boxers:
            raise ValueError(f"scene_order references unknown boxer {boxer_key!r}")

    seen: dict[str, tuple[str, str]] = {}
    for boxer_key, boxer in boxers.items():
        if not isinstance(boxer, dict):
            raise ValueError(f"boxer {boxer_key!r} must be an object")
        subject = _text(boxer.get("subject"))
        if not subject:
            raise ValueError(f"boxer {boxer_key!r} has no subject")
        assets = boxer.get("assets", {})
        if not isinstance(assets, dict):
            raise ValueError(f"boxer {boxer_key!r}.assets must be an object")
        for role, asset in assets.items():
            if role not in ROLES:
                raise ValueError(f"unsupported role {role!r} for {boxer_key}")
            if not isinstance(asset, dict):
                raise ValueError(f"asset {boxer_key}.{role} must be an object")
            asset_id = _text(asset.get("asset_id"))
            if not asset_id or asset_id.startswith("PLACEHOLDER"):
                raise ValueError(f"asset {boxer_key}.{role} has no concrete asset_id")
            owner = (boxer_key, role)
            previous = seen.get(asset_id)
            if previous and previous != owner:
                raise ValueError(
                    f"asset {asset_id!r} is assigned to both "
                    f"{previous[0]}.{previous[1]} and {boxer_key}.{role}"
                )
            seen[asset_id] = owner
            yield boxer_key, subject, role, asset

        clips = boxer.get("clips", [])
        if not isinstance(clips, list):
            raise ValueError(f"boxer {boxer_key!r}.clips must be an array")
        for index, clip in enumerate(clips):
            if not isinstance(clip, dict):
                raise ValueError(f"clip {boxer_key}.clips[{index}] must be an object")
            asset_id = _text(clip.get("asset_id"))
            if not asset_id:
                raise ValueError(f"clip {boxer_key}.clips[{index}] has no asset_id")
            owner = (boxer_key, f"clip_{index + 1}")
            previous = seen.get(asset_id)
            if previous and previous != owner:
                raise ValueError(
                    f"asset {asset_id!r} is assigned to both "
                    f"{previous[0]}.{previous[1]} and {boxer_key}.clips[{index}]"
                )
            seen[asset_id] = owner


def validate_registry(registry: dict[str, Any]) -> None:
    """Reject missing identity, unsupported roles, and duplicate assets."""
    list(_asset_entries(registry))
    pacquiao = registry["boxers"].get("manny_pacquiao", {})
    assets = pacquiao.get("assets", {}) if isinstance(pacquiao, dict) else {}
    missing = [role for role in REQUIRED_PACQUIAO_ROLES if not isinstance(assets.get(role), dict)]
    if missing:
        raise ValueError(
            "manny_pacquiao requires three validated stock assets; "
            f"missing roles: {', '.join(missing)}"
        )


def load_resolved_stock(path: str | os.PathLike[str]) -> dict[str, Any]:
    resolved = load_json(path)
    validate_registry(resolved)
    return resolved


def scene_expectations(resolved: dict[str, Any]) -> list[dict[str, Any]]:
    """Return ordered scene expectations derived only from the resolved registry."""
    result = []
    for index, boxer_key in enumerate(resolved["scene_order"]):
        boxer = resolved["boxers"][boxer_key]
        fight = boxer.get("assets", {}).get("fight")
        if not isinstance(fight, dict) or not _text(fight.get("asset_id")):
            # Subjects without validated stock remain BLOCKED and are not
            # included in expectations until their registry entry is filled.
            continue
        result.append({
            "index": index,
            "boxer_key": boxer_key,
            "subject": boxer["subject"],
            "segment_id": f"boxer-{boxer_key.replace('_', '-')}",
            "source_marker": f"SRC_{boxer_key.upper()}_0{index + 1}",
            "asset_id": fight["asset_id"],
            "drive_link": fight.get("drive_link", ""),
            "role": "fight",
        })
    return result


def _asset_lookup(resolved: dict[str, Any]) -> dict[str, dict[str, Any]]:
    lookup: dict[str, dict[str, Any]] = {}
    for boxer_key, boxer in resolved["boxers"].items():
        for role, asset in boxer.get("assets", {}).items():
            asset_id = _text(asset.get("asset_id"))
            if asset_id in lookup:
                raise ValueError(f"resolved asset duplicated: {asset_id}")
            entry = dict(asset)
            entry.update({"boxer_key": boxer_key, "subject": boxer["subject"], "role": role})
            lookup[asset_id] = entry
    return lookup


def _resolve_token(token: str, resolved: dict[str, Any]) -> Any:
    if not (token.startswith("{{stock.") and token.endswith("}}")):
        return token
    parts = token[len("{{stock."):-2].split(".")
    if len(parts) != 3:
        raise ValueError(f"invalid stock token {token!r}")
    boxer_key, role, field = parts
    asset = resolved.get("boxers", {}).get(boxer_key, {}).get("assets", {}).get(role)
    if not isinstance(asset, dict) or field not in asset:
        raise ValueError(f"unknown stock token {token!r}")
    return asset[field]


def _materialize_binding(binding: dict[str, Any], resolved: dict[str, Any], lookup: dict[str, dict[str, Any]]) -> dict[str, Any]:
    raw_asset_id = binding.get("asset_id")
    if not isinstance(raw_asset_id, str) or not raw_asset_id:
        return {key: materialize(value, resolved) for key, value in binding.items()}

    if raw_asset_id.startswith("{{stock."):
        asset_id = _resolve_token(raw_asset_id, resolved)
        if not isinstance(asset_id, str):
            raise ValueError(f"stock asset_id token did not resolve to text: {raw_asset_id!r}")
        output = {key: materialize(value, resolved) for key, value in binding.items()}
        output["asset_id"] = asset_id
        # The resolved registry owns the link when the scenario uses a token.
        output["drive_link"] = lookup[asset_id].get("drive_link", "")
        return output

    # Every stock binding must use a registry asset. No symbolic or
    # cross-subject fallback values are accepted.
    if raw_asset_id not in lookup:
        raise ValueError(
            f"scenario references unavailable or unregistered asset: {raw_asset_id!r}"
        )
    return {key: materialize(value, resolved) for key, value in binding.items()}


def materialize(value: Any, resolved: dict[str, Any]) -> Any:
    """Replace ``{{stock.<boxer>.<role>.<field>}}`` tokens from resolved stock."""
    lookup = _asset_lookup(resolved)
    if isinstance(value, list):
        return [materialize(item, resolved) for item in value]
    if isinstance(value, dict):
        if isinstance(value.get("asset_id"), str):
            return _materialize_binding(value, resolved, lookup)
        return {key: materialize(item, resolved) for key, item in value.items()}
    if isinstance(value, str) and value.startswith("{{stock."):
        return _resolve_token(value, resolved)
    return value


def _table_columns(connection: sqlite3.Connection) -> set[str]:
    return {row[1] for row in connection.execute("PRAGMA table_info(media_assets)")}


def _resolve_from_db(connection: sqlite3.Connection, asset_id: str, subject: str, role: str, expected_source: str) -> dict[str, Any]:
    columns = _table_columns(connection)
    required = {"id", "lifecycle_state", "source", "drive_link"}
    missing = required - columns
    if missing:
        raise ValueError(f"media_assets missing resolver columns: {sorted(missing)}")
    selected = ["id", "lifecycle_state", "source", "drive_link"]
    for optional in ("name", "folder_path", "filename", "search_text", "folder_id", "metadata_json", "source_provider"):
        if optional in columns:
            selected.append(optional)
    row = connection.execute(
        f"SELECT {', '.join(selected)} FROM media_assets WHERE id = ?", (asset_id,)
    ).fetchone()
    if row is None:
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} is not present in SQLite")
    record = dict(zip(selected, row))
    if _text(record.get("lifecycle_state")).upper() != "ACTIVE":
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} lifecycle_state is {record.get('lifecycle_state')!r}, expected ACTIVE")
    if "source_provider" in record:
        provider = _text(record.get("source_provider"))
        if _norm(provider) != _norm(expected_source):
            raise ValueError(f"{subject}.{role}: asset {asset_id!r} provider is {provider!r}, expected {expected_source!r}")
    elif _norm(record.get("source")) != _norm(expected_source):
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} provider is {record.get('source')!r}, expected {expected_source!r}")
    if "source_provider" in record and _norm(record.get("source")) != _norm(expected_source):
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} source is {record.get('source')!r}, expected {expected_source!r}")
    if not _text(record.get("drive_link")):
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} has empty drive_link")
    searchable = " ".join(_text(record.get(column)) for column in ("name", "folder_path", "filename", "search_text") if column in record)
    searchable_normalized = _norm(searchable)
    metadata: dict[str, Any] = {}
    metadata_raw = record.get("metadata_json")
    if _text(metadata_raw):
        try:
            decoded_metadata = json.loads(metadata_raw)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{subject}.{role}: asset {asset_id!r} metadata_json is invalid") from exc
        if not isinstance(decoded_metadata, dict):
            raise ValueError(f"{subject}.{role}: asset {asset_id!r} metadata_json must be an object")
        metadata = decoded_metadata

    declared_subject = next(
        (_text(metadata.get(key)) for key in ("subject", "subject_name", "asset_subject") if _text(metadata.get(key))),
        "",
    )
    if _norm(subject) not in searchable_normalized and _norm(declared_subject) != _norm(subject):
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} subject metadata does not match {subject!r}")

    declared_role = next(
        (_text(metadata.get(key)) for key in ("role", "asset_role", "stock_role") if _text(metadata.get(key))),
        "",
    )
    if _norm(role) not in searchable_normalized and _norm(declared_role) != _norm(role):
        raise ValueError(f"{subject}.{role}: asset {asset_id!r} role metadata does not match {role!r}")
    if declared_role and _norm(declared_role) != _norm(role):
        raise ValueError(
            f"{subject}.{role}: asset {asset_id!r} metadata role is "
            f"{declared_role!r}, expected {role!r}"
        )
    return record


def resolve_registry(registry: dict[str, Any], db_path: str | None = None) -> dict[str, Any]:
    validate_registry(registry)
    connection = None
    if db_path:
        if not os.path.exists(db_path):
            raise ValueError(f"SQLite database not found: {db_path}")
        connection = sqlite3.connect(db_path)
    try:
        resolved: dict[str, Any] = {
            "schema_version": registry.get("schema_version", 1),
            "scene_order": list(registry["scene_order"]),
            "source_registry": "boxers_stock_registry.json",
            "boxers": {},
        }
        for boxer_key, boxer in registry["boxers"].items():
            output_boxer = {"subject": boxer["subject"], "assets": {}, "clips": list(boxer.get("clips", []))}
            for role, asset in boxer.get("assets", {}).items():
                entry = dict(asset)
                entry.update({"role": role, "subject": boxer["subject"]})
                if connection is not None:
                    entry["db"] = _resolve_from_db(
                        connection, entry["asset_id"], boxer["subject"], role,
                        _text(entry.get("expected_source", "youtube")),
                    )
                    entry["drive_link"] = entry["db"]["drive_link"]
                elif not _text(entry.get("drive_link")):
                    raise ValueError(f"{boxer['subject']}.{role}: drive_link is required without SQLite")
                output_boxer["assets"][role] = entry
            resolved["boxers"][boxer_key] = output_boxer
        return resolved
    finally:
        if connection is not None:
            connection.close()


def write_json(value: dict[str, Any], path: str) -> None:
    destination = Path(path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with destination.open("w", encoding="utf-8") as handle:
        json.dump(value, handle, indent=2, sort_keys=True)
        handle.write("\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    resolve = sub.add_parser("resolve")
    resolve.add_argument("--registry", required=True)
    resolve.add_argument("--output", required=True)
    resolve.add_argument("--db", default="")
    materialize_command = sub.add_parser("materialize")
    materialize_command.add_argument("--resolved", required=True)
    materialize_command.add_argument("--input", required=True)
    materialize_command.add_argument("--output", required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "resolve":
            write_json(resolve_registry(load_json(args.registry), args.db or None), args.output)
        else:
            write_json(materialize(load_json(args.input), load_resolved_stock(args.resolved)), args.output)
    except (OSError, ValueError, sqlite3.Error) as exc:
        print(f"stock registry error: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
