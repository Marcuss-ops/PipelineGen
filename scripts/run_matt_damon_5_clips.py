#!/usr/bin/env python3
"""Submit and verify the five-clip reconstruction through PipelineGen HTTP."""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

from velox_client import VeloxClient


ROOT = Path(__file__).resolve().parents[1]
PAYLOAD = ROOT / "ops/jobs/matt_damon_5_clips_docs_true.generate.json"
# Operator-selected Drive parent for this reconstruction test. Keep this in
# sync with the checked-in payload so a plain test run cannot silently publish
# into a different Drive tree.
DEFAULT_FOLDER = "1ST6FxPuRaxwBOIz39MAN8Jj4gDv509-K"


def env_file(path: Path) -> None:
    if not path.is_file():
        return
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        os.environ.setdefault(key.strip(), value.strip().strip('"\''))


def result_object(response: dict) -> dict:
    result = response.get("result") or {}
    if isinstance(result, dict) and isinstance(result.get("result"), dict):
        return result["result"]
    data = result.get("data") if isinstance(result, dict) else None
    if isinstance(data, dict):
        result = data.get("result") or data
    return result if isinstance(result, dict) else {}


def verify(response: dict) -> dict:
    item = ((response.get("job") or {}).get("payload") or {}).get("items", [{}])[0]
    render = (item.get("output") or {}).get("render") or {}
    rendered = result_object(response)
    clips = rendered.get("localized_renders") or []
    metrics = rendered.get("render_metrics") or {}
    document = (rendered.get("documents") or {}).get("it") or {}
    if not (
        item.get("docs", {}).get("enabled") is True
        and len(item.get("source", {}).get("clip_ids", [])) == 5
        and render.get("enabled") is True
        and render.get("require_gpu") is True
        and render.get("drive_folder_id") == item.get("docs", {}).get("folder_id")
        and render.get("render_concurrency", 0) >= 8
        and render.get("watermark", {}).get("enabled") is True
        and render.get("subtitles", {}).get("enabled") is True
        and render.get("subtitles", {}).get("mode") == "burn"
        and render.get("subtitles", {}).get("style_id") == "shorts-v1-40-shadow"
        and len(clips) == 5
        and metrics.get("successful") == 5
        and metrics.get("failed", -1) == 0
        and all(clip.get("backend") == "chronon_vulkan" for clip in clips)
        and (document.get("link") or document.get("url"))
    ):
        raise RuntimeError("endpoint job completed without the required render/docs/folder contract")
    return {"document": document.get("link") or document.get("url"), "metrics": metrics, "clips": clips}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--job-id", help="poll an already submitted endpoint job")
    parser.add_argument("--timeout", type=int, default=1800)
    args = parser.parse_args()

    env_file(ROOT / ".env")
    token = os.environ.get("VELOX_PIPELINEGEN_TOKEN") or os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not token:
        raise RuntimeError("VELOX_PIPELINEGEN_TOKEN or VELOX_ADMIN_TOKEN is required")

    client = VeloxClient(os.environ.get("PIPELINEGEN_URL", "http://127.0.0.1:8000"), token)
    if args.job_id:
        job_id = args.job_id
    else:
        payload = json.loads(PAYLOAD.read_text(encoding="utf-8"))
        folder = os.environ.get("MATT_DAMON_DRIVE_FOLDER_ID", DEFAULT_FOLDER)
        subfolder = os.environ.get(
            "MATT_DAMON_DRIVE_SUBFOLDER_NAME",
            "matt-damon-5-clips-" + datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S"),
        )
        item = payload["items"][0]
        item["docs"]["folder_id"] = folder
        item["output"]["render"]["drive_folder_id"] = folder
        item["output"]["render"]["drive_subfolder_name"] = subfolder
        request_id = f"matt-damon-5-clips-{datetime.now(timezone.utc).strftime('%Y%m%d-%H%M%S') }"
        submitted = client.submit_async("api/script/generate", payload, req_id=request_id)
        job_id = submitted["job_id"]
        print(f"job_id={job_id}", flush=True)

    deadline = time.monotonic() + args.timeout
    while True:
        response = client.get_job_full(job_id)
        status = str(response.get("status") or (response.get("job") or {}).get("status") or "").upper()
        print(f"status={status}", flush=True)
        if status in {"SUCCEEDED", "COMPLETED", "SUCCEEDED_WITH_WARNINGS"}:
            verified = verify(response)
            print(json.dumps(verified, ensure_ascii=False))
            return 0
        if status in {"FAILED", "ERROR", "CANCELLED"}:
            print(json.dumps(response, ensure_ascii=False), file=sys.stderr)
            return 1
        if time.monotonic() >= deadline:
            raise TimeoutError(f"job {job_id} did not finish within {args.timeout}s")
        time.sleep(5)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)
