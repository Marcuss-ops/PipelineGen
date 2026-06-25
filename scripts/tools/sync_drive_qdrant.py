#!/usr/bin/env python3
"""Queue a Drive-folder sync through PipelineGen's internal HTTP API."""

import argparse
import json
import os
import socket
import sys
import time
import urllib.error
import urllib.request


DEFAULT_BASE_URL = "http://127.0.0.1:8080"
DEFAULT_TIMEOUT_SECONDS = 30.0
DEFAULT_RETRIES = 4
IDEMPOTENCY_HEADER = "Idempotency-Key"
REQUEST_ID_HEADER = "X-Request-ID"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Dispatch POST /internal/v1/media/sync-drive-folder."
    )
    parser.add_argument(
        "--base-url",
        default=os.getenv("PIPELINEGEN_BASE_URL", DEFAULT_BASE_URL),
        help="PipelineGen server base URL (env: PIPELINEGEN_BASE_URL)",
    )
    parser.add_argument(
        "--token",
        default=os.getenv("VELOX_WORKER_TOKEN", ""),
        help="Worker/service bearer token (env: VELOX_WORKER_TOKEN)",
    )
    parser.add_argument(
        "--folder-id",
        default=os.getenv("DRIVE_FOLDER_ID", ""),
        help="Google Drive folder ID to sync (env: DRIVE_FOLDER_ID)",
    )
    parser.add_argument(
        "--source",
        default=os.getenv("SYNC_SOURCE", "drive"),
        help="Source label for the sync job (env: SYNC_SOURCE)",
    )
    parser.add_argument(
        "--name",
        default=os.getenv("SYNC_NAME", ""),
        help="Optional human-readable folder name (env: SYNC_NAME)",
    )
    parser.add_argument(
        "--media-type",
        default=os.getenv("SYNC_MEDIA_TYPE", "clip"),
        help="Media type label for the sync job (env: SYNC_MEDIA_TYPE)",
    )
    parser.add_argument(
        "--idempotency-key",
        default=os.getenv("IDEMPOTENCY_KEY", ""),
        help="Explicit Idempotency-Key override (env: IDEMPOTENCY_KEY)",
    )
    parser.add_argument(
        "--timeout-seconds",
        type=float,
        default=DEFAULT_TIMEOUT_SECONDS,
        help="Per-request timeout in seconds",
    )
    parser.add_argument(
        "--retries",
        type=int,
        default=DEFAULT_RETRIES,
        help="Retries for timeout, 429, and 5xx responses",
    )
    return parser.parse_args()


def build_idempotency_key(args: argparse.Namespace) -> str:
    if args.idempotency_key.strip():
        return args.idempotency_key.strip()
    source = args.source.strip() or "drive"
    folder_id = args.folder_id.strip()
    return f"{source}:{folder_id}:sync-drive-folder"


def should_retry(status_code: int | None, err: Exception | None) -> bool:
    if status_code is not None:
        return status_code == 429 or status_code >= 500
    return isinstance(err, (TimeoutError, urllib.error.URLError, socket.timeout))


def dispatch_sync(args: argparse.Namespace, idem_key: str) -> dict:
    url = args.base_url.rstrip("/") + "/internal/v1/media/sync-drive-folder"
    payload = {
        "drive_folder_id": args.folder_id.strip(),
        "source": args.source.strip() or "drive",
        "name": args.name.strip(),
        "media_type": args.media_type.strip() or "clip",
    }
    body = json.dumps(payload).encode("utf-8")
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {args.token.strip()}",
        IDEMPOTENCY_HEADER: idem_key,
    }

    attempts = max(1, args.retries + 1)
    last_error = None
    for attempt in range(1, attempts + 1):
        req = urllib.request.Request(url, data=body, headers=headers, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=args.timeout_seconds) as resp:
                raw = resp.read().decode("utf-8")
                data = json.loads(raw) if raw else {}
                data["http_status"] = resp.status
                data["request_id"] = resp.headers.get(REQUEST_ID_HEADER, "")
                return data
        except urllib.error.HTTPError as err:
            raw = err.read().decode("utf-8", errors="replace")
            status_code = err.code
            last_error = RuntimeError(f"HTTP {status_code}: {raw}")
            if attempt == attempts or not should_retry(status_code, None):
                raise last_error
        except (urllib.error.URLError, TimeoutError, socket.timeout) as err:
            last_error = err
            if attempt == attempts or not should_retry(None, err):
                raise
        time.sleep(min(2 ** (attempt - 1), 8))

    raise RuntimeError(f"sync dispatch failed: {last_error}")


def main() -> int:
    args = parse_args()
    if not args.folder_id.strip():
        print("error: --folder-id (or DRIVE_FOLDER_ID) is required", file=sys.stderr)
        return 2
    if not args.token.strip():
        print("error: --token (or VELOX_WORKER_TOKEN) is required", file=sys.stderr)
        return 2

    idem_key = build_idempotency_key(args)
    result = dispatch_sync(args, idem_key)
    print(
        json.dumps(
            {
                "ok": bool(result.get("ok")),
                "job_id": result.get("job_id", ""),
                "drive_folder_id": result.get("drive_folder_id", args.folder_id.strip()),
                "idempotency_key": idem_key,
                "request_id": result.get("request_id", ""),
                "http_status": result.get("http_status", 0),
                "message": result.get("message", ""),
            },
            indent=2,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
