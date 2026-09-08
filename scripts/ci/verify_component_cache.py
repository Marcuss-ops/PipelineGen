"""Persistent verification cache (Git-ignored, rebuildable state)."""
from __future__ import annotations

import json
import os
import re
import time
from pathlib import Path
from typing import Any, Mapping

from verify_component_core import CACHE_DIR, CACHE_SCHEMA_VERSION, FINGERPRINT_SCHEMA_VERSION
from verify_runtime import now_utc as _now_utc, write_json_report

def cache_root(root: Path) -> Path:
    """Return the Git-ignored directory holding verification cache records."""
    return root / CACHE_DIR


def cache_entry_path(root: Path, component: str, fingerprint: str) -> Path:
    """Return the record path for one component/fingerprint pair."""
    return cache_root(root) / component / f"{fingerprint}.json"


def read_cache_entry(root: Path, component: str, fingerprint: str) -> dict[str, Any] | None:
    """Read a cache record, failing closed on any structural problem.

    Only well-formed PASS records with the exact requested fingerprint are
    returned.  Missing, corrupt, non-dict, non-PASS, or fingerprint-mismatched
    entries all resolve to ``None`` so the caller re-runs the gate.
    """
    path = cache_entry_path(root, component, fingerprint)
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    if not isinstance(raw, dict):
        return None
    if raw.get("status") != "PASS":
        return None
    if raw.get("fingerprint") != fingerprint:
        return None
    return raw


def write_cache_entry(
    root: Path, component: str, fingerprint: str, record: Mapping[str, Any]
) -> bool:
    """Atomically write a cache record, returning success."""
    path = cache_entry_path(root, component, fingerprint)
    try:
        write_json_report(path, record)
    except OSError:
        return False
    return True


def store_cache_pass(
    root: Path,
    component: str,
    fingerprint: str,
    duration_ms: int,
    mode: str,
    toolchain: Mapping[str, str] | None,
) -> bool:
    """Persist a successful deterministic verification, and only a success.

    FAIL/TIMEOUT/CANCELLED outcomes are deliberately never cached: a failed
    gate must be re-run on the next invocation rather than remembered as a
    reason to skip it.
    """
    record: dict[str, Any] = {
        "schema_version": CACHE_SCHEMA_VERSION,
        "cache_schema_version": FINGERPRINT_SCHEMA_VERSION,
        "gate": component,
        "fingerprint": fingerprint,
        "status": "PASS",
        "mode": mode,
        "duration_ms": duration_ms,
        "toolchain": dict(toolchain or {}),
        "completed_at": _now_utc(),
    }
    return write_cache_entry(root, component, fingerprint, record)


def cache_hit_entry(root: Path, component: str, fingerprint: str) -> dict[str, Any] | None:
    """Return a usable cache record for a hit, or ``None`` on a miss.

    A hit requires a PASS record with the exact fingerprint and a compatible
    cache schema.  Anything else is a miss and forces a re-run.
    """
    entry = read_cache_entry(root, component, fingerprint)
    if entry is None:
        return None
    if entry.get("cache_schema_version") != FINGERPRINT_SCHEMA_VERSION:
        return None
    return entry


def missing_required_artifacts(root: Path, definition: Mapping[str, Any]) -> list[str]:
    """Return required output artifacts that are absent from the working tree.

    A cache hit must never hide a missing artifact that a later gate depends
    on.  Any absent artifact turns the hit into a miss so the gate re-runs
    and regenerates it.
    """
    missing = []
    for artifact in definition.get("required_artifacts", []):
        if not (root / artifact).is_file():
            missing.append(artifact)
    return missing


def cache_summary(components: Mapping[str, Mapping[str, Any]]) -> dict[str, Any]:
    """Aggregate cache observability across resolved components.

    ``hits`` counts CACHED_PASS gates, ``misses`` (== ``executed``) counts
    gates whose commands actually ran, and ``saved_ms`` is the wall-clock time
    the hits avoided re-running.
    """
    hits = 0
    misses = 0
    saved_ms = 0
    gates: list[dict[str, Any]] = []
    for name in sorted(components):
        result = components[name]
        if result.get("cache_hit"):
            original = int(result.get("original_duration_ms") or 0)
            hits += 1
            saved_ms += original
            gates.append({"gate": name, "result": "HIT", "duration_ms": original})
        elif result.get("status") in {"PASS", "FAIL", "TIMEOUT"}:
            misses += 1
            gates.append(
                {"gate": name, "result": "MISS", "duration_ms": int(result.get("duration_ms") or 0)}
            )
    return {
        "hits": hits,
        "misses": misses,
        "executed": misses,
        "saved_ms": saved_ms,
        "gates": gates,
    }


def format_cache_summary(summary: Mapping[str, Any]) -> str:
    """Format the human-readable VERIFY CACHE block for terminal output."""
    lines = ["VERIFY CACHE", ""]
    lines.append(f"hits={summary.get('hits', 0)}")
    lines.append(f"misses={summary.get('misses', 0)}")
    lines.append(f"executed={summary.get('executed', 0)}")
    lines.append(f"saved_ms={summary.get('saved_ms', 0)}")
    lines.append("")
    for gate in summary.get("gates", []):
        duration_s = int(gate.get("duration_ms", 0)) / 1000.0
        if gate.get("result") == "HIT":
            lines.append(f"{gate['gate']:<18} HIT   saved {duration_s:.1f}s")
        else:
            lines.append(f"{gate['gate']:<18} MISS  {duration_s:.1f}s")
    return "\n".join(lines)

