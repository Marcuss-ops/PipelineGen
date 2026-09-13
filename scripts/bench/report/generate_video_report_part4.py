# ── Console report ─────────────────────────────────────────────────────────
print("")
print("════════════════════════════════════════════════════════════════")
print("  PIPELINEGEN BENCHMARK REPORT — WALL / WORK / CRITICAL PATH")
print("════════════════════════════════════════════════════════════════")
print("")
print("  %-40s %s" % ("Git SHA:", git_sha[:12]))
print("  %-40s %s" % ("Git branch:", git_branch))
print("  %-40s %s" % ("Config SHA:", config_sha[:12]))
print("  %-40s %s" % ("DB SHA:", db_sha[:12]))
print("  %-40s %s" % ("Worker IDs:", worker_ids or "<none>"))
print("  %-40s %s" % ("Base URL:", base_url))
print("  %-40s %s" % ("Timestamp:", datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")))
print("")
print("  %-40s %d / %d" % ("Jobs succeeded:", success_count, n_jobs))
print("  %-40s %d" % ("Total assets:", total_assets))
print("")
print("  ── Batch (wall / work / critical path / concurrency) ──")
print("  %-40s %s" % ("batch_parallel_wall_ms:", f"{batch_wall:,}"))
print("  %-40s %s" % ("batch_wall_source:", batch_wall_source))
print("  %-40s %s" % ("batch_total_work_ms:", f"{batch_work:,}"))
print("  %-40s %s" % ("batch_render_work_ms:", f"{render_work_total:,}"))
if batch_wall > 0:
    print("  %-40s %s" % ("batch_wall_minus_render_work_ms:", f"{batch_wall - render_work_total:,}"))
print("  %-40s %s" % ("overhead_rule:", "wall − Σ render work is NOT overhead when concurrency > 1; per-clip overhead = E2E − RENDER (per-clip table)"))
print("  %-40s %s" % ("batch_critical_path_ms:", f"{batch_critical:,}"))
if bottleneck:
    print("  %-40s %s @ %.1f%%" % (
        "batch bottleneck (critical path):", bottleneck, phases_summary[bottleneck]["critical_share"]))
if batch_wall > 0:
    print("  %-40s %.2fx" % ("parallelism_factor (Σ clip E2E / wall):", batch_work / batch_wall))
print("  %-40s %d" % ("peak_running_clips:", peak_concurrency))
print("  %-40s %.1f" % ("average_running_clips:", average_concurrency))
print("  %-40s %s" % ("queue_wait_total_ms:", f"{sum(j.get('queue_wait_ms', 0) for j in jobs):,}"))
print("  %-40s %s" % ("queue_wait_max_ms:", f"{max((j.get('queue_wait_ms', 0) for j in jobs), default=0):,}"))
if worker_slots > 0:
    print("  %-40s %d" % ("worker_slots:", worker_slots))
    print("  %-40s %.1f%%" % ("concurrency_utilization (peak/slots):", concurrency_utilization))
else:
    print("  %-40s %s" % ("worker_slots:", "- (set BENCH_WORKER_SLOTS to compare)"))
print("")
print("  ── Phase breakdown ──")
print("  wall = Σ per-job wall (sequential-equivalent); work = Σ measured work;")
print("  wall = elapsed phase time; work = accumulated operation work (parallel work may exceed wall).")
print("  critical_path = wall occupancy on the batch critical path; shares use this denominator.")
print("  The bottleneck is the largest critical-path contribution, never the largest work total.")
print("  %-12s %10s %10s %12s %8s %5s" % ("phase", "work_ms", "wall_ms", "critical_ms", "share", "jobs"))
print("  %-12s %10s %10s %12s %8s %5s" % ("─────", "───────", "───────", "────────────", "─────", "────"))
for name in sorted(phases_summary):
    p = phases_summary[name]
    print("  %-12s %10s %10s %12s %7.1f%% %5d" % (
        name, f"{p['work_ms']:,}", f"{p['wall_ms']:,}", f"{p['critical_path_ms']:,}",
        p["critical_share"], p["jobs"]))
print("")
print("  ── Throughput / resources ──")
source_seconds = sum(float((j["result"].get("render") or {}).get("duration_sec") or 0) for j in jobs)
print("  %-40s %.2f" % ("clips_per_minute:", (n_jobs / (batch_wall / 60000.0)) if batch_wall > 0 else 0.0))
print("  %-40s %.3fx" % ("batch_speed_factor (video/wall, >1 faster):", (source_seconds / (batch_wall / 1000.0)) if batch_wall > 0 else 0.0))
print("  %-40s %.3fx" % ("batch_xrt (wall/video, <1 faster):", ((batch_wall / 1000.0) / source_seconds) if source_seconds > 0 else 0.0))
print("  %-40s %s" % ("cpu_user_ms:", f"{total_cpu_user_ms:,}" if total_cpu_user_ms else "-"))
print("  %-40s %s" % ("cpu_system_ms:", f"{total_cpu_system_ms:,}" if total_cpu_system_ms else "-"))
print("  %-40s %s" % ("peak_cpu_percent:", f"{peak_cpu_percent:.1f}" if peak_cpu_percent else "-"))
print("  %-40s %s" % ("peak_rss_bytes:", f"{total_peak_rss_bytes:,}" if total_peak_rss_bytes else "-"))
print("  %-40s %s" % ("disk_read_bytes:", f"{total_disk_read_bytes:,}" if total_disk_read_bytes else "-"))
print("  %-40s %s" % ("disk_write_bytes:", f"{total_disk_write_bytes:,}" if total_disk_write_bytes else "-"))
for key in ("cpu_avg_pct", "cpu_peak_pct", "gpu_avg_pct", "gpu_peak_pct", "vram_peak_bytes", "encoder_avg_pct", "decoder_avg_pct", "disk_util_pct", "io_wait_pct", "disk_queue_depth", "gpu_temp_peak_c"):
    value = resource_summary.get(key)
    print("  %-40s %s" % (key + ":", "-" if value is None else (f"{value:,.1f}" if isinstance(value, float) else f"{value:,}")))
print("")
print("  ── Per-job (real worker facts) ──")
print("  %-16s %-9s %-11s %-16s %13s %15s %8s %10s %8s %10s" % (
    "JOB_ID", "MODE", "STATUS", "BACKEND", "PREP(w/work)", "RENDER(w/work)", "DRIVE", "TOTAL", "TRANSCR", "GPU_COPY"))
print("  %-16s %-9s %-11s %-16s %13s %13s %8s %10s %8s %10s" % (
    "──────", "────", "───────────", "──────", "───────────", "───────────", "─────", "─────", "───────", "────────"))
for i in range(n_jobs):
    j = jobs[i]
    r = j["result"]
    render = r.get("render") or {}
    trans = r.get("transcript") or {}
    prep = phase(j, "prepare")
    ren = phase(j, "render")
    drv = phase(j, "drive")
    prep_s = (fmt_work(prep) if prep else "-")
    ren_s = (fmt_work(ren) if ren else "-")
    drv_s = fmt_sec(drv["wall_ms"]) if drv and drv["wall_ms"] else "-"
    trans_s = "-"
    if trans:
        tag = "reuse" if trans.get("reused") else "gen"
        trans_s = "%s/%s/%s" % (tag, trans.get("language", "?"), trans.get("cues", "?"))
    gpu = (render.get("metrics_v2") or {}).get("gpu_copy_bytes")
    if gpu is None:
        gpu = render.get("gpu_copy_bytes")  # legacy fallback (pre-V2 servers)
    gpu_s = f"{ms(gpu):,}" if isinstance(gpu, (int, float)) else "-"
    wall_s = fmt_sec(j["wall_ms"])
    backend = render.get("backend") or "-"
    print("  %-16s %-9s %-11s %-16.16s %-13s %-13s %8s %10s %8s %10s" % (
        (j_ids[i] or "N/A")[:14], j_modes[i], j_statuses[i], str(backend),
        prep_s, ren_s, drv_s, wall_s, trans_s, gpu_s))


print("")
print("  ── Per-clip table (Queue / Prepare / Subs / Render / Upload / Total) ──")
print("  Queue = queue_wait_ms (RunReport); Prepare = clip.prepare wall;")
print("  Subs = clip.subtitles wall; Render = clip.render+probe+overlay wall;")
print("  Publish = clip.publish wall/work; Queue is excluded from execution total and shown separately.")
print("  OVERHEAD = TOTAL wall − RENDER wall: this clip's time outside the render;")
print("  it is per-clip (clips overlap), never additive into a batch total.")
print("  BOTTLENECK = largest critical-path phase on this clip.")
print("  %-14s %8s %15s %14s %15s %15s %15s %15s %15s %-10s" % ("JOB_ID", "QUEUE", "PREP wall/work", "SUBS wall/work", "RENDER wall/work", "PUBLISH wall/work", "TOTAL wall/work", "OVERHEAD", "CRITICAL", "BOTTLENECK"))
print("  %-14s %8s %15s %14s %15s %15s %15s %15s %15s %-10s" % ("──────", "─────", "───────────────", "──────────────", "───────────────", "───────────────", "───────────────", "────────", "──────", "──────────"))
for i in range(n_jobs):
    j = jobs[i]
    prep = phase(j, "prepare")
    subs = phase(j, "subs")
    ren = phase(j, "render")
    drv = phase(j, "drive")
    # Only clip.render jobs carry the serial clip phases; generate jobs have
    # no per-clip queue/prepare/render breakdown (their phases are llm/tts/
    # audio/finalize and stay in the phase breakdown table).
    if not prep and not subs and not ren and not drv:
        continue
    queue_s = fmt_sec(j.get("queue_wait_ms") or 0)
    prep_s = fmt_work(prep) if prep else "-"
    subs_s = fmt_work(subs) if subs else "-"
    ren_s = fmt_work(ren) if ren else "-"
    drv_s = fmt_work(drv) if drv else "-"
    total_s = "%s/%s" % (fmt_sec(j["wall_ms"]), fmt_sec(j["work_ms"]))
    overhead_s = fmt_sec(j["wall_ms"] - ren["wall_ms"]) if ren and ren["wall_ms"] > 0 else "-"
    critical_s = fmt_sec(j.get("critical_path_ms", 0))
    print("  %-14s %8s %15s %14s %15s %15s %15s %15s %15s %-10s" % (
        (j_ids[i] or "N/A")[:12], queue_s, prep_s, subs_s, ren_s, drv_s, total_s,
        overhead_s, critical_s, job_bottleneck(j)[:10]))


# ── Render phase split (metrics_v2, measured by the renderer) ──────────────
# The Rust boundary now splits the coarse ffmpeg wall into probe / decode /
# composite / encode (bench_all per-frame sums + the filter-graph residual).
# Only rows with at least one measured phase are shown; the rest stay
# NOT_INSTRUMENTED on the wire and "-" here.
render_rows = []
for i in range(n_jobs):
    j = jobs[i]
    m = (j["result"].get("render") or {}).get("metrics_v2") or {}
    if not any(sentinel_ok(m.get(k)) for k in ("probe_ms", "decode_ms", "composite_ms", "encode_ms")):
        continue
    cell = lambda k: fmt_sec(ms(m.get(k))) if sentinel_ok(m.get(k)) else "-"
    render_rows.append((
        (j_ids[i] or "N/A")[:12],
        cell("probe_ms"), cell("decode_ms"), cell("composite_ms"),
        cell("encode_ms"), cell("render_wall_ms")))
if render_rows:
    print("")
    print("  ── Render phase split (metrics_v2, measured by the renderer) ──")
    print("  probe = ffprobe wall; decode/encode = per-frame bench sums;")
    print("  composite = filter-graph residual (subtitles+watermark+compositing+mux);")
    print("  render_wall = worker-measured render wall (selection + execution).")
    print("  %-14s %8s %8s %10s %8s %12s" % ("JOB_ID", "PROBE", "DECODE", "COMPOSITE", "ENCODE", "RENDER_WALL"))
    print("  %-14s %8s %8s %10s %8s %12s" % ("──────", "─────", "──────", "─────────", "──────", "───────────"))
    for row in render_rows:
        print("  %-14s %8s %8s %10s %8s %12s" % row)

print("")
print("  ── Renderer cost totals (metrics_v2) ──")
print("  total_ms = sum across clips; avg_ms = average per measured clip;")
print("  composite includes the current subtitle/watermark full-frame compositor;")
print("  subtitle_raster/watermark_raster are shown as unavailable until separately instrumented.")
print("  %-28s %12s %12s %10s %-18s" % ("COST", "TOTAL_MS", "AVG_MS", "CLIPS", "STATUS"))
print("  %-28s %12s %12s %10s %-18s" % ("────", "────────", "──────", "─────", "──────"))
for name, cost in render_costs.items():
    total = str(cost["total_ms"]) if cost["total_ms"] is not None else "-"
    avg = str(cost["avg_ms"]) if cost["avg_ms"] is not None else "-"
    print("  %-28s %12s %12s %10d %-18s" % (name, total, avg, cost["measured_jobs"], cost["status"]))


print("")
print("  ── Worker facts (source / timings / asset / bottleneck, from job result) ──")
print("  SRC_CACHE = source.from_cache; PREP_PAR = timings.parallel;")
print("  DRIVE_FILE = asset.drive_file_id (published MP4 on Drive);")
print("  BOTTLENECK = phase with the largest critical-path contribution in this job.")
print("  %-14s %-10s %-9s %-20s %-10s" % ("JOB_ID", "SRC_CACHE", "PREP_PAR", "DRIVE_FILE_ID", "BOTTLENECK"))
print("  %-14s %-10s %-9s %-20s %-10s" % ("──────", "─────────", "────────", "─────────────", "──────────"))
for i in range(n_jobs):
    j = jobs[i]
    r = j["result"]
    src = r.get("source") or {}
    prep = phase(j, "prepare")
    asset = r.get("asset") or {}
    src_s = "cache" if src.get("from_cache") else ("miss" if src else "-")
    par_s = "yes" if (prep and prep.get("parallel")) else ("no" if prep else "-")
    dfid = asset.get("drive_file_id") or "-"
    print("  %-14s %-10s %-9s %-20.20s %-10s" % (
        (j_ids[i] or "N/A")[:12], src_s, par_s, str(dfid), job_bottleneck(j)[:10]))


print("")
print("  ── Batch worker facts (aggregates) ──")
print("  %-40s %s" % ("source cache hits:", f"{src_cache_hits}/{n_jobs}"))
print("  %-40s %s" % ("transcript reused:", f"{transcript_reused}/{n_jobs}"))
print("  %-40s %s" % ("prep parallel:", f"{prep_parallel}/{n_jobs}"))
print("  %-40s %s" % ("backends:", ", ".join(f"{k} x{v}" for k, v in backends.most_common()) or "-"))
print("  %-40s %d" % ("drive files published:", len(drive_file_ids)))
print("  %-40s %s" % ("gpu_copy_bytes total:", f"{total_gpu_copy_bytes:,}" if total_gpu_copy_bytes else "-"))
print("  %-40s %s" % ("peak_rss_bytes max:", f"{total_peak_rss_bytes:,}" if total_peak_rss_bytes else "-"))
print("  %-40s %s" % ("disk_read_bytes total:", f"{total_disk_read_bytes:,}" if total_disk_read_bytes else "-"))
print("  %-40s %s" % ("disk_write_bytes total:", f"{total_disk_write_bytes:,}" if total_disk_write_bytes else "-"))
print("")
print("  ── Drive / Google Doc ──")
print("  Download = Drive reads/materialization; Upload = clip artifacts; Document = Google Doc publication.")
for label, key in (("drive download wall/work", "download"), ("drive upload wall/work", "upload"), ("google doc publish wall/work", "document_publish")):
    wall = sum(j["drive"].get(f"{key}_wall_ms", 0) for j in jobs)
    work = sum(j["drive"].get(f"{key}_work_ms", 0) for j in jobs)
    print("  %-40s %s" % (f"{label}:", f"{wall:,}/{work:,} ms"))
print("")
print("  ── Materialization aggregates (SSOT) ──")
print("  asset_materialize_ms remains available in metrics_v2; these are per-asset totals.")
for asset_name in ("source", "watermark", "background"):
    m = materialization_totals[asset_name]
    print("  %-40s %s" % (f"{asset_name} wall/work:", f"{m['wall_ms']:,}/{m['work_ms']:,} ms" if m["jobs"] else "-"))
    print("  %-40s %s" % (f"{asset_name} size bytes:", f"{m['size_bytes']:,}" if m["jobs"] else "-"))
    print("  %-40s %s" % (f"{asset_name} download bytes:", f"{m['download_bytes']:,}" if m["jobs"] else "-"))
    print("  %-40s %s" % (f"{asset_name} cache hits/misses:", f"{m['cache_hits']}/{m['cache_misses']}" if m["jobs"] else "-"))


print("")
print("  ── LLM (Ollama split, from operation metadata) ──")
print("  wall/work/queue from the generate operation; load/eval/tokens from Ollama.")
print("  cold_start is a count (0/1 per call); model is 'mixed' when calls differ.")
print("  %-16s %5s %-12s %8s %9s %10s %12s %10s %8s %6s %6s" % (
    "JOB_ID", "CALLS", "MODEL", "QUEUE", "LOAD_MS", "PROMPT_MS", "INF_WALL/WORK", "TOK_IN/OUT", "TOK/S", "COLD", "WARM"))
print("  %-16s %5s %-12s %8s %9s %10s %12s %10s %8s %6s %6s" % (
    "──────", "─────", "────", "─────", "───────", "─────────", "────────────", "─────────", "─────", "────", "────"))
for i in range(n_jobs):
    llm = jobs[i]["llm"]
    if not llm or not llm.get("calls"):
        print("  %-16s %5s %-12s %8s %9s %10s %12s %10s %8s %6s %6s" % (
            (j_ids[i] or "N/A")[:14], "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"))
        continue
    calls = llm["calls"]
    cold = llm["cold_start"]
    warm = max(0, calls - cold)
    inf = "%s/%s" % (fmt_sec(llm["inference_wall_ms"]), fmt_sec(llm["inference_work_ms"]))
    toks = "%s/%s" % (f"{llm['input_tokens']:,}" if llm["input_tokens"] else "-",
                      f"{llm['output_tokens']:,}" if llm["output_tokens"] else "-")
    print("  %-16s %5d %-12.12s %8s %9s %10s %12s %10s %8s %6d %6d" % (
        (j_ids[i] or "N/A")[:14], calls, llm["model"],
        fmt_sec(llm["queue_wait_ms"]), fmt_sec(llm["model_load_ms"]),
        fmt_sec(llm["prompt_eval_ms"]), inf, toks,
        f"{llm['tokens_per_second']:.0f}" if llm["tokens_per_second"] else "-", cold, warm))
print("")
print("  ── Materialization (PhaseTiming per asset) ──")
print("  wall/work, download bytes and cache flags come from the materializer SSOT.")
print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % ("JOB_ID", "ASSET", "WALL/WORK", "CACHE", "SIZE", "DOWNLOAD", "ASSET_ID"))
print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % ("──────", "─────", "─────────", "─────", "────", "────────", "────────"))
for i in range(n_jobs):
    mat = jobs[i]["materialization"]
    if not mat:
        print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % (
            (j_ids[i] or "N/A")[:14], "-", "-", "-", "-", "-", "-"))
        continue
    for asset in ("source", "watermark", "background"):
        m = mat.get(asset)
        if not m:
            continue
        cache_s = "hit" if m["from_cache"] else "miss"
        bytes_s = f"{m['size_bytes']:,}" if m["size_bytes"] > 0 else "-"
        download_s = f"{m['download_bytes']:,}" if m["download_bytes"] > 0 else "0"
        print("  %-16s %-11s %-13s %-6s %12s %12s %12s" % (
            (j_ids[i] or "N/A")[:14], asset,
            fmt_work(m), cache_s, bytes_s, download_s, (m.get("asset_id") or "-")[:12]))
print("")
print("  ── Min / Max / Avg per-job wall ──")
print("  %-40s %s" % ("Min:", fmt_sec(min_t)))
print("  %-40s %s" % ("Max:", fmt_sec(max_t)))
print("  %-40s %s" % ("Avg:", fmt_sec(avg_t)))
print("")
print("════════════════════════════════════════════════════════════════")

