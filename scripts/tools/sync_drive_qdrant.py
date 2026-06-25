#!/usr/bin/env python3
"""
sync_drive_qdrant.py — Pure HTTP client for the Go Qdrant ingestion gateway.

QDRANT-001 (June 2026) closure: this script is the last legacy direct writer
of Qdrant points and SQLite rows. It now POSTs to the canonical Go server,
which is the sole writer of media.db.sqlite and the Qdrant collection.

What was removed (QDRANT-001 DoD):
  - import sqlite3, google.oauth2.credentials, googleapiclient, qdrant_client
  - from sentence_transformers import SentenceTransformer
  - hardcoded ROOT path (/home/pierone/...), DB_PATH, QDRANT_URL,
    QDRANT_COLLECTION, Drive folder ID, OAuth client_id, Drive token
  - dual write to Qdrant + SQLite (INSERT OR REPLACE)
  - direct PUT to collections/{}/points

What was retained (intentionally):
  - the "--folder-id" CLI flag now resolves from a flag
    OR from VELOX_DRIVE_FOLDER_ID env var (no hardcoded ID).
  - single HTTP call to the Go server with Idempotency-Key.

Configuration sources (NEVER hardcoded):
  VELOX_BROKER_URL        required  e.g. http://127.0.0.1:8080
  VELOX_WORKER_TOKEN      required  service-to-service token
  VELOX_DRIVE_FOLDER_ID   optional  Drive root folder
                                        (overridden by --folder-id)
  VELOX_HTTP_TIMEOUT_S    optional  per-request timeout seconds
                                        (default 30)
  VELOX_HTTP_MAX_RETRIES  optional  retry budget (default 5)

Behavior:
  - POST /internal/v1/media/sync-drive-folder with body
       {"drive_folder_id": "...", "source": "drive",
        "media_type": "clip", "name": "..."}
  - Sends Idempotency-Key: drive:<folder-id>:folder-sync
    (stable per-folder replay dedup).
  - Retries only on timeout, 429, 5xx (jittered exponential backoff).
  - Never retries 4xx validation errors.
  - Logs asset_id / job_id / request correlation id — never local paths,
    tokens, or vector data.

Replaces: the previous impl embedded an E5 model + Qdrant client +
SQLite handle. Closing QDRANT-001 = making Go the sole writer.
"""
import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from typing import Optional

# Retry policy: only retryable errors.
RETRYABLE_HTTP = {408, 429, 500, 502, 503, 504}
DEFAULT_TIMEOUT_S = 30
DEFAULT_MAX_RETRIES = 5


def env_required(name: str) -> str:
    val = os.environ.get(name, "").strip()
    if not val:
        sys.stderr.write(
            f"error: env var {name} is required "
            "(QDRANT-001 forbids hardcoding server URLs/tokens)\n"
        )
        sys.exit(2)
    return val


def env_optional(name: str, default: str) -> str:
    return os.environ.get(name, "").strip() or default


def build_request(
    url: str,
    method: str,
    token: str,
    payload: Optional[dict] = None,
    idempotency_key: Optional[str] = None,
) -> urllib.request.Request:
    data = None
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": "application/json",
        "User-Agent": "pipelinegen-sync/1.0",
    }
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if idempotency_key:
        # RFC-style Idempotency-Key (Go mirrors/sequences this header).
        headers["Idempotency-Key"] = idempotency_key
    return urllib.request.Request(url, data=data, headers=headers, method=method)


def http_call(
    url: str,
    method: str,
    token: str,
    payload: Optional[dict] = None,
    idempotency_key: Optional[str] = None,
    timeout_s: int = DEFAULT_TIMEOUT_S,
    max_retries: int = DEFAULT_MAX_RETRIES,
) -> dict:
    """One-shot HTTP call with retry for retryable errors only."""
    last_err: Optional[str] = None
    last_status: int = 0
    for attempt in range(1, max_retries + 1):
        try:
            req = build_request(url, method, token, payload, idempotency_key)
            with urllib.request.urlopen(req, timeout=timeout_s) as resp:
                raw = resp.read().decode("utf-8") or "{}"
                return {
                    "status": resp.status,
                    "body": json.loads(raw),
                    "retryable": False,
                    "attempts": attempt,
                }
        except urllib.error.HTTPError as e:
            last_status = e.code
            last_err_body = e.read().decode("utf-8", errors="ignore") if e.fp else ""
            try:
                last_err = json.loads(last_err_body)
            except Exception:
                last_err = last_err_body
            if e.code in RETRYABLE_HTTP and attempt < max_retries:
                backoff = min(2 ** attempt, 30)
                sys.stderr.write(
                    f"retry {attempt}/{max_retries}: HTTP {e.code}, "
                    f"sleeping {backoff}s\n"
                )
                time.sleep(backoff)
                continue
            return {
                "status": e.code,
                "body": last_err,
                "retryable": False,
                "attempts": attempt,
            }
        except (urllib.error.URLError, TimeoutError, ConnectionError, OSError) as e:
            last_err = str(e)
            if attempt < max_retries:
                backoff = min(2 ** attempt, 30)
                sys.stderr.write(
                    f"retry {attempt}/{max_retries}: network error "
                    f"{type(e).__name__}: {e}, sleeping {backoff}s\n"
                )
                time.sleep(backoff)
                continue
            return {
                "status": last_status,
                "body": last_err,
                "retryable": False,
                "attempts": attempt,
            }
    return {
        "status": last_status,
        "body": last_err or "max retries exhausted",
        "retryable": False,
        "attempts": max_retries,
    }


def post_folder_sync(
    base_url: str,
    token: str,
    drive_folder_id: str,
    source: str = "drive",
    media_type: str = "clip",
    name: Optional[str] = None,
    timeout_s: int = DEFAULT_TIMEOUT_S,
    max_retries: int = DEFAULT_MAX_RETRIES,
) -> dict:
    """Single folder-level sync — the Go server recurses and queues indexing."""
    url = f"{base_url.rstrip('/')}/internal/v1/media/sync-drive-folder"
    payload: dict = {
        "drive_folder_id": drive_folder_id,
        "source": source,
        "media_type": media_type,
    }
    if name:
        payload["name"] = name
    idem = f"drive:{drive_folder_id}:folder-sync"
    return http_call(
        url=url,
        method="POST",
        token=token,
        payload=payload,
        idempotency_key=idem,
        timeout_s=timeout_s,
        max_retries=max_retries,
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Pure-HTTP Go Qdrant ingestion sync "
        "(QDRANT-001 — no direct SQLite/Qdrant access).",
    )
    parser.add_argument(
        "--folder-id",
        default=env_optional("VELOX_DRIVE_FOLDER_ID", ""),
        help="Google Drive folder ID (or set VELOX_DRIVE_FOLDER_ID).",
    )
    parser.add_argument(
        "--source",
        default="drive",
        help="Provider source tag (default 'drive').",
    )
    parser.add_argument(
        "--media-type",
        default="clip",
        help="Media type tag (default 'clip').",
    )
    parser.add_argument(
        "--name",
        default="",
        help="Human-readable name for the folder (optional).",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=int(env_optional("VELOX_HTTP_TIMEOUT_S", str(DEFAULT_TIMEOUT_S))),
        help="Per-request timeout seconds.",
    )
    parser.add_argument(
        "--max-retries",
        type=int,
        default=int(env_optional(
            "VELOX_HTTP_MAX_RETRIES", str(DEFAULT_MAX_RETRIES),
        )),
        help="Retry budget (only on timeout/429/5xx).",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    if not args.folder_id:
        sys.stderr.write(
            "error: --folder-id (or VELOX_DRIVE_FOLDER_ID) is required\n"
        )
        return 2

    base_url = env_required("VELOX_BROKER_URL")
    token = env_required("VELOX_WORKER_TOKEN")

    sys.stderr.write(
        f"folder-sync -> {base_url}/internal/v1/media/sync-drive-folder "
        f"(folder_id={args.folder_id})\n"
    )
    res = post_folder_sync(
        base_url=base_url,
        token=token,
        drive_folder_id=args.folder_id,
        source=args.source,
        media_type=args.media_type,
        name=args.name or None,
        timeout_s=args.timeout,
        max_retries=args.max_retries,
    )

    sys.stderr.write(
        f"server response: status={res.get('status')} "
        f"attempts={res.get('attempts', 1)}\n"
    )
    sys.stdout.write(json.dumps(res, indent=2, ensure_ascii=False))
    sys.stdout.write("\n")
    status = res.get("status", 0)
    return 0 if 200 <= status < 300 else 1


if __name__ == "__main__":
    sys.exit(main())
