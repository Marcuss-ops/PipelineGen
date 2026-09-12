#!/usr/bin/env python3
"""
Benchmark Runner for 5 Clips Parallel Render via /api/script/generate.

Specs:
- 5 Clips
- Concurrency: 5
- Watermark: center, text 'VELOX EDITING'
- Subtitles: burn mode, preset 'impact'
- Background: 'classic1' (Pale Olive Classic)
"""

import os
import sys
import time
import json
import uuid
import urllib.request
import urllib.error
import subprocess
from pathlib import Path

BASE_URL = os.environ.get("VELOX_BASE_URL", "http://127.0.0.1:8000")
ADMIN_TOKEN = os.environ.get("VELOX_ADMIN_TOKEN", "").strip()

JOB_FILE = Path(__file__).resolve().parents[2] / "ops" / "jobs" / "celebrity_5_clips.generate.json"

def make_request(url, data=None, method="GET"):
    headers = {
        "Authorization": f"Bearer {ADMIN_TOKEN}",
        "Content-Type": "application/json",
        "Idempotency-Key": str(uuid.uuid4()),
    }
    req = urllib.request.Request(url, headers=headers, method=method)
    if data:
        req.data = json.dumps(data).encode("utf-8")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8")
            return resp.status, json.loads(body) if body else {}
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8")
        try:
            err_json = json.loads(body)
        except Exception:
            err_json = {"raw": body}
        return e.code, err_json

def probe_clip(path):
    if not os.path.exists(path):
        return None
    try:
        cmd = [
            "ffprobe", "-v", "error",
            "-select_streams", "v:0",
            "-show_entries", "stream=width,height,duration,r_frame_rate,codec_name",
            "-of", "json",
            path
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, check=True)
        info = json.loads(res.stdout)
        streams = info.get("streams", [])
        if streams:
            s = streams[0]
            size_mb = os.path.getsize(path) / (1024 * 1024)
            return {
                "exists": True,
                "path": path,
                "size_mb": round(size_mb, 2),
                "width": s.get("width"),
                "height": s.get("height"),
                "codec": s.get("codec_name"),
                "fps": s.get("r_frame_rate"),
                "duration": float(s.get("duration", 0)),
            }
    except Exception as e:
        return {"exists": True, "path": path, "error": str(e)}
    return None

def main():
    if not ADMIN_TOKEN:
        print("ERROR: VELOX_ADMIN_TOKEN must be set; refusing to use a hardcoded credential")
        sys.exit(2)

    print("=" * 70)
    print("  VELOX EDITING - 5 CLIPS PARALLEL RENDER BENCHMARK")
    print("=" * 70)
    print(f"Target URL:        {BASE_URL}/api/script/generate")
    print(f"Base Payload File: {JOB_FILE}")

    if not JOB_FILE.exists():
        print(f"ERROR: Payload file {JOB_FILE} not found!")
        sys.exit(1)

    with open(JOB_FILE) as f:
        payload = json.load(f)

    run_id = f"bench-5clips-{int(time.time())}"
    payload["correlation_id"] = run_id
    if payload.get("items"):
        payload["items"][0]["id"] = run_id
        # Force 5 concurrency, Pale Olive background, center watermark, subs burn
        r = payload["items"][0]["output"]["render"]
        r["render_concurrency"] = 5
        r["background"] = {"mode": "asset", "asset_id": "classic1"}
        r["watermark"]["position"] = "center"
        r["subtitles"]["enabled"] = True
        r["subtitles"]["mode"] = "burn"

    print(f"Run ID:            {run_id}")
    print(f"Clips count:       {len(payload['items'][0]['source']['clip_ids'])}")
    print(f"Background:        {payload['items'][0]['output']['render']['background']['asset_id']} (Pale Olive)")
    print(f"Watermark pos:     {payload['items'][0]['output']['render']['watermark']['position']}")
    print(f"Subtitles:         mode={payload['items'][0]['output']['render']['subtitles']['mode']}")
    print(f"Concurrency:       {payload['items'][0]['output']['render']['render_concurrency']}")
    print("-" * 70)

    print(f"Submitting job to {BASE_URL}/api/script/generate ...")
    t0 = time.time()
    status_code, resp = make_request(f"{BASE_URL}/api/script/generate", data=payload, method="POST")
    submit_wall = time.time() - t0

    if status_code not in (200, 202):
        print(f"Submission FAILED with HTTP {status_code}: {resp}")
        sys.exit(1)

    job_id = resp.get("job_id") or resp.get("id")
    print(f"Job enqueued successfully! Job ID: {job_id} (submit time: {submit_wall*1000:.1f}ms)")
    print("-" * 70)
    print("Polling job execution...")

    poll_start = time.time()
    last_status = None
    poll_count = 0
    full_data = None

    while True:
        poll_count += 1
        elapsed = time.time() - poll_start
        status_code, full_data = make_request(f"{BASE_URL}/api/jobs/{job_id}/full")

        job_status = full_data.get("status", "UNKNOWN").upper()
        current_stage = full_data.get("current_stage", "")

        if job_status != last_status:
            print(f"[{elapsed:6.1f}s] Status: {job_status:<12} Stage: {current_stage}")
            last_status = job_status
        else:
            print(f"\r[{elapsed:6.1f}s] Status: {job_status:<12} (polling #{poll_count})...", end="", flush=True)

        if job_status in ("COMPLETED", "SUCCEEDED"):
            print(f"\n Job completed successfully in {elapsed:.2f}s!")
            break
        elif job_status in ("FAILED", "CANCELLED", "ERROR"):
            print(f"\n Job {job_status}! Full output:\n{json.dumps(full_data, indent=2)}")
            sys.exit(1)

        time.sleep(2)

    total_wall_sec = time.time() - t0
    print("=" * 70)
    print("  VERIFYING GENERATED CLIPS")
    print("=" * 70)

    result_obj = full_data.get("result", {})
    inner_result = result_obj.get("result", {}) if "result" in result_obj else result_obj

    localized_renders = inner_result.get("localized_renders", [])
    if not localized_renders and "localized_render_staged" in inner_result:
        localized_renders = inner_result.get("localized_render_staged", [])

    print(f"Total Localized Renders reported: {len(localized_renders)}")

    verified_clips = []
    for idx, r in enumerate(localized_renders, 1):
        local_path = r.get("local_path") or r.get("output_path", "")
        scene_id = r.get("scene_id", f"scene-{idx}")
        clip_id = r.get("clip_id", "")
        render_wall_ms = r.get("wall_ms", 0)

        probe = probe_clip(local_path)
        verified_clips.append({
            "index": idx,
            "scene_id": scene_id,
            "clip_id": clip_id,
            "local_path": local_path,
            "render_wall_ms": render_wall_ms,
            "probe": probe
        })

        print(f"\nClip #{idx}: {scene_id} (source: {clip_id})")
        print(f"  Path:       {local_path}")
        print(f"  Render Wall: {render_wall_ms} ms")
        if probe:
            print(f"  Specs:      {probe.get('width')}x{probe.get('height')} @ {probe.get('fps')} fps ({probe.get('codec')})")
            print(f"  Duration:   {probe.get('duration')}s, Size: {probe.get('size_mb')} MB")
        else:
            print(f"  Probe:      File not found or unreadable on local disk")

    print("\n" + "=" * 70)
    print("  PERFORMANCE METRICS BREAKDOWN")
    print("=" * 70)

    render_metrics = inner_result.get("render_metrics", {})
    run_report = inner_result.get("run_report") or result_obj.get("run_report", {})

    print(f"Total E2E Wall Clock: {total_wall_sec:.2f} s ({total_wall_sec*1000:.0f} ms)")

    if render_metrics:
        expected = render_metrics.get("expected", len(localized_renders))
        successful = render_metrics.get("successful", len(localized_renders))
        work_ms = render_metrics.get("work_ms", 0)
        wall_ms = render_metrics.get("wall_ms", 0)
        concurrency = render_metrics.get("concurrency", 5)
        speedup = (work_ms / wall_ms) if wall_ms > 0 else 0

        print(f"Render Units Expected:    {expected}")
        print(f"Render Units Successful:  {successful}")
        print(f"Render Concurrency:       {concurrency}")
        print(f"Total Render Work Time:   {work_ms} ms (sum of all renders)")
        print(f"Total Render Wall Time:   {wall_ms} ms (concurrent batch duration)")
        print(f"Realized Speedup:         {speedup:.2f}x (effective parallelism)")
        if wall_ms > 0:
            rate = (successful / (wall_ms / 60000.0))
            print(f"Throughput:               {rate:.1f} clips/min")

    if run_report and run_report.get("stages"):
        print("\nStage Timings from RunReport:")
        for s in run_report.get("stages", []):
            st_name = s.get("stage")
            st_wall = s.get("wall_ms", 0)
            st_status = s.get("status", "")
            print(f"  - {st_name:<25} {st_wall:>8} ms  [{st_status}]")

    print("=" * 70)
    print("  BENCHMARK COMPLETE")
    print("=" * 70)

if __name__ == "__main__":
    main()
