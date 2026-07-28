#!/usr/bin/env python3
"""Run a fixture-driven YouTube stock pipeline request.

The fixture owns all project data.  This module owns only validation,
submission, polling, and count assertions.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

ENDPOINT = "/api/stock-pipeline/run"
TERMINAL_STATES = {"SUCCEEDED", "COMPLETED", "FAILED", "CANCELLED", "DEAD_LETTERED"}


def load_fixture(path: Path) -> dict[str, Any]:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot load fixture {path}: {exc}") from exc
    if not isinstance(data, dict):
        raise ValueError("fixture root must be an object")
    return data


def validate_fixture(fixture: dict[str, Any]) -> None:
    if fixture.get("schema_version") != 1:
        raise ValueError("schema_version must be 1")
    if fixture.get("endpoint") != ENDPOINT:
        raise ValueError(f"endpoint must be {ENDPOINT}")
    requests = fixture.get("requests")
    if not isinstance(requests, list) or not requests:
        raise ValueError("requests must be a non-empty array")
    for index, request in enumerate(requests):
        if not isinstance(request, dict):
            raise ValueError(f"requests[{index}] must be an object")
        source_url = request.get("source_url")
        if not isinstance(source_url, str) or not source_url.startswith("https://www.youtube.com/"):
            raise ValueError(f"requests[{index}].source_url must be a public YouTube URL")
        clips = request.get("clips")
        if not isinstance(clips, list) or not clips:
            raise ValueError(f"requests[{index}].clips must be non-empty")
        if len(clips) > 100:
            raise ValueError(f"requests[{index}] exceeds the API clip limit")
        for clip_index, clip in enumerate(clips):
            if not isinstance(clip, dict):
                raise ValueError(f"requests[{index}].clips[{clip_index}] must be an object")
            start = clip.get("start_sec")
            end = clip.get("end_sec")
            if not isinstance(start, (int, float)) or not isinstance(end, (int, float)) or end <= start:
                raise ValueError(f"requests[{index}].clips[{clip_index}] has invalid timestamps")
            if end - start < 3 or end - start > 30:
                raise ValueError(f"requests[{index}].clips[{clip_index}] must be 3-30 seconds")
            if "/" in str(clip.get("folder_name", "")) or "/" in str(clip.get("subfolder", "")):
                raise ValueError("fixture cannot encode Drive folder paths")


def make_payload(fixture: dict[str, Any], request: dict[str, Any], folder_id: str) -> dict[str, Any]:
    defaults = fixture.get("defaults", {})
    if not isinstance(defaults, dict):
        raise ValueError("defaults must be an object")
    clips = []
    for clip in request["clips"]:
        item = dict(clip)
        item["url"] = request["source_url"]
        clips.append(item)
    payload = {
        **defaults,
        "direct_urls": [request["source_url"]],
        "clips": clips,
        "folder_name": request["destination_folder_name"],
        "drive_folder_id": folder_id,
        "metadata": request.get("metadata", {}),
        "async": True,
    }
    payload.pop("subfolder", None)
    return payload


def request_json(base_url: str, token: str, method: str, path: str, body: dict[str, Any] | None = None) -> tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    headers = {"X-Velox-Admin-Token": token, "Content-Type": "application/json"}
    req = urllib.request.Request(f"{base_url.rstrip('/')}{path}", data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return response.status, json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            parsed = {"error": raw}
        return exc.code, parsed


def poll_job(base_url: str, token: str, job_id: str, timeout: int, interval: int) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status_code, body = request_json(base_url, token, "GET", f"/api/jobs/{job_id}/full")
        if status_code >= 400:
            raise RuntimeError(f"job poll returned HTTP {status_code}")
        state = str(body.get("status", body.get("state", ""))).upper()
        if state in TERMINAL_STATES:
            return body
        time.sleep(interval)
    raise TimeoutError(f"job {job_id} did not reach a terminal state within {timeout}s")


def assert_counts(expected: dict[str, Any], result: dict[str, Any], label: str) -> None:
    counts = result.get("counts", result.get("summary", {}))
    if not isinstance(counts, dict):
        counts = {}
    mismatches = []
    for key, value in expected.items():
        if counts.get(key) != value:
            mismatches.append(f"{key}: expected {value}, got {counts.get(key)!r}")
    if mismatches:
        raise RuntimeError(f"{label}: count verification failed ({'; '.join(mismatches)})")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Run a fixture-driven YouTube stock pipeline request")
    parser.add_argument("fixture", type=Path)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--base-url", default=os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000"))
    parser.add_argument("--drive-folder-id", default=os.environ.get("STOCK_DRIVE_FOLDER_ID", ""))
    parser.add_argument("--poll-timeout", type=int, default=900)
    parser.add_argument("--poll-interval", type=int, default=5)
    args = parser.parse_args(argv)

    try:
        fixture = load_fixture(args.fixture)
        validate_fixture(fixture)
        folder_id = args.drive_folder_id
        if not args.dry_run and not folder_id:
            raise ValueError("STOCK_DRIVE_FOLDER_ID is required for a live run")
        token = os.environ.get("VELOX_ADMIN_TOKEN", "")
        if not args.dry_run and not token:
            raise ValueError("VELOX_ADMIN_TOKEN is required for a live run")

        for request in fixture["requests"]:
            payload = make_payload(fixture, request, folder_id)
            label = request["name"]
            if args.dry_run:
                print(f"{label}: clips={len(payload['clips'])} source={request['source_url']}")
                continue
            status_code, response = request_json(args.base_url, token, "POST", ENDPOINT, payload)
            if status_code >= 300 or not response.get("job_id"):
                raise RuntimeError(f"{label}: submission failed with HTTP {status_code}")
            result = poll_job(args.base_url, token, response["job_id"], args.poll_timeout, args.poll_interval)
            if str(result.get("status", result.get("state", ""))).upper() not in {"SUCCEEDED", "COMPLETED"}:
                raise RuntimeError(f"{label}: job did not succeed")
            assert_counts(request.get("expected_counts", {}), result, label)
            print(f"{label}: succeeded")
    except (RuntimeError, TimeoutError, ValueError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
