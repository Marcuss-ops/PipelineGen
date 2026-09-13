jobs = [build_job(load_full(i), i) for i in range(n_jobs)]


def job_assets(j):
    r = j["result"]
    if r.get("asset") and r["asset"].get("asset_id"):
        return 1
    manifest = (r.get("data") or {}).get("__artifact_manifest") or {}
    artifacts = manifest.get("artifacts") or []
    return len(artifacts) if artifacts else (1 if r.get("data") else 0)


def batch_intervals(jobs):
    # Per-phase wall-clock intervals on the batch clock, placed along each
    # job's REAL critical path: phases run serially inside the job in
    # critical_order, each occupying its critical_ms (phases with 0 critical
    # contribution — e.g. TTS work overlapped by generate — are skipped, so
    # the union reflects true wall-time occupancy, never accumulated work).
    # The window is the server-owned execution span [started_at, finished_at].
    out = {}
    for i, j in enumerate(jobs):
        order = j.get("critical_order") or []
        if not order:
            continue
        start = j.get("runtime_started_ms")
        end = j.get("runtime_finished_ms")
        if start is None or end is None:
            continue
        offset = 0
        for name in order:
            p = next((p for p in j["phases"] if p["name"] == name), None)
            crit = (p or {}).get("critical_ms") or 0
            if crit > 0:
                s = start + offset
                e = min(end, s + crit)
                if e > s:
                    out.setdefault(name, []).append((s, e))
            offset += crit
    return out


def union_ms(intervals):
    if not intervals:
        return 0
    iv = sorted(intervals)
    total = 0
    cs, ce = iv[0]
    for s, e in iv[1:]:
        if s > ce:
            total += ce - cs
            cs, ce = s, e
        else:
            ce = max(ce, e)
    return total + ce - cs


def batch_concurrency(jobs):
    # Real concurrency from the per-job execution windows recorded by the runtime.
    # peak = max simultaneous running jobs; average = time-weighted mean;
    # both measured — never guessed from the submit pattern.
    events = []
    for i, j in enumerate(jobs):
        start = j.get("runtime_started_ms")
        end = j.get("runtime_finished_ms")
        if start is None or end is None:
            continue
        if end > start:
            events.append((start, 1))
            events.append((end, -1))
    if not events:
        return 0, 0.0
    events.sort()
    active, weighted, peak, last = 0, 0, 0, events[0][0]
    span = events[-1][0] - events[0][0]
    for at, delta in events:
        weighted += active * (at - last)
        active += delta
        if active > peak:
            peak = active
        last = at
    avg = weighted / span if span > 0 else float(peak)
    return peak, avg


peak_concurrency, average_concurrency = batch_concurrency(jobs)
concurrency_utilization = round(peak_concurrency / worker_slots * 100, 1) if worker_slots > 0 else None

# Batch aggregates are derived only from job-runtime SSOT events and the
# per-job RunReport values. Local submit/poll clocks are not metric inputs.
# batch_wall is the PARALLEL WALL-CLOCK of the batch: the elapsed span from
# the earliest runtime execution start to the latest runtime execution finish.
# It is deliberately NOT the max per-clip E2E wall: with concurrency > 1 the
# two differ whenever clips are staggered (queue wait pushes starts apart).
# batch_work is the sum of each clip's E2E execution wall — the
# sequential-equivalent total, never a wall clock. The cumulative FFmpeg work
# is a third quantity (batch_render_work_ms, Σ render-phase work below).
# These are different magnitudes: none may be subtracted from another to get
# "the overhead" when concurrency > 1.
runtime_windows = [
    (j["runtime_started_ms"], j["runtime_finished_ms"]) for j in jobs
    if (j.get("runtime_started_ms") or 0) > 0
    and (j.get("runtime_finished_ms") or 0) > (j.get("runtime_started_ms") or 0)
]
report_walls = [ms(j["timing"].get("wall_ms")) for j in jobs if ms(j["timing"].get("wall_ms")) > 0]
if runtime_windows:
    batch_wall = max(f for _, f in runtime_windows) - min(s for s, _ in runtime_windows)
    batch_wall_source = "runtime_execution_span"
elif report_walls:
    # Fallback for servers predating started_at_ms/finished_at_ms: the max
    # per-clip E2E wall. Understated whenever clip starts are staggered.
    batch_wall = max(report_walls)
    batch_wall_source = "max_per_clip_e2e_wall_fallback"
else:
    batch_wall = 0
    batch_wall_source = "unavailable"
batch_work = sum(ms(j.get("timing", {}).get("execution_wall_ms", j["wall_ms"])) for j in jobs)
intervals = batch_intervals(jobs)
all_intervals = [iv for ivs in intervals.values() for iv in ivs]
# Batch critical path is the union of per-job phase windows on the batch
# clock. For a batch of independent jobs it is the longest serial chain
# through the run — the honest denominator for bottleneck shares (never the
# summed work, which double-counts parallel phases).
batch_critical = union_ms(all_intervals) if all_intervals else batch_wall
phases_summary = {}
for name, ivs in intervals.items():
    work = sum(p["work_ms"] for j in jobs for p in j["phases"] if p["name"] == name)
    wall = sum(p["wall_ms"] for j in jobs for p in j["phases"] if p["name"] == name)
    crit = union_ms(ivs)
    phases_summary[name] = {
        "work_ms": work,
        "wall_ms": wall,
        "critical_path_ms": crit,
        "critical_share": round(crit / batch_critical * 100, 1) if batch_critical else 0.0,
        "jobs": sum(1 for j in jobs for p in j["phases"] if p["name"] == name and p["wall_ms"] > 0),
    }
# Cumulative FFmpeg work across the batch (Σ render-phase work). This is the
# "41.08 s" figure in batch analyses — a work total, never comparable with
# the parallel wall clock by subtraction.
render_work_total = phases_summary.get("render", {}).get("work_ms", 0)
# The batch bottleneck is the phase with the LARGEST critical-path
# contribution (wall occupancy on the serial chain) — never the phase with
# the most accumulated work.
bottleneck = max(phases_summary, key=lambda n: phases_summary[n]["critical_path_ms"]) if phases_summary else None


def job_bottleneck(j):
    # Per-job bottleneck from the job's own serial chain (max critical_ms);
    # falls back to the RunReport bottleneck_stage for generate jobs.
    best, best_ms = None, 0
    for p in j["phases"]:
        c = p.get("critical_ms") or 0
        if c > best_ms:
            best_ms = c
            best = p["name"]
    if best is None:
        best = (j["timing"] or {}).get("bottleneck_stage") or "-"
    return best

totals = [j["wall_ms"] for j in jobs if j["wall_ms"] > 0]
min_t = min(totals) if totals else 0
max_t = max(totals) if totals else 0
avg_t = int(sum(totals) / len(totals)) if totals else 0
total_assets = sum(job_assets(j) for j in jobs)
submit_total = 0


# ── Batch worker facts (aggregates over the sealed job results) ───────────
# Materialization is reported per asset and retained alongside the existing
# aggregate asset_materialize_ms. These are facts from the worker/preparer;
# this script only aggregates them.
def materialization_summary(jobs):
    summary = {}
    for asset_name in ("source", "watermark", "background"):
        rows = [j["materialization"].get(asset_name) for j in jobs
                if isinstance(j.get("materialization"), dict)
                and isinstance(j["materialization"].get(asset_name), dict)]
        summary[asset_name] = {
            "wall_ms": sum(ms(row.get("wall_ms")) for row in rows),
            "work_ms": sum(ms(row.get("work_ms")) for row in rows),
            "size_bytes": sum(ms(row.get("size_bytes")) for row in rows),
            "download_bytes": sum(ms(row.get("download_bytes")) for row in rows),
            "cache_hits": sum(1 for row in rows if row.get("cache_hit", row.get("from_cache", False))),
            "cache_misses": sum(1 for row in rows if not row.get("cache_hit", row.get("from_cache", False))),
            "jobs": len(rows),
        }
    return summary

materialization_totals = materialization_summary(jobs)

# These are the facts the worker already publishes on the result envelope
# (worker_result.go): source.from_cache, transcript.reused,
# timings.total_wall_ms/work_ms/parallel, render.backend, gpu_copy_bytes,
# asset.drive_file_id. Aggregated here so the report shows them explicitly.
src_cache_hits = sum(1 for j in jobs if (j["result"].get("source") or {}).get("from_cache"))
transcript_reused = sum(1 for j in jobs if (j["result"].get("transcript") or {}).get("reused"))
prep_parallel = sum(1 for j in jobs if (phase(j, "prepare") or {}).get("parallel"))
backends = Counter(str((j["result"].get("render") or {}).get("backend") or "-") for j in jobs)
drive_file_ids = [str(a) for j in jobs for a in [(j["result"].get("asset") or {}).get("drive_file_id")] if a]
# gpu_copy_bytes: metrics_v2 is the authoritative source; the legacy
# render.gpu_copy_bytes key is only a pre-V2 fallback.
metrics_by_job = [
    ((j["result"].get("render") or {}).get("metrics_v2") or {})
    for j in jobs
]
# ResourceSampler persists canonical host/GPU observations in the primary
# media database. Read the persisted projection here; the benchmark never
# samples the machine itself and never turns missing values into zero.
resource_by_job = {}
resource_db = os.environ.get("BENCH_RESOURCE_DB", "")
resource_db = os.path.abspath(resource_db)
if os.path.isfile(resource_db):
    try:
        conn = sqlite3.connect(resource_db)
        columns = [
            "cpu_avg_pct", "cpu_peak_pct", "rss_peak_bytes", "gpu_avg_pct",
            "gpu_peak_pct", "vram_peak_bytes", "encoder_avg_pct",
            "decoder_avg_pct", "disk_read_bytes", "disk_write_bytes",
            "disk_util_pct", "io_wait_pct", "disk_queue_depth",
            "gpu_temp_peak_c", "throttled",
        ]
        sql = "SELECT job_id,%s FROM resource_observations WHERE job_id IN (%s)" % (
            ",".join(columns), ",".join("?" for _ in jobs))
        rows = conn.execute(sql, j_ids).fetchall()
        conn.close()
        grouped = {}
        for row in rows:
            grouped.setdefault(row[0], []).append(row[1:])
        for job_id, samples in grouped.items():
            aggregate = {"samples": len(samples)}
            for pos, name in enumerate(columns):
                values = [row[pos] for row in samples if row[pos] is not None]
                if not values:
                    continue
                if name.endswith("_avg_pct") or name in ("disk_util_pct", "io_wait_pct", "disk_queue_depth"):
                    aggregate[name] = sum(float(v) for v in values) / len(values)
                elif name == "throttled":
                    aggregate[name] = any(bool(v) for v in values)
                else:
                    aggregate[name] = max(values)
            resource_by_job[job_id] = aggregate
    except (OSError, sqlite3.Error):
        resource_by_job = {}

resource_rows = [resource_by_job.get(j_ids[i], {}) for i in range(n_jobs)]
def resource_avg(name):
    values = [float(r[name]) for r in resource_rows if isinstance(r.get(name), (int, float))]
    return sum(values) / len(values) if values else None
def resource_max(name):
    values = [float(r[name]) for r in resource_rows if isinstance(r.get(name), (int, float))]
    return max(values) if values else None
def resource_sum(name):
    values = [float(r[name]) for r in resource_rows if isinstance(r.get(name), (int, float))]
    return sum(values) if values else None
resource_summary = {
    "samples": sum(int(r.get("samples", 0)) for r in resource_rows),
    "cpu_avg_pct": resource_avg("cpu_avg_pct"),
    "cpu_peak_pct": resource_max("cpu_peak_pct"),
    "rss_peak_bytes": resource_max("rss_peak_bytes"),
    "gpu_avg_pct": resource_avg("gpu_avg_pct"),
    "gpu_peak_pct": resource_max("gpu_peak_pct"),
    "vram_peak_bytes": resource_max("vram_peak_bytes"),
    "encoder_avg_pct": resource_avg("encoder_avg_pct"),
    "decoder_avg_pct": resource_avg("decoder_avg_pct"),
    "disk_read_bytes": resource_max("disk_read_bytes"),
    "disk_write_bytes": resource_max("disk_write_bytes"),
    "disk_util_pct": resource_avg("disk_util_pct"),
    "io_wait_pct": resource_avg("io_wait_pct"),
    "disk_queue_depth": resource_avg("disk_queue_depth"),
    "gpu_temp_peak_c": resource_max("gpu_temp_peak_c"),
    "throttled": any(bool(r.get("throttled")) for r in resource_rows),
}

# Fine-grained renderer costs. Each value is taken from metrics_v2 exactly as
# emitted by the renderer. Missing instrumentation remains null with a zero
# measured-job count; it is never inferred from composite_ms.
render_cost_names = (
    "renderer_startup_ms", "probe_ms", "prepare_ms", "render_loop_ms",
    "decode_ms", "composite_ms", "frame_slot_wait_ms", "cuda_vulkan_wait_ms",
    "decoder_wait_ms", "encoder_backpressure_ms",
    "subtitle_compile_ms", "subtitle_raster_ms", "watermark_raster_ms",
    "frame_conversion_ms", "encode_ms", "mux_finalize_ms", "validation_ms",
    "audio_mux_ms", "renderer_finalize_ms", "drive_upload_ms", "render_wall_ms",
)
render_costs = {}
for name in render_cost_names:
    key = name
    values = [ms(m.get(key)) for m in metrics_by_job if sentinel_ok(m.get(key))]
    render_costs[name] = {
        "total_ms": sum(values) if values else None,
        "avg_ms": round(sum(values) / len(values), 2) if values else None,
        "measured_jobs": len(values),
        "status": "measured" if values else "NOT_INSTRUMENTED",
    }
# Resource metrics are sourced from each render's canonical metrics_v2.
# CPU is aggregated as user+system time; RSS is a batch high-water mark.
total_cpu_user_ms = sum(ms(m.get("cpu_user_ms")) for m in metrics_by_job if sentinel_ok(m.get("cpu_user_ms")))
total_cpu_system_ms = sum(ms(m.get("cpu_system_ms")) for m in metrics_by_job if sentinel_ok(m.get("cpu_system_ms")))
total_network_rx_bytes = sum(ms(m.get("network_rx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_rx_bytes")))
total_network_tx_bytes = sum(ms(m.get("network_tx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_tx_bytes")))
peak_cpu_percent = max((float(m.get("peak_cpu_percent", 0)) for m in metrics_by_job if isinstance(m.get("peak_cpu_percent"), (int, float))), default=0.0)

total_gpu_copy_bytes = sum(
    ms(metrics.get("gpu_copy_bytes"))
    for metrics in metrics_by_job
    if isinstance(metrics.get("gpu_copy_bytes"), (int, float)) and metrics.get("gpu_copy_bytes") >= 0
)
total_peak_rss_bytes = max(
    (ms(metrics.get("peak_rss_bytes")) for metrics in metrics_by_job
     if isinstance(metrics.get("peak_rss_bytes"), (int, float)) and metrics.get("peak_rss_bytes") >= 0),
    default=0,
)
total_disk_read_bytes = sum(
    ms(metrics.get("disk_read_bytes")) for metrics in metrics_by_job
    if isinstance(metrics.get("disk_read_bytes"), (int, float)) and metrics.get("disk_read_bytes") >= 0
)
total_disk_write_bytes = sum(
    ms(metrics.get("disk_write_bytes")) for metrics in metrics_by_job
    if isinstance(metrics.get("disk_write_bytes"), (int, float)) and metrics.get("disk_write_bytes") >= 0
)
total_network_rx_bytes = sum(ms(m.get("network_rx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_rx_bytes")))
total_network_tx_bytes = sum(ms(m.get("network_tx_bytes")) for m in metrics_by_job if sentinel_ok(m.get("network_tx_bytes")))


