# ── Emit JSON report ───────────────────────────────────────────────────────
fingerprint = {}
if fingerprint_file and os.path.isfile(fingerprint_file):
    try:
        with open(fingerprint_file) as f:
            fingerprint = json.load(f)
    except Exception:
        fingerprint = {"error": "failed to load fingerprint"}

jobs_out = []
for i in range(n_jobs):
    j = jobs[i]
    r = j["result"]
    render = r.get("render") or {}
    timing = j["timing"] or {}
    clip_render_wall = (phase(j, "render") or {}).get("wall_ms") or 0
    clip_overhead_ms = (j["wall_ms"] - clip_render_wall) if clip_render_wall > 0 else 0
    jobs_out.append({
        "job_id": j_ids[i],
        "label": j_labels[i],
        "mode": j_modes[i],
        "status": j_statuses[i],
        "assets": job_assets(j),
        "wall_ms": j["wall_ms"],
        "work_ms": j["work_ms"],            "timing_source": "job_runtime_run_report",
        "phases": j["phases"],
        "facts": {
            "transcript": r.get("transcript"),
            "materialization_summary": {
                asset_name: {
                    "wall_ms": ms((j["materialization"].get(asset_name) or {}).get("wall_ms")),
                    "work_ms": ms((j["materialization"].get(asset_name) or {}).get("work_ms")),
                    "size_bytes": ms((j["materialization"].get(asset_name) or {}).get("size_bytes")),
                    "download_bytes": ms((j["materialization"].get(asset_name) or {}).get("download_bytes")),
                    "cache_hit": bool((j["materialization"].get(asset_name) or {}).get("cache_hit", (j["materialization"].get(asset_name) or {}).get("from_cache", False))),
                }
                for asset_name in ("source", "watermark", "background")
                if j["materialization"].get(asset_name)
            },
            "timings": r.get("timings"),
            "materialization": j["materialization"],
            "subtitle_cache": j["subtitle_cache"],
            "llm": j["llm"],
            "source": r.get("source"),
            "render": {
                "backend": render.get("backend"),
                # metrics_v2 is the authoritative render metrics object;
                # legacy scalar fields are not copied as independent values.
                "metrics_v2": render.get("metrics_v2"),
                "render_wall_ms": (render.get("metrics_v2") or {}).get("render_wall_ms"),
                "duration_sec": render.get("duration_sec"),
                "size_bytes": render.get("size_bytes"),
                "audio_copy_eligible": render.get("audio_copy_eligible"),
                "audio_encode_passes": render.get("audio_encode_passes"),
                "subtitle_raster_cpu": (render.get("metrics_v2") or {}).get("subtitle_raster_cpu", render.get("subtitle_raster_cpu")),
                "gpu_copy_bytes": (render.get("metrics_v2") or {}).get("gpu_copy_bytes"),
            },
            "asset": r.get("asset"),
            "subtitles": r.get("subtitles"),
            "subtitle_cache": j["subtitle_cache"],
        },
        "per_clip": {
            "queue_wait_ms": j.get("queue_wait_ms") or 0,
            "prepare_wall_ms": (phase(j, "prepare") or {}).get("wall_ms", 0),
            "prepare_work_ms": (phase(j, "prepare") or {}).get("work_ms", 0),
            "subs_wall_ms": (phase(j, "subs") or {}).get("wall_ms", 0),
            "subs_work_ms": (phase(j, "subs") or {}).get("work_ms", 0),
            "render_wall_ms": (phase(j, "render") or {}).get("wall_ms", 0),
            "render_work_ms": (phase(j, "render") or {}).get("work_ms", 0),
            # overhead_ms = clip E2E wall − clip render wall: this clip's
            # out-of-render time (prepare, publish, scheduling — queue is
            # reported separately). The honest per-clip overhead figure;
            # not additive across clips when concurrency > 1.
            "overhead_ms": clip_overhead_ms,
            "upload_wall_ms": j.get("publish_wall_ms", 0),
            "upload_work_ms": j.get("publish_work_ms", 0),
            "total_wall_ms": j["wall_ms"],
            "total_work_ms": j["work_ms"],
            "critical_path_ms": j.get("critical_path_ms", 0),
            "publish_wall_ms": j.get("publish_wall_ms", 0),
            "publish_work_ms": j.get("publish_work_ms", 0),
            "bottleneck": job_bottleneck(j),
        },
        "run_report": {
            "wall_ms": timing.get("wall_ms"),
            "execution_wall_ms": timing.get("execution_wall_ms", timing.get("wall_ms")),
            "started_at": timing.get("started_at"),
            "finished_at": timing.get("finished_at"),
            "queue_wait_ms": timing.get("queue_wait_ms"),
            "attributed_ms": timing.get("attributed_ms"),
            "unattributed_ms": timing.get("unattributed_ms"),
            "unattributed_percent": timing.get("unattributed_percent"),
            "overlapped_ms": timing.get("overlapped_ms"),
            "bottleneck_stage": timing.get("bottleneck_stage"),
            "bottleneck_operation": timing.get("bottleneck_operation"),
            "bottleneck_percent": timing.get("bottleneck_percent"),
            "critical_path": timing.get("critical_path"),
            "stages": timing.get("stages"),
            "operations": timing.get("operations"),
            "fanout": timing.get("fanout"),
        },
        "bottleneck": {
            "phase": job_bottleneck(j),
            "critical_path_ms": max((p.get("critical_ms") or 0 for p in j["phases"]), default=0),
        },
        "critical_order": j["critical_order"],
    })

report = {
    "schema_version": "pipelinegen-benchmark-v3",
    "fingerprint": fingerprint,
    "git_sha": git_sha,
    "git_branch": git_branch,
    "config_sha": config_sha,
    "db_sha": db_sha,
    "worker_ids": worker_ids.split(",") if worker_ids else [],
    "base_url": base_url,
    "timestamp": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "summary": {
        "total_jobs": n_jobs,
        "queue": {
            "total_wait_ms": sum(j.get("queue_wait_ms", 0) for j in jobs),
            "max_wait_ms": max((j.get("queue_wait_ms", 0) for j in jobs), default=0),
            "average_wait_ms": round(sum(j.get("queue_wait_ms", 0) for j in jobs) / n_jobs, 2) if n_jobs else 0.0,
            "measured_jobs": sum(1 for j in jobs if j.get("queue_wait_ms", 0) > 0),
        },
        "succeeded": success_count,
        "failed": n_jobs - success_count,
        "total_assets": total_assets,
        "batch": {
            "batch_total_wall_ms": batch_wall,
            "batch_wall_source": batch_wall_source,
            "batch_total_work_ms": batch_work,
            "batch_render_work_ms": render_work_total,
            "batch_wall_minus_render_work_ms": (batch_wall - render_work_total) if batch_wall > 0 else 0,
            "derived_only": True,
            "derivation": {
                "batch_total_wall_ms": "parallel wall-clock: earliest runtime execution start → latest runtime execution finish; fallback = max per-clip E2E wall",
                "batch_wall_source": batch_wall_source,
                "batch_total_work_ms": "sum of per-clip E2E execution_wall_ms — sequential-equivalent, NOT wall",
                "batch_render_work_ms": "sum of render-phase work_ms — the cumulative FFmpeg work",
                "batch_wall_minus_render_work_ms": "wall − Σ render work; NOT the batch overhead when concurrency > 1 (clips overlap)",
                "batch_overhead_rule": "per-clip overhead = clip E2E wall − clip render wall (per_clip.overhead_ms)",
                "peak_concurrency": "max overlap of runtime execution spans",
                "average_concurrency": "time-weighted overlap over batch wall",
                "parallelism_factor": "Σ per-clip E2E execution wall / parallel wall",
            },
            "batch_critical_path_ms": batch_critical,
            "parallelism_factor": round(batch_work / batch_wall, 3) if batch_wall else 0.0,
            "parallelism_efficiency": round(batch_work / batch_wall, 3) if batch_wall else 0.0,
            "peak_running_clips": peak_concurrency,
            "average_running_clips": round(average_concurrency, 2),
            "bottleneck": {
                "phase": bottleneck,
                "critical_path_ms": phases_summary[bottleneck]["critical_path_ms"] if bottleneck else 0,
                "critical_share": phases_summary[bottleneck]["critical_share"] if bottleneck else 0.0,
            },
            "concurrency": {
                "peak_running_clips": peak_concurrency,
                "average_running_clips": round(average_concurrency, 2),
                "worker_slots": worker_slots if worker_slots > 0 else None,
                "concurrency_utilization_percent": concurrency_utilization,
            },
            "source": "job_runtime_run_reports_only",
            "derived_only": True,
        },
        "phases": phases_summary,
        "stage_timing": {
            # Submission round-trip is transport bookkeeping, not a render
            # phase and is intentionally excluded from SSOT-derived timing.
            "submit_ms": 0,
            "generate_ms": phases_summary.get("llm", {}).get("wall_ms", 0),
            "render_ms": phases_summary.get("render", {}).get("wall_ms", 0),
            "drive_ms": phases_summary.get("drive", {}).get("wall_ms", 0),
            "wall_clock_ms": batch_wall,
        },
        "per_job": {"min_ms": min_t, "max_ms": max_t, "avg_ms": avg_t},
        "worker_facts": {
            "source_cache_hits": src_cache_hits,
            "transcript_reused": transcript_reused,
            "prep_parallel": prep_parallel,
            "backends": dict(backends),
            "drive_file_ids": drive_file_ids,
            "gpu_copy_bytes_total": total_gpu_copy_bytes,
            "render_costs": render_costs,
            "throughput": {
                "clips_per_minute": (n_jobs / (batch_wall / 60000.0)) if batch_wall > 0 else 0.0,
                # batch_speed_factor = video seconds produced / batch wall
                # (>1 = faster than realtime). pipeline_rtf is a legacy alias
                # of the same number — it is NOT an xRT-style factor; the
                # true inverse (wall / video, <1 = faster) is batch_xrt.
                "batch_speed_factor": (source_seconds / (batch_wall / 1000.0)) if batch_wall > 0 else 0.0,
                "batch_xrt": ((batch_wall / 1000.0) / source_seconds) if source_seconds > 0 else 0.0,
                "pipeline_rtf": (source_seconds / (batch_wall / 1000.0)) if batch_wall > 0 else 0.0,
            },
            "resources": {
                "cpu_user_ms": total_cpu_user_ms,
                "cpu_system_ms": total_cpu_system_ms,
                "peak_cpu_percent": peak_cpu_percent,
                "peak_rss_bytes": total_peak_rss_bytes,
                "disk_read_bytes": total_disk_read_bytes,
                "disk_write_bytes": total_disk_write_bytes,
                "network_rx_bytes": total_network_rx_bytes,
                "network_tx_bytes": total_network_tx_bytes,
                **resource_summary,
            },
            "peak_rss_bytes_max": total_peak_rss_bytes,
            "disk_read_bytes_total": total_disk_read_bytes,
            "disk_write_bytes_total": total_disk_write_bytes,
            "materialization": materialization_totals,
            "drive": {
                "download_wall_ms": sum(j["drive"].get("download_wall_ms", 0) for j in jobs),
                "download_work_ms": sum(j["drive"].get("download_work_ms", 0) for j in jobs),
                "upload_wall_ms": sum(j["drive"].get("upload_wall_ms", 0) for j in jobs),
                "upload_work_ms": sum(j["drive"].get("upload_work_ms", 0) for j in jobs),
                "google_doc_publish_wall_ms": sum(j["drive"].get("document_publish_wall_ms", 0) for j in jobs),
                "google_doc_publish_work_ms": sum(j["drive"].get("document_publish_work_ms", 0) for j in jobs),
            },
            "source_materialize_ms": materialization_totals["source"]["wall_ms"],
            "watermark_materialize_ms": materialization_totals["watermark"]["wall_ms"],
            "background_materialize_ms": materialization_totals["background"]["wall_ms"],
            "download_bytes": {
                "source": materialization_totals["source"]["download_bytes"],
                "watermark": materialization_totals["watermark"]["download_bytes"],
                "background": materialization_totals["background"]["download_bytes"],
                "total": sum(m["download_bytes"] for m in materialization_totals.values()),
            },
            "cache_hits": {
                "source": materialization_totals["source"]["cache_hits"],
                "watermark": materialization_totals["watermark"]["cache_hits"],
                "background": materialization_totals["background"]["cache_hits"],
                "total": sum(m["cache_hits"] for m in materialization_totals.values()),
            },
        },
    },
    "jobs": jobs_out,
}

with open(out_file, "w") as f:
    json.dump(report, f, indent=2)
    f.write("\n")

print(f"[bench] Report written to {out_file}")
