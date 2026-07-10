#!/usr/bin/env python3
"""Helpers for persisting Playwright storage_state() safely.

These helpers are shared by the login flow and the persistent worker so we do
not accidentally overwrite a good Google session with a logged-out snapshot.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any


def load_storage_file(path: str) -> dict[str, Any] | None:
    """Load a storage_state JSON file, returning None on any error."""
    p = Path(path)
    if not p.exists():
        return None
    try:
        return json.loads(p.read_text())
    except Exception:
        return None


def storage_looks_usable(storage: dict[str, Any] | None) -> bool:
    """Best-effort validation for a Google session snapshot.

    We keep this intentionally conservative: if the snapshot has no cookies,
    it is not worth promoting over an existing one.
    """
    if not storage:
        return False
    cookies = storage.get("cookies") or []
    if not cookies:
        return False
    origins = storage.get("origins") or []
    return bool(origins)


def choose_storage_candidate(*paths: str) -> tuple[str | None, dict[str, Any] | None]:
    """Pick the first usable storage snapshot from the provided paths."""
    for path in paths:
        storage = load_storage_file(path)
        if storage_looks_usable(storage):
            return path, storage
    return None, None


def _atomic_write_text(path: str, payload: str) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = target.with_name(target.name + ".tmp")
    tmp_path.write_text(payload)
    os.replace(tmp_path, target)


def save_storage_snapshot(path: str, storage: dict[str, Any], backup_path: str | None = None) -> None:
    """Atomically persist storage_state() and optionally refresh a backup.

    If a target file already exists, keep the old contents in backup_path before
    replacing it.
    """
    payload = json.dumps(storage, indent=2)
    target = Path(path)
    if backup_path and target.exists():
        try:
            Path(backup_path).parent.mkdir(parents=True, exist_ok=True)
            Path(backup_path).write_text(target.read_text())
        except Exception:
            # Backup is best-effort; the primary write still proceeds.
            pass
    _atomic_write_text(path, payload)

