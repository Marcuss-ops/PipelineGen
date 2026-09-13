def build_job(full, i):
    result = full.get("result") or {}
    timing = full.get("timing") or {}
    phases = []

    # ── RunReport facts (LLM / tts / audio / drive / finalize) ──────────
    stages = {}
    for s in timing.get("stages") or []:
        stages[s.get("name", "")] = ms(s.get("duration_ms"))
    cp = {}
    cp_order = []
    for c in timing.get("critical_path") or []:
        cp[c.get("name", "")] = ms(c.get("duration_ms"))
        cp_order.append(c.get("name", ""))
    llm_work = llm_calls = 0
    tts_work = tts_calls = 0
    audio_work = 0
    drive_work = 0
    drive_download_ms = drive_download_work = drive_upload_ms = drive_upload_work = 0
    doc_publish_ms = doc_publish_work = 0
    # publish_wall is assigned by the clip.render envelope branch; default it
    # so a job with no render facts (missing result file, e.g.) never crashes
    # the report at the drive dict.
    publish_wall = 0
    for op in timing.get("operations") or []:
        comp = op.get("component", "")
        opname = op.get("operation", "")
        w = ms(op.get("work_ms"))
        c = ms(op.get("calls"))
        if comp == "ollama":
            llm_work += w
            llm_calls += c
        elif comp == "tts":
            tts_work += w
            tts_calls += c
        elif comp == "rust" and opname == "audio_render":
            audio_work += w
        elif comp == "audio" and opname in ("audio_plan_compile", "mix", "aac_encode", "probe", "hash"):
            audio_work += w
        elif comp in ("drive", "google_docs") or opname in ("upload", "publish"):
            drive_work += w
            if opname in ("download", "fetch", "materialize"):
                drive_download_ms += ms(op.get("duration_ms"))
                drive_download_work += w
            elif comp == "google_docs" or opname in ("document.publish", "doc_publish"):
                doc_publish_ms += ms(op.get("duration_ms"))
                doc_publish_work += w
            else:
                drive_upload_ms += ms(op.get("duration_ms"))
                drive_upload_work += w
    # Ollama split facts come from the operation metadata merged by
    # TimingOperation (numeric values summed across calls, cold_start counted,
    # model kept when uniform). queue_wait_ms is the accumulated queue wait.
    llm = {}
    for op in timing.get("operations") or []:
        if op.get("component") == "ollama" and op.get("operation") == "generate":
            meta = op.get("metadata") or {}
            model = meta.get("model")
            if not isinstance(model, str):
                model = ""
            llm = {
                "calls": ms(op.get("calls")),
                "queue_wait_ms": ms(op.get("queue_wait_ms")),
                "work_ms": ms(op.get("work_ms")),
                "model": model,
                "input_tokens": ms(meta.get("input_tokens")),
                "output_tokens": ms(meta.get("output_tokens")),
                "model_load_ms": ms(meta.get("model_load_ms")),
                "prompt_eval_ms": ms(meta.get("prompt_eval_ms")),
                "inference_wall_ms": ms(meta.get("inference_wall_ms")),
                "inference_work_ms": ms(meta.get("inference_work_ms")),
                "tokens_per_second": ms(meta.get("tokens_per_second")),
                "cold_start": ms(meta.get("cold_start")),
            }
            break
    if "generate" in stages or llm_work > 0:
        phases.append({"name": "llm", "wall_ms": stages.get("generate", 0), "work_ms": llm_work,
                       "critical_ms": cp.get("generate", 0), "calls": llm_calls, "parallel": False})
    if "tts" in stages or tts_work > 0:
        phases.append({"name": "tts", "wall_ms": stages.get("tts", 0), "work_ms": tts_work,
                       "critical_ms": cp.get("tts", 0), "calls": tts_calls, "parallel": False})
    if "audio_compile" in stages:
        phases.append({"name": "audio", "wall_ms": stages.get("audio_compile", 0), "work_ms": audio_work,
                       "critical_ms": cp.get("audio_compile", 0), "calls": 0, "parallel": False})
    if drive_work > 0 or stages.get("document.publish", 0) > 0:
        phases.append({"name": "drive", "wall_ms": stages.get("document.publish", 0) or stages.get("document", 0),
                       "work_ms": drive_work, "critical_ms": cp.get("document.publish", 0) or cp.get("document", 0),
                       "calls": 0, "parallel": False})
    if "post_writer_finalize" in stages:
        # post_writer_finalize is a top-level serial stage (recorded by the
        # worker runner after the handler returns), so its wall IS its
        # critical-path contribution; the RunReport critical path is only
        # consulted when present.
        fw = stages.get("post_writer_finalize", 0)
        phases.append({"name": "finalize", "wall_ms": fw, "work_ms": 0,
                       "critical_ms": cp.get("post_writer_finalize", fw), "calls": 0, "parallel": False})

    # ── clip.render result facts (prepare / render / drive) ─────────────
    timings = result.get("timings") or {}
    render = result.get("render") or {}
    metrics = render.get("metrics_v2") or {}
    # The RunReport clip.* stages (recorded by the worker since the
    # observability instrumentation) are the SSOT for the clip.render serial
    # chain: clip.prepare → clip.subtitles → clip.render → clip.probe →
    # clip.overlay → clip.publish. Each stage's wall is its critical-path
    # contribution. Fall back to the result envelope only for servers that
    # predate the stage instrumentation.
    clip_stages = {s.get("name", ""): ms(s.get("duration_ms")) for s in timing.get("stages") or []
                   if str(s.get("name", "")).startswith("clip.")}
    if clip_stages:
        prep_wall = clip_stages.get("clip.prepare", 0)
        prep_work = ms(timings.get("total_work_ms"))
        if prep_wall > 0 or prep_work:
            phases.append({"name": "prepare", "wall_ms": prep_wall, "work_ms": prep_work,
                           "critical_ms": prep_wall, "calls": 0, "parallel": bool(timings.get("parallel"))})
        if clip_stages.get("clip.subtitles", 0) > 0:
            phases.append({"name": "subs", "wall_ms": clip_stages["clip.subtitles"], "work_ms": 0,
                           "critical_ms": clip_stages["clip.subtitles"], "calls": 0, "parallel": False})
        # Render-side serial chain: render + probe + overlay certification are
        # one contiguous wall span inside the worker.
        render_wall = (clip_stages.get("clip.render", 0) + clip_stages.get("clip.probe", 0)
                       + clip_stages.get("clip.overlay", 0))
        if render_wall > 0 or render:
            phases.append({"name": "render", "wall_ms": render_wall, "work_ms": render_work(metrics),
                           "critical_ms": render_wall, "calls": 0, "parallel": False})
        if clip_stages.get("clip.publish", 0) > 0:
            pw = clip_stages["clip.publish"]
            phases.append({"name": "drive", "wall_ms": pw, "work_ms": pw,
                           "critical_ms": pw, "calls": 0, "parallel": False})
    elif timings or render:
        prep_wall = ms(timings.get("total_wall_ms"))
        prep_work = ms(timings.get("total_work_ms"))
        publish_wall = ms(metrics.get("publication_total_ms")) if sentinel_ok(metrics.get("publication_total_ms")) else 0
        if publish_wall == 0:
            # Legacy fallback: publish_ms is the deprecated Rust-side local
            # finalize rename, never the publication wall.
            publish_wall = ms(metrics.get("renderer_finalize_ms")) if sentinel_ok(metrics.get("renderer_finalize_ms")) else ms(metrics.get("publish_ms")) if sentinel_ok(metrics.get("publish_ms")) else 0
        # render_wall_ms is exposed both on the render block (worker_result.go)
        # and inside metrics_v2 — the same worker-owned value; prefer the V2
        # report and keep the render block as the legacy fallback.
        rw = ms(metrics.get("render_wall_ms")) if sentinel_ok(metrics.get("render_wall_ms")) else None
        if rw is None:
            rw = ms(render.get("render_wall_ms")) if sentinel_ok(render.get("render_wall_ms")) else None
        if prep_wall or prep_work:
            # prepare / render / drive are strictly sequential inside the
            # clip.render worker (preparer → renderer → publisher), so each
            # phase's measured wall IS its critical-path contribution within
            # the job — never 0 and never the accumulated work.
            phases.append({"name": "prepare", "wall_ms": prep_wall, "work_ms": prep_work,
                           "critical_ms": prep_wall, "calls": 0, "parallel": bool(timings.get("parallel"))})
        if render or metrics:
            if rw is None:
                # Fallback for servers without render_wall_ms: derive from the
                # job wall minus the render phase + publish walls (includes
                # backend selection + unaccounted).
                job_wall = ms(timing.get("execution_wall_ms", timing.get("wall_ms")))
                rw = max(0, job_wall - prep_wall - publish_wall)
            phases.append({"name": "render", "wall_ms": rw, "work_ms": render_work(metrics),
                           "critical_ms": rw, "calls": 0, "parallel": False})
        if publish_wall > 0:
            phases.append({"name": "drive", "wall_ms": publish_wall, "work_ms": publish_wall,
                           "critical_ms": publish_wall, "calls": 0, "parallel": False})

    # Order phases by the job's REAL critical path.
    # - clip.render jobs: the worker serial chain is prepare → render →
    #   drive → post_writer_finalize (enforced explicitly — the RunReport
    #   cp_order for such jobs only contains post_writer_finalize and would
    #   otherwise sort it first).
    # - RunReport-backed jobs (script.generate): use the ordered chain of
    #   top-level sequential stages from the RunReport critical path.
    #   (prepare/render exist only on clip.render jobs, so they are the
    #   discriminator — a generate job's "drive" phase must NOT trigger the
    #   serial reorder.)
    if any(p["name"] in ("prepare", "render") for p in phases):
        serial = ["prepare", "subs", "render", "drive", "finalize"]
        phases.sort(key=lambda p: serial.index(p["name"]) if p["name"] in serial else 10 ** 9)
    elif cp_order:
        rank = {name: k for k, name in enumerate(cp_order)}
        phases.sort(key=lambda p: rank.get(p["name"], 10 ** 9))

    mat = materialization(timings, result)
    subtitles = result.get("subtitles") or {}
    cache_facts = {}
    if "content_cache_hit" in subtitles:
        cache_facts["content_cache_hit"] = bool(subtitles["content_cache_hit"])
    if "artifact_cache_hit" in subtitles:
        cache_facts["artifact_cache_hit"] = bool(subtitles["artifact_cache_hit"])
    cache_facts["measured"] = bool(cache_facts)
    # Prefer the worker's explicit materialization report. The phase fallback
    # is retained only for older job results.
    explicit_materialization = result.get("materialization") or {}
    if explicit_materialization:
        mat = explicit_materialization

    # The benchmark must not invent a wall timer from local polling clocks.
    # Prefer the server-owned RunReport wall; the job lifecycle timestamps are
    # retained only as a compatibility fallback for older responses.
    wall = ms(timing.get("wall_ms"))
    if wall <= 0:
        wall = ms(timing.get("execution_wall_ms", timing.get("wall_ms")))
    work = sum(p["work_ms"] for p in phases)
    # critical_order is the per-job serial execution chain used to place
    # phases on the batch critical path: the RunReport critical path for
    # generate jobs, the measured serial chain for clip.render jobs.
    critical_order = [p["name"] for p in phases]
    queue_wait = ms(timing.get("queue_wait_ms"))
    critical_path = sum((p.get("critical_ms") or 0) for p in phases)
    publish = phase({"phases": phases}, "drive")
    # Current /full responses expose these on the job envelope as RFC3339;
    # older workers used *_at_ms inside timing. Accept both representations.
    job_meta = full.get("job") or {}
    runtime_start = timestamp_ms(timing.get("started_at_ms"))
    if runtime_start is None:
        runtime_start = timestamp_ms(timing.get("started_at"))
    if runtime_start is None:
        runtime_start = timestamp_ms(job_meta.get("started_at"))
    runtime_finish = timestamp_ms(timing.get("finished_at_ms"))
    if runtime_finish is None:
        runtime_finish = timestamp_ms(timing.get("finished_at"))
    if runtime_finish is None:
        runtime_finish = timestamp_ms(job_meta.get("completed_at"))
    return {"phases": phases, "critical_order": critical_order, "wall_ms": wall,
            "runtime_started_ms": runtime_start,
            "runtime_finished_ms": runtime_finish,
            "work_ms": work, "queue_wait_ms": queue_wait,
            "critical_path_ms": critical_path,
            "publish_wall_ms": (publish or {}).get("wall_ms", 0),
            "publish_work_ms": (publish or {}).get("work_ms", 0),
            "materialization": mat, "subtitle_cache": cache_facts,
            "drive": {
                "download_wall_ms": drive_download_ms,
                "download_work_ms": drive_download_work,
                "upload_wall_ms": drive_upload_ms,
                "upload_work_ms": drive_upload_work,
                "document_publish_wall_ms": doc_publish_ms,
                "renderer_finalize_ms": publish_wall,
                "document_publish_work_ms": doc_publish_work,
            },
            "llm": llm, "result": result, "timing": timing}


