#!/usr/bin/env python3
"""Sequential Obama clip batch: one video at a time, English only.

For each video:
  1. fetch the English transcript for every window (best-effort);
  2. POST /api/clips/process with concurrency=1;
  3. wait for the extraction job;
  4. run the canonical text-tracks-backfill with --languages=en to repair
     search_text and reindex every clip (the per-video fan-out is deduped by
     correlation_id, so this is required for clips 2..N);
  5. inspect every asset (index_state, embedding, text-track languages);
  6. only then move to the next video.

The runner is resumable through state.json and stops on the first hard failure.
"""
from __future__ import annotations

import hashlib
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path.cwd()
OUT = ROOT / "ops/jobs/obama_20261113"
MANIFEST = OUT / "manifest-remaining.json"
STATE = OUT / "state.json"
REPORT = OUT / "BATCH-REPORT.json"
FOLDER_ID = "1n4YRewXB7F7nC5QozS8RmTF2tBLFDfFr"
BASE = "http://127.0.0.1:8000"


def token() -> str:
    tok = os.environ.get("VELOX_ADMIN_TOKEN", "").strip()
    if tok:
        return tok
    env = ROOT / ".env"
    if env.is_file():
        for line in env.read_text(errors="ignore").splitlines():
            if line.strip().startswith("VELOX_ADMIN_TOKEN="):
                return line.split("=", 1)[1].strip().strip('"').strip("'")
    raise RuntimeError("VELOX_ADMIN_TOKEN not available")


TOKEN = token()


def log(msg: str) -> None:
    print(f"{time.strftime('%H:%M:%S')} {msg}", flush=True)


def http(method: str, path: str, body=None, timeout: int = 180, extra_headers=None):
    data = None
    headers = {"X-Velox-Admin-Token": TOKEN}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    headers.update(extra_headers or {})
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    last_err = None
    for attempt in range(61):
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                raw = resp.read().decode()
                return json.loads(raw) if raw.strip() else {}
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="ignore")[:1000]
            raise RuntimeError(f"HTTP {e.code} {method} {path}: {detail}") from None
        except (urllib.error.URLError, ConnectionError, TimeoutError) as e:
            last_err = e
            if method != "GET" or attempt >= 60:
                raise
            log(f"    service unavailable ({e}); retry {attempt + 1}/60")
            time.sleep(5)
    raise RuntimeError(f"GET {path} failed after retries: {last_err}")


def hms_to_sec(ts: str) -> int:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def dedup_append(acc, words, max_overlap=90):
    n = min(len(words), len(acc), max_overlap)
    for L in range(n, 0, -1):
        if acc[-L:] == words[:L]:
            acc.extend(words[L:])
            return
    acc.extend(words)


def transcript_for_window(url: str, start: str, end: str):
    params = {"url": url, "start": hms_to_sec(start), "end": hms_to_sec(end)}
    try:
        tr = http("GET", "/api/clips/transcript?" + urllib.parse.urlencode(params), timeout=45)
    except Exception as e:
        log(f"    transcript unavailable {start}-{end}: {e}")
        return None, ""
    acc = []
    for c in tr.get("cues") or []:
        dedup_append(acc, (c.get("text") or "").split())
    text = " ".join(acc).strip() or (tr.get("text") or "").strip()
    return (tr.get("language") or ""), text


def build_segments(video):
    segments = []
    for c in video["clips"]:
        name = c["name"].strip()
        kind = (c.get("kind") or "moment").lower()
        tags = ["Obama", video.get("category") or "interview"]
        topics = list(c.get("topics") or [])
        if kind == "hook":
            plain = name[5:].strip() if name.lower().startswith("hook:") else name
            name = "Hook: " + plain
            marker = "Hook: " + plain
            if marker not in topics:
                topics.insert(0, marker)
            if "hook" not in [t.lower() for t in tags]:
                tags.insert(0, "hook")
            category = "hook"
        else:
            category = video.get("category") or "interview"
        seg = {
            "start": c["start"],
            "end": c["end"],
            "name": name,
            "category": category,
            "summary": c.get("summary") or "",
            "topics": topics,
            "tags": tags,
            "mentioned_people": ["Barack Obama"],
            "source_title": video.get("title") or "",
            "source_channel": video.get("channel") or "",
        }
        if kind == "hook":
            seg["kind"] = "hook"
        segments.append(seg)
    return segments


def poll_job(job_id: str, timeout_s: int = 3600):
    deadline = time.time() + timeout_s
    last = None
    while time.time() < deadline:
        d = http("GET", f"/api/jobs/{job_id}/full", timeout=30)
        status = d.get("status") or (d.get("job") or {}).get("status") or "UNKNOWN"
        prog = d.get("progress")
        if prog is None:
            prog = (d.get("job") or {}).get("progress")
        if (status, prog) != last:
            log(f"    job {job_id}: {status} progress={prog}")
            last = (status, prog)
        if status in ("SUCCEEDED", "COMPLETED", "SUCCESS", "DONE"):
            return d
        if status in ("FAILED", "ERROR", "CANCELLED", "REJECTED", "DEAD_LETTER"):
            err = d.get("error") or (d.get("job") or {}).get("error") or ""
            raise RuntimeError(f"job {job_id} {status}: {err}")
        time.sleep(10)
    raise RuntimeError(f"job {job_id} timed out after {timeout_s}s")


def run_backfill(asset_ids):
    csv = ",".join(asset_ids)
    cmd = [
        str(ROOT / "bin/admin"),
        "text-tracks-backfill",
        "--source=youtube",
        "--languages=en",
        f"--asset-ids={csv}",
        "--all",
        "--apply",
        "--json",
    ]
    log(f"    backfill en: {len(asset_ids)} assets")
    p = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, timeout=900)
    if p.returncode != 0:
        raise RuntimeError(
            f"text-tracks-backfill failed rc={p.returncode}: "
            f"{(p.stderr or p.stdout)[-2000:]}"
        )
    raw = p.stdout
    start = raw.find("{")
    if start < 0:
        raise RuntimeError(f"text-tracks-backfill produced no JSON: {raw[-1000:]}")
    report = json.loads(raw[start:])
    if report.get("failed_asset_ids"):
        raise RuntimeError(f"backfill failed assets: {report['failed_asset_ids']}")
    if report.get("processed") != len(asset_ids):
        raise RuntimeError(
            f"backfill processed {report.get('processed')} of {len(asset_ids)}"
        )
    log(
        "    backfill: processed={processed} repaired={index_repaired_total} "
        "reindex={reindex_requested_total} subtitles={subtitles_delivered_total}".format(**report)
    )
    return report


def inspect_asset(asset_id: str):
    return http("GET", f"/api/assets/operator/assets/{asset_id}", timeout=30)


def wait_assets(asset_ids, timeout_s=180):
    deadline = time.time() + timeout_s
    last = None
    while time.time() < deadline:
        rows = []
        for aid in asset_ids:
            d = inspect_asset(aid)
            langs = sorted({t.get("language_code") for t in (d.get("text_tracks") or [])})
            health = (d.get("index_health") or {}).get("code")
            rows.append(
                {
                    "id": aid,
                    "index_state": d.get("index_state"),
                    "index_health": health,
                    "pending_outbox_events": d.get("pending_outbox_events"),
                    "has_embedding": d.get("has_embedding"),
                    "languages": langs,
                }
            )
        if all(
            r["index_state"] == "INDEXED"
            and r["pending_outbox_events"] == 0
            and r["has_embedding"]
            and r["languages"] == ["en"]
            and r["index_health"] not in (None, "PENDING")
            for r in rows
        ):
            return rows
        if rows != last:
            log(f"    waiting assets: {rows}")
            last = rows
        time.sleep(5)
    raise RuntimeError(f"assets not ready after {timeout_s}s: {rows}")


def save_state(state):
    tmp = STATE.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(state, indent=2, ensure_ascii=False))
    tmp.replace(STATE)


def main():
    manifest = json.loads(MANIFEST.read_text())
    videos = manifest["videos"]
    state = json.loads(STATE.read_text()) if STATE.is_file() else {"videos": {}}
    state.setdefault("videos", {})
    state["folder_id"] = FOLDER_ID
    state["started_at"] = state.get("started_at") or time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    for video in videos:
        n = str(video["n"])
        existing = state["videos"].get(n) or {}
        if existing.get("status") in ("done", "blocked"):
            log(f"video {n} {video['id']}: already {existing.get('status')}, skip")
            continue

        log(f"=== video {n} {video['id']} ===")
        entry = existing
        attempt = int(existing.get("attempt", 0))
        if existing.get("status") == "failed":
            attempt += 1
        entry["attempt"] = attempt
        entry.pop("error", None)
        entry.update(
            {
                "n": video["n"],
                "video_id": video["id"],
                "url": video["url"],
                "status": "running",
                "started_at": entry.get("started_at") or time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            }
        )
        state["videos"][n] = entry
        save_state(state)

        try:
            segments = build_segments(video)
            log(f"    {len(segments)} segments")
            for seg in segments:
                lang, text = transcript_for_window(video["url"], seg["start"], seg["end"])
                if text and lang == "en":
                    seg.setdefault("texts", []).insert(
                        0,
                        {
                            "language_code": "en",
                            "source_type": "provided",
                            "is_original": True,
                            "transcript": text,
                        },
                    )
                elif text:
                    log(f"    skip non-en transcript {seg['start']}-{seg['end']} lang={lang}")
                else:
                    log(f"    no transcript for {seg['start']}-{seg['end']}; server fallback")

            payload = {
                "url": video["url"],
                "strategy": "verify",
                "concurrency": 1,
                "keep_audio": True,
                "write_summary": True,
                "destination": {"folder_id": FOLDER_ID, "create_subfolder": False},
                "segments": segments,
            }
            stamp = "|".join(f"{s['start']}-{s['end']}" for s in segments)
            digest = hashlib.sha256(stamp.encode()).hexdigest()[:40]
            idem = f"obama-en:{video['id']}:a{attempt}:{len(segments)}segs:{digest}"
            ack = http(
                "POST",
                "/api/clips/process",
                body=payload,
                timeout=120,
                extra_headers={"Idempotency-Key": idem},
            )
            job_id = ack.get("job_id")
            if isinstance(job_id, dict):
                job_id = job_id.get("id")
            if not job_id:
                raise RuntimeError(f"no job_id in submit ack: {ack}")
            entry["job_id"] = job_id
            save_state(state)
            log(f"    submitted job_id={job_id}")

            d = poll_job(job_id)
            res = d.get("result") or (d.get("job") or {}).get("result") or {}
            items = res.get("items") or []
            if not items:
                raise RuntimeError(f"job {job_id} succeeded without items")
            clip_ids = []
            compact_items = []
            for it in items:
                st = it.get("status")
                if st not in ("processed", "succeeded", "skipped"):
                    raise RuntimeError(f"clip {it.get('id')} status={st} error={it.get('error')}")
                cid = it.get("id") or it.get("asset_id") or it.get("clip_id")
                if not cid:
                    raise RuntimeError(f"clip item without id: {it}")
                clip_ids.append(cid)
                compact_items.append(
                    {
                        k: it.get(k)
                        for k in ("id", "status", "start", "end", "name", "filename", "drive_file_id", "drive_link", "error")
                    }
                )
            entry["items"] = compact_items
            entry["asset_ids"] = clip_ids
            save_state(state)
            log(f"    {len(clip_ids)} clips committed, repairing search_text/index")

            report = run_backfill(clip_ids)
            entry["backfill"] = {
                k: report.get(k)
                for k in (
                    "processed",
                    "source_ready_count",
                    "source_acquired_count",
                    "created_total",
                    "index_repaired_total",
                    "reindex_requested_total",
                    "subtitles_delivered_total",
                    "failed_lang_total",
                    "failed_asset_ids",
                    "duration_ms",
                )
            }
            save_state(state)

            rows = wait_assets(clip_ids)
            entry["assets"] = rows
            entry["status"] = "done"
            entry["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
            save_state(state)
            log(f"video {n} DONE: {len(clip_ids)} clips indexed en")
            time.sleep(5)

        except Exception as e:
            entry["status"] = "failed"
            entry["error"] = str(e)
            entry["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
            save_state(state)
            log(f"video {n} FAILED: {e}")
            raise

    REPORT.write_text(json.dumps(state, indent=2, ensure_ascii=False))
    log("batch complete")


if __name__ == "__main__":
    main()
