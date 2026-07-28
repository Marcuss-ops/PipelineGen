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
COUNT_KEYS = (
    "requested_video_count", "discovered_video_count", "selected_video_count",
    "downloaded_video_count", "processed_video_count", "planned_clip_count",
    "created_clip_count", "published_clip_count", "persisted_clip_count",
    "indexed_clip_count", "failed_video_count", "failed_clip_count",
)


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
        source_urls = request.get("source_urls")
        if source_urls is None:
            source_urls = [request.get("source_url")]
        if not isinstance(source_urls, list) or not source_urls:
            raise ValueError(f"requests[{index}].source_urls must be non-empty")
        if any(not isinstance(url, str) or not url.startswith("https://www.youtube.com/") for url in source_urls):
            raise ValueError(f"requests[{index}].source_urls must contain public YouTube URLs")
        clips = request.get("clips")
        clips_per_source = request.get("clips_per_source", 0)
        if clips is None and (not isinstance(clips_per_source, int) or clips_per_source <= 0):
            raise ValueError(f"requests[{index}] needs clips or a positive clips_per_source")
        if clips is not None and (not isinstance(clips, list) or not clips):
            raise ValueError(f"requests[{index}].clips must be non-empty when provided")
        clip_count = len(clips) if clips is not None else clips_per_source * len(source_urls)
        if clip_count > 1000:
            raise ValueError(f"requests[{index}] exceeds the fixture clip limit")
        for clip_index, clip in enumerate(clips or []):
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
    source_urls = request.get("source_urls") or [request["source_url"]]
    clips = []
    if request.get("clips") is not None:
        for clip in request["clips"]:
            item = dict(clip)
            item["url"] = source_urls[0]
            clips.append(item)
    else:
        for source_index, source_url in enumerate(source_urls):
            for clip_index in range(request["clips_per_source"]):
                start = 10 + clip_index * 40
                clips.append({
                    "title": f"source-{source_index + 1:02d}-clip-{clip_index + 1:03d}",
                    "start_sec": start,
                    "end_sec": start + 4,
                    "url": source_url,
                })
    payload = {
        **defaults,
        "direct_urls": source_urls,
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


def add_counts(total: dict[str, int], result: dict[str, Any]) -> None:
    counts = result.get("counts", result.get("summary", {}))
    if not isinstance(counts, dict):
        raise RuntimeError("job result does not contain structured counts")
    for key in COUNT_KEYS:
        value = counts.get(key)
        if not isinstance(value, int):
            raise RuntimeError(f"job result count {key} is missing or not an integer")
        total[key] += value


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

        aggregate = {key: 0 for key in COUNT_KEYS}
        for request in fixture["requests"]:
            payload = make_payload(fixture, request, folder_id)
            label = request["name"]
            if args.dry_run:
                print(f"{label}: clips={len(payload['clips'])} sources={len(payload['direct_urls'])}")
                continue
            status_code, response = request_json(args.base_url, token, "POST", ENDPOINT, payload)
            if status_code >= 300 or not response.get("job_id"):
                raise RuntimeError(f"{label}: submission failed with HTTP {status_code}")
            result = poll_job(args.base_url, token, response["job_id"], args.poll_timeout, args.poll_interval)
            if str(result.get("status", result.get("state", ""))).upper() not in {"SUCCEEDED", "COMPLETED"}:
                raise RuntimeError(f"{label}: job did not succeed")
            assert_counts(request.get("expected_counts", {}), result, label)
            add_counts(aggregate, result)
            print(f"{label}: succeeded")
        if not args.dry_run:
            assert_counts(fixture.get("expected_run_counts", {}), {"counts": aggregate}, "fixture aggregate")
    except (RuntimeError, TimeoutError, ValueError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
