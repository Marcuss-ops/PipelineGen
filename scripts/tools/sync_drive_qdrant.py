#!/usr/bin/env python3
# sync_drive_qdrant.py — QDRANT-001 thin HTTP client.
#
# QDRANT-001 (June 2026, OWNERSHIP / API GATEWAY AND LEGACY REMOVAL):
# this script is a THIN HTTP CLIENT. The Go PipelineGen DataServer is
# the sole canonical owner of SQLite (media_assets) and Qdrant
# (media_assets collection). Python/C++/frontend components must not
# write to either store directly; they MUST call the Go HTTP API.
#
# The script triggers a recursive Drive-folder sync by calling the
# canonical handler:
#
#     POST {API_BASE}/api/media/sync-drive-folder
#
# implemented in ``internal/api/assets/storage/sync_drive_folder.go``.
# The handler enqueues an async ``drive.folder.sync`` job whose
# execution lives in ``internal/application/assets/catalogsync`` —
# SQLite upserts + Qdrant upserts + embedding generation all happen
# inside the Go process. This script is responsible for NOTHING
# besides shaping the request, polling the returned ``job_id``, and
# surfacing the result.
#
# Configuration via env vars (no hardcoded defaults for sensitive
# material — see QDRANT-001 §"Acceptance Criteria"):
#
#   VELOX_BROKER_URL   e.g. http://127.0.0.1:8080  (or set API_BASE)
#   VELOX_WORKER_TOKEN  worker bearer token       (or set VELOX_ADMIN_TOKEN)
#
# Required CLI flag:
#
#   --folder-id        Google Drive folder ID to sync
#
# Optional CLI flags:
#
#   --source           drive | youtube | stock | artlist  (default: drive)
#   --name             human-readable name               (default: folder ID)
#   --media-type       clip | video | stock               (default: clip)
#   --wait             poll job_id until terminal status (default: false)
#   --timeout          --wait deadline in seconds         (default: 600)
#
# Exit codes:
#
#   0  job submitted (or succeeded when --wait)
#   2  configuration error (missing flag / env var)
#   3  authentication failure (HTTP 401/403)
#   4  job reached FAILED or CANCELLED terminal state (with --wait)
#   5  --wait timeout exceeded
#
# This file is stdlib-only; the canonical HTTP client is reused from
# ``scripts/velox_client.py`` (added to sys.path at import time).
"""
sync_drive_qdrant — QDRANT-001 thin HTTP client for Drive folder sync.
"""
from __future__ import annotations

import argparse
import os
import sys
import time
import urllib.error
from pathlib import Path

# ── Reuse the canonical HTTP client (velox_client.py lives in scripts/) ────────
# scripts/tools/<this>.py → add scripts/ to sys.path so we can import
# scripts/velox_client.py without hardcoding any path. The script's own
# location is derived from __file__ — never from a user-controlled env var.
_THIS_DIR = Path(__file__).resolve().parent          # .../scripts/tools
_VELOX_DIR = _THIS_DIR.parent                       # .../scripts
if str(_VELOX_DIR) not in sys.path:
    sys.path.insert(0, str(_VELOX_DIR))

from velox_client import (  # noqa: E402 — intentional sys.path bootstrap
    AuthError,
    BadRequestError,
    NotFoundError,
    ServerError,
    VeloxClient,
    VeloxError,
    is_retryable,
)

__all__ = ["main", "SyncFolderCLI"]


# ── Config helpers ────────────────────────────────────────────────────────────

def _resolve_base_url() -> str:
    """Resolve the API base URL from env vars. Required (no hardcoded default)."""
    base = os.environ.get("VELOX_BROKER_URL") or os.environ.get("API_BASE")
    if not base:
        raise ConfigError(
            "VELOX_BROKER_URL (or API_BASE) env var is required. "
            "See AGENTS.md §Port policy for the canonical server URL shape."
        )
    return base.rstrip("/")


def _resolve_token() -> str:
    """Resolve the bearer token. Worker token preferred; admin token is a fallback."""
    worker = os.environ.get("VELOX_WORKER_TOKEN")
    admin = os.environ.get("VELOX_ADMIN_TOKEN")
    if worker:
        return worker
    if admin:
        print(
            "warning: VELOX_ADMIN_TOKEN used as fallback (worker token preferred "
            "for non-admin callers — see scripts/velox_client.py docstring)",
            file=sys.stderr,
        )
        return admin
    raise ConfigError(
        "VELOX_WORKER_TOKEN (or VELOX_ADMIN_TOKEN) env var is required to call "
        "the canonical /api/media/sync-drive-folder endpoint."
    )


# ── Custom exceptions ─────────────────────────────────────────────────────────

class ConfigError(RuntimeError):
    """Raised when CLI args / env vars are invalid. Mapped to exit code 2."""


class JobFailedError(RuntimeError):
    """Raised when --wait observed a terminal FAILED/CANCELLED state."""


class JobTimeoutError(RuntimeError):
    """Raised when --wait exceeded its deadline."""


# ── CLI driver ─────────────────────────────────────────────────────────────────

class SyncFolderCLI:
    """Encapsulates the CLI contract so tests can drive it without argv parsing."""

    # Canonical HTTP endpoint (relative — base_url comes from VeloxClient).
    # The job-status poll path is owned by VeloxClient.get_job (which builds
    # ``api/jobs/{id}/full`` internally); do not duplicate it here.
    ENDPOINT = "/api/media/sync-drive-folder"
    POLL_INTERVAL_S = 2.0

    def __init__(self, client: VeloxClient) -> None:
        self.client = client

    def enqueue(
        self,
        folder_id: str,
        source: str = "drive",
        name: str = "",
        media_type: str = "clip",
    ) -> str:
        """POST /api/media/sync-drive-folder and return the declared job_id.

        `req_id` is set to the folder_id for natural idempotency: re-running
        the script with the same --folder-id returns the SAME job_id (no
        duplicate enqueue). This is the QDRANT-001 idempotency knob.
        """
        payload = {
            "drive_folder_id": folder_id,
            "source": source,
            "name": name,
            "media_type": media_type,
        }
        resp = self.client.submit_async(
            path=self.ENDPOINT,
            payload=payload,
            req_id=folder_id,
        )
        job_id = (resp or {}).get("job_id", "")
        if not job_id:
            raise ServerError(
                f"server returned 202 without job_id (response={resp!r})"
            )
        return job_id

    def poll_until_terminal(self, job_id: str, timeout_s: float) -> dict:
        deadline = time.monotonic() + timeout_s
        last: dict = {}
        while time.monotonic() < deadline:
            last = self.client.get_job(job_id)
            status = (last.get("status") or "").upper()
            if status in ("SUCCEEDED", "FAILED", "CANCELLED"):
                return last
            time.sleep(self.POLL_INTERVAL_S)
        raise JobTimeoutError(
            f"job {job_id} did not reach a terminal state within {timeout_s:.0f}s "
            f"(last status={last.get('status')!r})"
        )


# ── Entry point ───────────────────────────────────────────────────────────────

def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="sync_drive_qdrant",
        description=(
            "Trigger an async Drive folder sync via the Go canonical HTTP API. "
            "QDRANT-001: this script is a thin HTTP client and owns no state."
        ),
    )
    parser.add_argument(
        "--folder-id",
        required=True,
        help="Google Drive folder ID to sync (required).",
    )
    parser.add_argument(
        "--source",
        default="drive",
        choices=("drive", "youtube", "stock", "artlist"),
        help="logical source label written to media_assets (default: drive).",
    )
    parser.add_argument(
        "--name",
        default="",
        help="human-readable name; defaults to the folder ID.",
    )
    parser.add_argument(
        "--media-type",
        default="clip",
        choices=("clip", "video", "stock"),
        help="media_type label written to media_assets (default: clip).",
    )
    parser.add_argument(
        "--wait",
        action="store_true",
        help="block until the enqueued job reaches a terminal status.",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=600.0,
        help="--wait deadline in seconds (default: 600).",
    )
    args = parser.parse_args(argv)

    try:
        base_url = _resolve_base_url()
        token = _resolve_token()
    except ConfigError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    client = VeloxClient(base_url=base_url, token=token)
    cli = SyncFolderCLI(client)

    print(
        f"→ POST {base_url}{SyncFolderCLI.ENDPOINT}  "
        f"folder_id={args.folder_id}  source={args.source}  "
        f"media_type={args.media_type}"
    )

    # ── enqueue ────────────────────────────────────────────────────────────
    try:
        job_id = cli.enqueue(
            folder_id=args.folder_id,
            source=args.source,
            name=args.name,
            media_type=args.media_type,
        )
    except AuthError as exc:
        print(f"error: authentication rejected: {exc}", file=sys.stderr)
        return 3
    except (BadRequestError, NotFoundError) as exc:
        print(f"error: server rejected the request: {exc}", file=sys.stderr)
        return 2
    except ServerError as exc:
        # VeloxClient already retried; bubble up as transport failure.
        print(f"error: server unavailable after retries: {exc}", file=sys.stderr)
        return 2
    except VeloxError as exc:
        print(f"error: unexpected velox error: {exc}", file=sys.stderr)
        return 2
    except urllib.error.URLError as exc:
        print(f"error: network failure: {exc}", file=sys.stderr)
        return 2

    print(f"✓ enqueued job_id={job_id}")

    # ── optional: poll ─────────────────────────────────────────────────────
    if not args.wait:
        return 0

    try:
        final = cli.poll_until_terminal(job_id, args.timeout)
    except JobTimeoutError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 5
    except (AuthError, BadRequestError, NotFoundError, ServerError, VeloxError) as exc:
        print(f"error: poll failed: {exc}", file=sys.stderr)
        return 2

    status = (final.get("status") or "").upper()
    print(f"✓ job {job_id} finished: status={status}")
    if status != "SUCCEEDED":
        err = final.get("error") or "no error detail in response"
        print(f"  error_detail={err}", file=sys.stderr)
        return 4
    return 0


if __name__ == "__main__":
    sys.exit(main())
