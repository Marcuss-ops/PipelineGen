#!/usr/bin/env python3
"""tclip_long_driver — finish the 10-minute (600s) long-segment extraction.

Why a second driver instead of the plain batch runner: on this box the
pipelinegen unit is restarted by an external deploy every few minutes, which
kills whatever job is in flight. A batch runner that only looks at its own
subprocess exit code then either gives up or re-submits duplicates.

This driver is idempotent on the ARTIFACT, not on the job: for every segment it
first asks Drive whether `yt_<videoID>_<start>_<end>_v1*` already exists in the
destination subfolder. Only the missing ones are submitted (with `--no-wait`,
so the client is never the thing that dies), then the recorded job ids are
polled until terminal — tolerating connection failures — and the Drive listing
is re-checked. Rounds repeat until every segment is on Drive or the round
budget is spent.

Exit code 0 only when every expected segment is present on Drive.
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def _token() -> str:
    import os
    tok = os.environ.get("VELOX_ADMIN_TOKEN", "").strip()
    if tok:
        return tok
    for line in (ROOT / ".env").read_text(errors="ignore").splitlines():
        if line.startswith("VELOX_ADMIN_TOKEN="):
            return line.split("=", 1)[1].strip().strip('"').strip("'")
    return ""


def _request(path: str, timeout: int = 30):
    req = urllib.request.Request(_base() + path,
                                 headers={"X-Velox-Admin-Token": _token()})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read().decode()
    return json.loads(raw) if raw.strip() else {}


def _base() -> str:
    import os
    return os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000"


def _sec(ts: str) -> int:
    parts = [int(p) for p in ts.strip().split(":")]
    while len(parts) < 3:
        parts.insert(0, 0)
    return parts[0] * 3600 + parts[1] * 60 + parts[2]


def _folder_children(folder_id: str) -> list[dict]:
    """[] when the folder cannot be read (server down / folder absent)."""
    try:
        r = _request("/api/drive/files?folder_id=" + urllib.parse.quote(folder_id))
        return r.get("files") or []
    except Exception:
        return []


def _find_subfolder(root_id: str, name: str) -> str:
    for f in _folder_children(root_id):
        if f.get("name") == name and f.get("mime_type") == "application/vnd.google-apps.folder":
            return f["id"]
    return ""


def _snapshot(root_id: str, planned: list[dict]) -> list[dict]:
    """Re-read Drive and fill entry['sub_id']/entry['present'] for every entry."""
    sub_ids: dict[str, str] = {}
    for e in planned:
        name = e["subfolder"]
        if name not in sub_ids:
            sub_ids[name] = _find_subfolder(root_id, name)
        sub_id = sub_ids[name]
        e["sub_id"] = sub_id
        e["present"] = bool(sub_id) and any(
            f.get("name", "").startswith(e["prefix"]) for f in _folder_children(sub_id))
    return [e for e in planned if not e["present"]]


def _entry(video_id: str, seg: dict) -> dict:
    start, end = _sec(seg["start"]), _sec(seg["end"])
    return {
        "video_id": video_id,
        "start": seg["start"],
        "end": seg["end"],
        "start_sec": start,
        "end_sec": end,
        "name": seg["name"],
        "prefix": f"yt_{video_id}_{start}_{end}_v1",
    }


def _log(log_path: Path, msg: str) -> None:
    line = f"{time.strftime('%H:%M:%S')} {msg}"
    print(line, flush=True)
    with log_path.open("a", encoding="utf-8") as fh:
        fh.write(line + "\n")


def _submit(entry: dict, segments_file: Path, seg_index: int, root_id: str,
            subfolder: str, tag: str, log_path: Path) -> str:
    """Submit one segment as its own job; return the job id ('' on failure)."""
    tmp = segments_file.parent / "_split_live" / f"{segments_file.stem}.p{seg_index}.json"
    tmp.parent.mkdir(parents=True, exist_ok=True)
    tmp.write_text(json.dumps([json.loads(segments_file.read_text())[seg_index - 1]],
                              ensure_ascii=False, indent=1))
    cmd = [
        sys.executable, str(ROOT / "scripts" / "tclip.py"), "clip",
        "--url", f"https://www.youtube.com/watch?v={entry['video_id']}",
        "--folder-id", root_id,
        "--segments-file", str(tmp),
        "--subfolder", subfolder,
        "--create-subfolder",
        "--with-transcript",
        "--idempotency", f"tclip:{entry['video_id']}:{entry['start_sec']}:{tag}",
        "--concurrency", "2",
        "--no-wait",
    ]
    proc = subprocess.run(cmd, cwd=str(ROOT), capture_output=True, text=True)
    out = (proc.stdout or "") + (proc.stderr or "")
    m = re.search(r"job_id=(\S+)", out)
    if not m:
        _log(log_path, f"SUBMIT-FAIL {entry['prefix']} rc={proc.returncode} "
                       f"{out.strip().splitlines()[-1][:160] if out.strip() else ''}")
        return ""
    _log(log_path, f"SUBMITTED {entry['prefix']} job={m.group(1)}")
    return m.group(1)


def _poll(job_id: str, deadline: float, log_path: Path) -> str:
    """Return the terminal status, tolerating connection errors while polling."""
    last = ""
    while time.time() < deadline:
        try:
            body = _request(f"/api/jobs/{job_id}/full", timeout=20)
        except Exception:
            time.sleep(10)
            continue
        job = body.get("job", body)
        status = job.get("status") or "UNKNOWN"
        if status != last:
            _log(log_path, f"  {job_id} -> {status}")
            last = status
        if status in ("SUCCEEDED", "COMPLETED", "SUCCESS", "DONE"):
            return "SUCCEEDED"
        if status in ("FAILED", "ERROR", "CANCELLED", "REJECTED", "DEAD_LETTER"):
            return "FAILED"
        time.sleep(15)
    return "TIMEOUT"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--folder-id", required=True, help="root Drive folder")
    ap.add_argument("--data-dir", default="data/tclip/persons/vannacci")
    ap.add_argument("--rounds", type=int, default=12)
    ap.add_argument("--wave", type=int, default=4,
                    help="how many missing segments to submit per round (0 = all)")
    ap.add_argument("--full-after", type=int, default=2,
                    help="from this round on, submit every missing segment at once")
    ap.add_argument("--only", default="", help="comma separated video ids")
    ap.add_argument("--job-timeout", type=int, default=2700)
    a = ap.parse_args()

    data = ROOT / a.data_dir
    meta = json.loads((data / "_meta.json").read_text())
    log_path = data / "_long_driver.log"
    only = {v.strip() for v in a.only.split(",") if v.strip()}

    planned: list[dict] = []
    for seg_file in sorted(data.glob("[0-9][0-9]-*.long.json")):
        vid = seg_file.stem.split("-", 1)[1].removesuffix(".long")
        if only and vid not in only:
            continue
        n = seg_file.stem.split("-", 1)[0]
        channel = (meta.get(vid, {}).get("channel") or "canale").strip()
        segs = json.loads(seg_file.read_text())
        for i, seg in enumerate(segs, 1):
            e = _entry(vid, seg)
            e.update({"segments_file": seg_file, "seg_index": i,
                      "subfolder": f"Vannacci {n} - {channel} - lungo"[:90]})
            planned.append(e)

    _log(log_path, f"planned {len(planned)} long segments")

    for round_no in range(1, a.rounds + 1):
        missing = _snapshot(a.folder_id, planned)
        _log(log_path, f"round {round_no}: {len(planned) - len(missing)}/{len(planned)} on Drive, "
                       f"{len(missing)} missing")
        if not missing:
            _log(log_path, "all long segments present on Drive")
            return 0

        wave = a.wave
        if round_no >= a.full_after:
            wave = 0
        planned_round = missing if wave == 0 else missing[:wave]
        tag = time.strftime("%y%m%d%H%M%S")
        jobs: list[tuple[dict, str]] = []
        for e in planned_round:
            if not _api_ready():
                _log(log_path, "API not answering, pausing submissions")
                time.sleep(30)
                if not _api_ready():
                    break
            job_id = _submit(e, e["segments_file"], e["seg_index"], a.folder_id,
                             e["subfolder"], tag, log_path)
            if job_id:
                jobs.append((e, job_id))
            time.sleep(5)

        if not jobs:
            _log(log_path, "no job accepted this round")
            continue

        # Poll in two parallel lanes so one slow segment does not serialize the pass.
        import threading
        lock = threading.Lock()
        idx = 0
        results: dict[str, str] = {}

        def lane() -> None:
            nonlocal idx
            while True:
                with lock:
                    if idx >= len(jobs):
                        return
                    entry, job_id = jobs[idx]
                    idx += 1
                results[entry["prefix"]] = _poll(job_id, time.time() + a.job_timeout, log_path)

        lanes = max(2, min(8, len(jobs)))
        threads = [threading.Thread(target=lane) for _ in range(lanes)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        for prefix, status in results.items():
            if status != "SUCCEEDED":
                _log(log_path, f"job for {prefix} ended {status} (retried next round)")

    # Final verdict: the artifacts themselves decide.
    missing = [e["prefix"] for e in _snapshot(a.folder_id, planned)]
    if missing:
        _log(log_path, f"INCOMPLETE: {len(missing)} segments still missing: {missing}")
        return 1
    _log(log_path, "all long segments present on Drive")
    return 0


def _api_ready(timeout: int = 240) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            _request("/api/jobs?limit=1", timeout=5)
            return True
        except Exception:
            time.sleep(10)
    return False


if __name__ == "__main__":
    raise SystemExit(main())
