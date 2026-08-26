#!/usr/bin/env python3
"""Canonical ML model weights downloader (registry SSOT).

Downloads and verifies model weights on the server using the canonical
model registry mirror (scripts/services/model_registry_generated.py,
generated from internal/kernel/models by cmd/model-registry-gen).

The registry is the SINGLE source of truth for model ids, revisions,
and checksums. This tool never hardcodes a model name — every download
anchors to MODEL_REGISTRY and pins the registry revision when present.

Modes:
    default             download every CORE (enabled) model
    --all               download every registry entry (incl. OPTIONAL)
    --models id[,id]    download specific repo ids (validated against the
                        registry; unknown ids fail closed)
    --dry-run           resolve + print the download plan, no network I/O
    --verify-only       verify cached weights only, no download
    --cache-dir DIR     huggingface_hub cache directory override

Exit codes:
    0   every requested model downloaded + verified (or dry-run planned)
    1   a model failed (download, revision, or checksum mismatch)
    2   usage / registry error

Checksum contract (godlike/06): the registry carries a SHA-256 of the
weights blob, empty until the first download is verified against the
upstream hub. When a registry entry has a non-empty checksum this tool
hashes every file of the downloaded snapshot and requires a match; empty
checksum is reported as "skipped" so the wiring is enforced as soon as
checksums are pinned.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path
from typing import Any

try:
    from scripts.services.model_registry_generated import MODEL_REGISTRY
except ModuleNotFoundError:  # direct `python scripts/tools/model_downloader.py`
    sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
    from scripts.services.model_registry_generated import (  # type: ignore[no-redef]
        MODEL_REGISTRY,
    )


def _report(ok: bool, code: int, models: list[dict[str, Any]]) -> int:
    """Emit exactly one machine-readable JSON document and return its code."""
    print(json.dumps({"ok": ok, "models": models}, sort_keys=True))
    return code


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _checksum_verify(model_id: str, checksum: str, snapshot_dir: Path) -> tuple[bool, str]:
    """Verify a downloaded snapshot against the registry checksum.

    The checksum targets the weights blob: every file under the snapshot
    is hashed and any match is accepted (model repos carry config +
    weights; the weights file(s) are the identity anchor). An empty
    registry checksum is reported as skipped.
    """
    if not checksum:
        return True, "skipped (no checksum pinned in registry)"
    files = [p for p in snapshot_dir.rglob("*") if p.is_file()]
    if not files:
        return False, f"snapshot is empty; cannot verify checksum {checksum}"
    for path in files:
        if _sha256_file(path) == checksum:
            return True, "verified (sha256 match)"
    return False, (
        f"no file under {snapshot_dir} matches registry checksum {checksum}"
    )


def _resolve_plan(model_ids: list[str], include_all: bool) -> list[tuple[str, dict[str, Any]]]:
    """Resolve the registry entries to download, in stable registry order."""
    if model_ids:
        missing = [mid for mid in model_ids if mid not in MODEL_REGISTRY]
        if missing:
            raise ValueError(
                f"unknown model id(s) {', '.join(missing)} — not in the canonical "
                f"registry (see scripts/services/model_registry_generated.py)"
            )
        return [(mid, MODEL_REGISTRY[mid]) for mid in model_ids]
    ordered = list(MODEL_REGISTRY.items())
    if not include_all:
        ordered = [(mid, entry) for mid, entry in ordered if entry.get("enabled")]
    return ordered


def _download_one(model_id: str, entry: dict[str, Any], cache_dir: str | None) -> dict[str, Any]:
    revision = entry.get("revision") or ""
    checksum = entry.get("checksum") or ""
    result: dict[str, Any] = {
        "id": model_id,
        "revision": revision,
        "enabled": bool(entry.get("enabled")),
    }

    try:
        from huggingface_hub import snapshot_download
    except ImportError as exc:  # pragma: no cover - env-dependent
        result["error"] = f"huggingface_hub is not installed: {exc}"
        result["verified"] = False
        return result

    try:
        path = snapshot_download(
            repo_id=model_id,
            revision=revision or None,
            cache_dir=cache_dir,
        )
    except Exception as exc:  # noqa: BLE001 - fail-closed machine-readable error
        result["error"] = f"download failed: {exc}"
        result["verified"] = False
        return result

    result["path"] = path
    verified, note = _checksum_verify(model_id, checksum, Path(path))
    result["verified"] = verified
    result["verification"] = note
    if not verified:
        result["error"] = f"checksum mismatch for {model_id}"
    return result


def _verify_one(model_id: str, entry: dict[str, Any], cache_dir: str | None) -> dict[str, Any]:
    revision = entry.get("revision") or ""
    checksum = entry.get("checksum") or ""
    result: dict[str, Any] = {
        "id": model_id,
        "revision": revision,
        "enabled": bool(entry.get("enabled")),
    }

    try:
        from huggingface_hub import scan_cache_dir
    except ImportError as exc:  # pragma: no cover - env-dependent
        result["error"] = f"huggingface_hub is not installed: {exc}"
        result["verified"] = False
        return result

    try:
        info = scan_cache_dir(cache_dir)
        revision_ref = revision or None
        hits = []
        for repo in info.repos:
            if repo.repo_id != model_id or not repo.revisions:
                continue
            if revision_ref is None or repo.revisions[0].commit_hash.startswith(revision_ref):
                hits.append(repo)
        if not hits:
            result["verified"] = False
            result["error"] = "model not found in HF cache"
            return result
        path = hits[0].repo_path
    except Exception as exc:  # noqa: BLE001 - fail-closed machine-readable error
        result["error"] = f"cache scan failed: {exc}"
        result["verified"] = False
        return result

    result["path"] = str(path)
    verified, note = _checksum_verify(model_id, checksum, Path(path))
    result["verified"] = verified
    result["verification"] = note
    if not verified:
        result["error"] = f"checksum mismatch for {model_id}"
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--all", action="store_true", help="download every registry entry (incl. OPTIONAL)")
    parser.add_argument("--models", help="comma-separated repo ids; validated against the registry")
    parser.add_argument("--dry-run", action="store_true", help="print the download plan without network I/O")
    parser.add_argument("--verify-only", action="store_true", help="verify cached weights only, no download")
    parser.add_argument("--cache-dir", help="huggingface_hub cache directory override")
    args = parser.parse_args(argv)

    if args.all and args.models:
        parser.error("--all and --models are mutually exclusive")
    if args.dry_run and args.verify_only:
        parser.error("--dry-run and --verify-only are mutually exclusive")

    model_ids = [mid.strip() for mid in args.models.split(",")] if args.models else []
    try:
        plan = _resolve_plan(model_ids, include_all=args.all)
    except ValueError as exc:
        print(json.dumps({"ok": False, "error": str(exc)}, sort_keys=True))
        return 2

    if args.dry_run:
        print(json.dumps({
            "ok": True,
            "dry_run": True,
            "models": [
                {
                    "id": mid,
                    "revision": entry.get("revision") or "",
                    "checksum": entry.get("checksum") or "",
                    "enabled": bool(entry.get("enabled")),
                }
                for mid, entry in plan
            ],
        }, sort_keys=True))
        return 0

    results = [
        (_verify_one if args.verify_only else _download_one)(mid, entry, args.cache_dir)
        for mid, entry in plan
    ]
    failed = [res for res in results if not res.get("verified") or res.get("error")]
    return _report(not failed, 1 if failed else 0, results)


if __name__ == "__main__":
    sys.exit(main())
