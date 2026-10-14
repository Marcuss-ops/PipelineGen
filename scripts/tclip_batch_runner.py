#!/usr/bin/env python3
"""tclip_batch_runner — submit `tclip.py clip` jobs for a person's interviews.

One job per interview by default; `--split` submits one job per SEGMENT instead
(a restart then only costs the segment in flight, not the whole interview).
Runs at most `--parallel` jobs at once so one server is not drowned by
simultaneous yt-dlp + ffmpeg pipelines. Each video gets its own Drive
subfolder inside the destination folder. Progress is appended to
<data-dir>/_<state>.log and the per-entry results to <data-dir>/_<state>_jobs.json
so a re-run resumes only the entries that did not succeed.

`--attempts N` re-queues the failures after waiting for the API to answer, which
matters on a box where an external deploy may restart the service mid-flight.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import threading
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

START_FMT = "%H:%M:%S"


def _log(path: Path, msg: str) -> None:
    line = f"{time.strftime(START_FMT)} {msg}"
    print(line, flush=True)
    with path.open("a", encoding="utf-8") as fh:
        fh.write(line + "\n")


def _base_url() -> str:
    import os
    return os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000"


def _token() -> str:
    import os
    tok = os.environ.get("VELOX_ADMIN_TOKEN", "").strip()
    if tok:
        return tok
    env = ROOT / ".env"
    if env.is_file():
        for line in env.read_text(errors="ignore").splitlines():
            if line.startswith("VELOX_ADMIN_TOKEN="):
                return line.split("=", 1)[1].strip().strip('"').strip("'")
    return ""


def api_ready(timeout: int = 300) -> bool:
    """Wait until the admin API answers, so a submission is not sent into a restart."""
    deadline = time.time() + timeout
    req_url = _base_url().rstrip("/") + "/api/jobs?limit=1"
    while time.time() < deadline:
        try:
            req = urllib.request.Request(req_url, headers={"X-Velox-Admin-Token": _token()})
            with urllib.request.urlopen(req, timeout=5) as resp:
                if resp.status == 200:
                    return True
        except Exception:
            time.sleep(10)
    return False


def run_one(entry: dict, folder_id: str, log: Path, lock: threading.Lock,
            results: dict, results_path: Path, state: str, suffix: str, run_tag: str) -> None:
    key = entry["key"]
    subfolder = entry["subfolder"]
    n_segs = len(json.loads(Path(entry["segments_file"]).read_text()))
    with lock:
        _log(log, f"START {key} -> {subfolder}")
    cmd = [
        sys.executable, str(ROOT / "scripts" / "tclip.py"), "clip",
        "--url", f"https://www.youtube.com/watch?v={entry['video_id']}",
        "--folder-id", folder_id,
        "--segments-file", entry["segments_file"],
        "--subfolder", subfolder,
        "--create-subfolder",
        "--with-transcript",
        # The server answers an idempotency hit with the PRIOR job even when that
        # job failed, so the key must change per invocation: a re-run after a
        # deploy-killed job has to create a fresh job, not resurrect the corpse.
        "--idempotency", f"tclip:{key}:{state}{suffix}:{run_tag}:{n_segs}segs",
        "--source-title", entry.get("title") or "",
        "--source-channel", entry.get("channel") or "",
        "--concurrency", "2",
        "--timeout", "5400",
        "--interval", "15",
    ]
    try:
        proc = subprocess.run(cmd, cwd=str(ROOT), capture_output=True, text=True)
        out = (proc.stdout or "") + (proc.stderr or "")
        with lock:
            results[key] = {"returncode": proc.returncode,
                            "tail": out.strip().splitlines()[-6:]}
            results_path.write_text(json.dumps(results, ensure_ascii=False, indent=1))
            _log(log, f"DONE  {key} rc={proc.returncode}")
            for line in out.strip().splitlines()[-8:]:
                _log(log, f"    {key}| {line}")
    except Exception as exc:  # pragma: no cover - defensive
        with lock:
            results[key] = {"returncode": -1, "error": str(exc)}
            results_path.write_text(json.dumps(results, ensure_ascii=False, indent=1))
            _log(log, f"FAIL  {key} {exc}")


def build_entries(data: Path, suffix: str, only: set[str], split: bool,
                  split_dir: Path) -> list[dict]:
    meta_path = data / "_meta.json"
    meta = json.loads(meta_path.read_text()) if meta_path.is_file() else {}
    entries: list[dict] = []
    for seg in sorted(data.glob(f"[0-9][0-9]-*{suffix}.json")):
        vid = seg.stem.split("-", 1)[1]
        if suffix and vid.endswith(suffix):
            vid = vid[: -len(suffix)]
        if only and vid not in only:
            continue
        n = seg.stem.split("-", 1)[0]
        info = meta.get(vid, {})
        channel = (info.get("channel") or "canale").strip()
        title = (info.get("title") or vid).strip()
        sub = f"Vannacci {n} - {channel}" + (" - lungo" if suffix else "")
        segs = json.loads(seg.read_text())
        if not split or len(segs) == 1:
            entries.append({"key": vid, "video_id": vid, "segments_file": str(seg),
                            "subfolder": sub[:90], "title": title, "channel": channel})
            continue
        split_dir.mkdir(parents=True, exist_ok=True)
        for i, one in enumerate(segs, 1):
            part = split_dir / f"{seg.stem}.p{i}.json"
            part.write_text(json.dumps([one], ensure_ascii=False, indent=1))
            entries.append({
                "key": f"{vid}#{i}", "video_id": vid, "segments_file": str(part),
                "subfolder": sub[:90], "title": title, "channel": channel,
            })
    return entries


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--folder-id", required=True)
    ap.add_argument("--data-dir", default="data/tclip/persons/vannacci")
    ap.add_argument("--only", default="", help="comma separated video ids")
    ap.add_argument("--parallel", type=int, default=2)
    ap.add_argument("--suffix", default="",
                    help="segment-file suffix (e.g. .long) so a second pass can reuse the same videos")
    ap.add_argument("--state", default="batch", help="log/jobs state prefix")
    ap.add_argument("--split", action="store_true", help="one job per segment")
    ap.add_argument("--attempts", type=int, default=1, help="re-queue failures N times")
    a = ap.parse_args()

    data = ROOT / a.data_dir
    split_dir = data / "_split" / a.state
    entries = build_entries(data, a.suffix, {v.strip() for v in a.only.split(",") if v.strip()},
                            a.split, split_dir)
    if not entries:
        print("no entries matched", flush=True)
        return 1

    log = data / f"_{a.state}.log"
    results_path = data / f"_{a.state}_jobs.json"
    results = json.loads(results_path.read_text()) if results_path.is_file() else {}
    lock = threading.Lock()
    run_tag = time.strftime("%y%m%d%H%M%S")

    for attempt in range(1, a.attempts + 1):
        queue = [e for e in entries if results.get(e["key"], {}).get("returncode") != 0]
        if not queue:
            break
        if not api_ready():
            _log(log, f"attempt {attempt}: API not answering, aborting pass")
            break
        _log(log, f"attempt {attempt}: {len(queue)}/{len(entries)} entries, parallel={a.parallel}")

        idx = 0

        def worker() -> None:
            nonlocal idx
            while True:
                with lock:
                    if idx >= len(queue):
                        return
                    e = queue[idx]
                    idx += 1
                run_one(e, a.folder_id, log, lock, results, results_path,
                        a.state, a.suffix, run_tag)

        threads = [threading.Thread(target=worker) for _ in range(max(1, a.parallel))]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

    ok = sum(1 for e in entries if results.get(e["key"], {}).get("returncode") == 0)
    _log(log, f"batch end: {ok}/{len(entries)} ok")
    return 0 if ok == len(entries) else 1


if __name__ == "__main__":
    raise SystemExit(main())
