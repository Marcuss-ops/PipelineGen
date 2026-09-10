#!/usr/bin/env python3
"""Shared runtime mechanics for the CI verification runners.

This module deliberately contains no registry or domain policy.  It owns only
bounded subprocess execution and deterministic report plumbing so the component
and pipeline runners can keep their public contracts while sharing the risky
process/timeout/file-writing mechanics.
"""

from __future__ import annotations

import json
import os
import signal
import subprocess
import tempfile
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping, Sequence


@dataclass(frozen=True)
class BoundedProcessResult:
    """Raw result of a command executed in an isolated process group."""

    status: str
    exit_code: int | None
    duration_ms: int
    stdout: str = ""
    stderr: str = ""
    timed_out: bool = False


def text_output(value: str | bytes | None) -> str:
    """Normalize subprocess output across text and timeout exception paths."""
    if value is None:
        return ""
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="replace")
    return value


def run_bounded_process(
    argv: Sequence[str], timeout_seconds: float, cwd: Path
) -> BoundedProcessResult:
    """Run a command in its own process group with bounded timeout cleanup."""
    started = time.monotonic()
    process = subprocess.Popen(
        list(argv),
        cwd=str(cwd),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        stdout, stderr = process.communicate(timeout=max(0.001, timeout_seconds))
    except subprocess.TimeoutExpired as exc:
        try:
            os.killpg(process.pid, signal.SIGTERM)
            process.communicate(timeout=1)
        except (ProcessLookupError, subprocess.TimeoutExpired):
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate()
        return BoundedProcessResult(
            status="TIMEOUT",
            exit_code=None,
            duration_ms=int((time.monotonic() - started) * 1000),
            stdout=text_output(exc.stdout),
            stderr=text_output(exc.stderr),
            timed_out=True,
        )

    return BoundedProcessResult(
        status="PASS" if process.returncode == 0 else "FAIL",
        exit_code=process.returncode,
        duration_ms=int((time.monotonic() - started) * 1000),
        stdout=stdout,
        stderr=stderr,
    )


def now_utc() -> str:
    """Return the canonical machine-report timestamp."""
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def git_sha(root: Path) -> str | None:
    """Return HEAD without making Git a verification dependency."""
    try:
        completed = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=str(root),
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
        )
    except OSError:
        return None
    value = completed.stdout.strip()
    return value if completed.returncode == 0 and value else None


def write_json_report(path: Path, report: Mapping[str, Any]) -> None:
    """Write a JSON report atomically, creating its parent directory."""
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=str(path.parent)
    )
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(report, handle, indent=2, ensure_ascii=False)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
