"""Diagnostics: timestamps, structured logging, forensic screenshots.

Migrated from slide_worker.py in Commit 1. The four module-level
helpers (`_iso8601_utc_ms`, `_log`, `_log_diag`, `_screenshot_on_failure`)
are kept as-is so existing `from X import _log` call sites keep
working. The `DiagnosticsSink` class is forward-compat scaffolding:
no caller uses it in Commit 1, but Commit 4 (GenerationRunner)
delivers a DiagnosticsSink instance to the orchestrator instead of
relying on module-level state — the class is committed now so the
broad blast radius of the orchestrator refactor doesn't have to ship
a new public surface simultaneously.

Per godlike/07 fail-closed: diagnostics emission is best-effort.
Errors during write are logged to stderr but never crash the
request pipeline. The pipeline's typed-error path is the source of
truth for failure visibility — the diagnostics stream is forensic,
not authoritative.
"""

from __future__ import annotations

import datetime as _dt
import json
import os
import sys
from typing import Optional

from .config import DIAG_FILE, P2_DIAGNOSTICS_DIR


def _iso8601_utc_ms() -> str:
    """Return current UTC timestamp with millisecond precision.

    Format: YYYY-MM-DDTHH:MM:SS.sssZ (Go-friendly, JSON-friendly,
    grep-friendly). Trailing-Z indicates UTC unambiguously; naive
    parsers that conflate local+UTC cannot mistake the marker.
    """
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"


# ── Helpers ────────────────────────────────────────────────────────────────

def _log(msg: str) -> None:
    """Log a free-form stderr line with timestamp prefix.

    Direct stderr write (not stdout) so the JSONL response stream
    on stdout stays clean. flush=True so tailers see live output.
    """
    print(f"[{_iso8601_utc_ms()}] {msg}", file=sys.stderr, flush=True)


def _log_diag(request_id: str, profile_id: int, phase: str, **kwargs) -> None:
    """Append one JSONL line to $P2_DIAGNOSTICS_DIR/requests.jsonl.

    No-op when P2_DIAGNOSTICS_DIR is unset. Errors during emission are
    logged to stderr but do NOT crash the request path (godlike/07
    fail-closed observability — diagnostics are best-effort; the
    extraction pipeline is the source of truth).
    """
    if not DIAG_FILE:
        return
    payload = {
        "ts": _iso8601_utc_ms(),
        "request_id": request_id,
        "profile_id": profile_id,
        "phase": phase,
    }
    payload.update(kwargs)
    try:
        line = json.dumps(payload, ensure_ascii=False) + "\n"
        # Append mode 'a' accumulates across worker restarts (forensics
        # continuity). The flush() makes the line visible immediately
        # so a `while true; do tail -F` operator sees phases live.
        with open(DIAG_FILE, "a", encoding="utf-8") as f:
            f.write(line)
            f.flush()
    except Exception as e:
        _log(f"[diag] failed to write diagnostic line for phase={phase}: {e}")


def _screenshot_on_failure(page, label: str) -> Optional[str]:
    """Save a screenshot under /tmp/slide_worker_diagnostics/ if writable.

    Per P2 (July 2026): invoked BEFORE _fresh_page in error paths so the
    page-reset doesn't erase the forensic snapshot. Returns the absolute
    path on success, None on write failure or absent page.
    """
    if page is None:
        return None
    try:
        target_dir = P2_DIAGNOSTICS_DIR if P2_DIAGNOSTICS_DIR else "/tmp/slide_worker_diagnostics"
        os.makedirs(target_dir, exist_ok=True)
        # Filename: slide_worker_<label>_<ts>.png for unambiguous sort.
        ts_short = _iso8601_utc_ms().replace(":", "").replace("-", "").replace(".", "").replace("Z", "")
        out_path = os.path.join(target_dir, f"slide_worker_{label}_{ts_short}.png")
        page.screenshot(path=out_path)
        return out_path
    except Exception as e:
        _log(f"[screenshot] failed to save {label} screenshot: {e}")
        return None


class DiagnosticsSink:
    """Forward-compat class facade for the module-level diagnostics helpers.

    Commit 4 (GenerationRunner) instantiates one DiagnosticsSink per
    request and routes log/phase/screenshot through it. The
    module-level helpers remain for backward compatibility with the
    pre-Commit-1 call sites; the sink methods delegate to them so
    there is exactly one underlying writer implementation.

    godlike/06 SSOT: DiagnosticsSink is the SINGLE owner of the
    diagnostics emission path scoped to one logical request. Future
    commit's orchestrator constructs one with (request_id,
    profile_id, page) and the 7 step methods write through it.
    """

    __slots__ = ("request_id", "profile_id")

    def __init__(self, request_id: str, profile_id: int) -> None:
        self.request_id = request_id
        self.profile_id = profile_id

    def log(self, message: str) -> None:
        """Forward a free-form stderr line, prefixed with request_id."""
        _log(f"[profile-{self.profile_id}][{self.request_id}] {message}")

    def phase(self, phase: str, **data) -> None:
        """Emit one P2 diagnostics phase-event for this request."""
        _log_diag(self.request_id, self.profile_id, phase, **data)

    def screenshot(self, page, label: str) -> Optional[str]:
        """Capture a forensic screenshot under the canonical dir."""
        return _screenshot_on_failure(page, label)
