import json
import os
import re
import sqlite3
import sys
from collections import Counter
from datetime import datetime, timezone

args = sys.argv[1:]
idx = 0


def grab(n=1):
    global idx
    v = args[idx:idx + n]
    idx += n
    return v[0] if n == 1 else v


out_file = grab()
fingerprint_file = grab()
git_sha, git_branch, config_sha, db_sha, worker_ids, base_url = grab(6)
_legacy_start, _legacy_end, _legacy_elapsed = [int(x) for x in grab(3)]
success_count, n_jobs = int(grab()), int(grab())
jobs_dir = grab()

j_ids = grab(n_jobs)
j_labels = grab(n_jobs)
j_modes = grab(n_jobs)
j_statuses = grab(n_jobs)
# Optional worker slots (claim concurrency) from BENCH_WORKER_SLOTS; empty
# means unknown (the report then shows "-" and no utilization).
worker_slots_raw = grab()
try:
    worker_slots = int(worker_slots_raw)
except (TypeError, ValueError):
    worker_slots = 0


def load_full(i):
    p = os.path.join(jobs_dir, "job-%d.json" % i)
    if not os.path.isfile(p):
        return {}
    try:
        with open(p) as f:
            return json.load(f)
    except Exception:
        return {}


def ms(v):
    if isinstance(v, bool):
        return 0
    try:
        return int(v)
    except (TypeError, ValueError):
        return 0


def timestamp_ms(v):
    """Parse runtime timestamps from either numeric or RFC3339 JSON fields."""
    if isinstance(v, (int, float)) and not isinstance(v, bool):
        return int(v)
    if not isinstance(v, str) or not v.strip():
        return None
    try:
        return int(float(v))
    except ValueError:
        pass
    try:
        value = v.strip().replace("Z", "+00:00")
        # Go's RFC3339Nano timestamps may contain 9 fractional digits;
        # datetime.fromisoformat accepts at most 6.
        value = re.sub(r"(\.\d{6})\d+(?=\+00:00$)", r"\1", value)
        return int(datetime.fromisoformat(value).timestamp() * 1000)
    except (TypeError, ValueError, OverflowError):
        return None
def sentinel_ok(v):
    # metrics_v2 NOT_INSTRUMENTED sentinel serializes as the string
    # "NOT_INSTRUMENTED" (and -1 in-process). Only a real measured value
    # (>= 0) counts.
    if isinstance(v, str):
        return False
    try:
        return int(v) >= 0
    except (TypeError, ValueError):
        return False


def render_work(metrics):
    # Coarse backend (ffmpeg_fallback / cuda_native): composite_ms covers the
    # decode→encode span, so render WORK = startup + composite. A per-phase
    # backend instead sums every instrumented phase.
    if sentinel_ok(metrics.get("composite_ms")):
        return ms(metrics.get("renderer_startup_ms")) + ms(metrics.get("composite_ms"))
    total = 0
    for k in ("decode_ms", "composite_ms", "subtitle_raster_ms", "watermark_raster_ms",
              "frame_conversion_ms", "encode_ms", "audio_mux_ms"):
        if sentinel_ok(metrics.get(k)):
            total += ms(metrics.get(k))
    return total


def materialization(timings, result):
    # Per-asset materialization facts straight from the preparer's PhaseTiming
    # list (now exposed on the job result as timings.phases): each
    # materialize_<asset> phase carries wall_ms / work_ms plus notes with
    # from_cache and size_bytes. This is the real "bring assets to disk"
    # cost per asset — cache hits (from_cache=true) are visible as cheap
    # phases, and the bytes tell whether Drive was actually read.
    mat = {}
    for ph in timings.get("phases") or []:
        name = ph.get("phase", "")
        if not name.startswith("materialize_"):
            continue
        asset = name[len("materialize_"):]
        notes = ph.get("notes") or {}
        size_bytes = ms(notes.get("size_bytes"))
        mat[asset] = {
            "wall_ms": ms(ph.get("wall_ms")),
            "work_ms": ms(ph.get("work_ms")),
            "from_cache": bool(notes.get("from_cache")),
            "cache_hit": bool(notes.get("cache_hit", notes.get("from_cache"))),
            "size_bytes": size_bytes,
            "download_bytes": ms(notes.get("download_bytes", 0 if notes.get("from_cache") else size_bytes)),
            "asset_id": notes.get("asset_id", ""),
        }
    # Fallback when the server predates timings.phases: derive source facts
    # from the result's source block (from_cache + size_bytes) with the
    # materialize wall already folded into asset_materialize_ms.
    if not mat:
        src = result.get("source") or {}
        if src:
            mat["source"] = {
                "wall_ms": 0, "work_ms": 0,
                "from_cache": bool(src.get("from_cache")),
                "cache_hit": bool(src.get("cache_hit", src.get("from_cache"))),
                "size_bytes": ms(src.get("size_bytes")),
                "download_bytes": ms(src.get("download_bytes", 0 if src.get("from_cache") else src.get("size_bytes"))),
                "asset_id": src.get("asset_id", ""),
            }
    return mat


def phase(j, name):
    for p in j["phases"]:
        if p["name"] == name:
            return p
    return None


def fmt_sec(v):
    if v <= 0:
        return "-"
    return f"{v/1000:.1f}s"


def fmt_work(p):
    if not p:
        return "-"
    return "%s/%s" % (fmt_sec(ms(p.get("wall_ms"))), fmt_sec(ms(p.get("work_ms"))))


