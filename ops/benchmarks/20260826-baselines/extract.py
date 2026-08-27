#!/usr/bin/env python3
"""Extract the 2026-08-26 baseline reports (20-clip cold, 10-clip cold) from the
local SQLite databases and save structured reports into the repo.

Sources:
  - jobs DB      : data/media/media.db.sqlite   -> jobs (result_json)
  - observability: data/observability/api_requests.db.sqlite -> run_observability (report_json)
"""
import json
import os
import sqlite3
from collections import Counter, defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
JOBS_DB = os.path.join(ROOT, "data/media/media.db.sqlite")
OBS_DB = os.path.join(ROOT, "data/observability/api_requests.db.sqlite")
OUT = os.path.join(ROOT, "ops/benchmarks/20260826-baselines")

RUNS = [
    {
        "tag": "20-clip-cold",
        "job_id": "job_1787741752772649756_cf2ad88e",
        "run_id": "run_1787741851825987481_cf2437f8a83f",
        "note": "num_clips=20, force_refresh=true — benchmark canonico",
    },
    {
        "tag": "10-clip-cold",
        "job_id": "job_1787742604386297795_7b0af271",
        "run_id": "run_1787742607243524793_75280c6ea622",
        "note": "num_clips=10, force_refresh=true — scaling con N scene",
    },
]

os.makedirs(OUT, exist_ok=True)


def fetch(jdb, obsdb, tag, job_id, run_id, note):
    cur = jdb.execute(
        "SELECT * FROM jobs WHERE id=?",
        (job_id,),
    )
    job = dict(zip([d[0] for d in cur.description], cur.fetchone()))
    payload = json.loads(job["payload_json"] or "{}")
    result = json.loads(job["result_json"] or "{}")

    run = obsdb.execute(
        "SELECT report_json FROM run_observability WHERE run_id=?", (run_id,)
    ).fetchone()
    report = json.loads(run[0]) if run else {}

    # ---- payload facts ----
    items = payload.get("items") or []
    item = items[0] if items else {}
    source = item.get("source") or {}
    num_clips = source.get("num_clips")
    force_refresh = (item.get("script_params") or {}).get("force_refresh")

    # ---- RunReport facts ----
    stages = defaultdict(lambda: {"ms": 0, "n": 0, "status": Counter()})
    for s in report.get("stages", []):
        name = s.get("name", "?")
        stages[name]["ms"] += s.get("duration_ms") or 0
        stages[name]["n"] += 1
        stages[name]["status"][s.get("status", "?")] += 1

    ops = defaultdict(lambda: {"ms": 0, "n": 0, "queue": 0, "items": 0})
    for o in report.get("operations", []):
        k = (o.get("stage", "?"), o.get("component", "?"), o.get("operation", "?"))
        ops[k]["ms"] += o.get("duration_ms") or 0
        ops[k]["n"] += 1
        ops[k]["queue"] += o.get("queue_wait_ms") or 0
        ops[k]["items"] += o.get("items") or 0

    # ---- result facts ----
    data = (result.get("data") or {}).get("result") or {}
    audio = data.get("audio_metrics") or {}
    translation = data.get("translation_metrics") or {}
    final_audio = data.get("final_audio") or {}
    manifest = (result.get("data") or {}).get("__artifact_manifest") or {}
    artifacts = manifest.get("artifacts") or []
    kinds = Counter(a.get("kind", "?") for a in artifacts)
    docs = data.get("documents") or {}
    doc_link = ""
    if docs and "en" in docs:
        doc_link = (docs["en"] or {}).get("link", "")

    summary = {
        "tag": tag,
        "note": note,
        "job_id": job_id,
        "run_id": run_id,
        "correlation_id": job.get("correlation_id", ""),
        "status": job.get("status", ""),
        "num_clips": num_clips,
        "force_refresh": force_refresh,
        "started_at": job.get("started_at"),
        "completed_at": job.get("completed_at"),
        "total_wall_ms": job.get("duration_ms"),
        "run_report": {
            "wall_time_ms": report.get("wall_time_ms"),
            "queue_wait_ms": report.get("queue_wait_ms"),
            "accumulated_operation_ms": report.get("accumulated_operation_ms"),
            "attributed_stage_ms": report.get("attributed_stage_ms"),
            "unattributed_ms": report.get("unattributed_ms"),
            "unattributed_percent": report.get("unattributed_percent"),
            "bottleneck_stage": report.get("bottleneck_stage"),
            "bottleneck_operation": report.get("bottleneck_operation"),
            "stages": {
                k: {"wall_ms": v["ms"], "count": v["n"], "status": dict(v["status"])}
                for k, v in sorted(stages.items(), key=lambda kv: -kv[1]["ms"])
            },
            "operations": {
                "/".join(k): {"total_ms": v["ms"], "count": v["n"],
                              "queue_wait_ms": v["queue"], "items": v["items"]}
                for k, v in sorted(ops.items(), key=lambda kv: -kv[1]["ms"])
            },
        },
        "audio_metrics": audio,
        "translation_metrics_llm": translation,
        "final_audio": {
            "duration_ms": final_audio.get("duration_ms"),
            "size_bytes": final_audio.get("size_bytes"),
            "bitrate": final_audio.get("bitrate"),
            "sample_rate": final_audio.get("sample_rate"),
            "channels": final_audio.get("channels"),
            "codec": final_audio.get("codec"),
        },
        "docs": {"en_link": doc_link},
        "artifacts": {"count": len(artifacts), "kinds": dict(kinds)},
        "error": job.get("error", ""),
    }
    return summary


def fmt_ms(ms):
    if ms is None:
        return "-"
    return f"{ms/1000:.1f}s" if ms >= 1000 else f"{ms}ms"


def render_md(s):
    L = []
    L.append(f"# Baseline `{s['tag']}` — {s['note']}")
    L.append("")
    L.append(f"- Job: `{s['job_id']}` — Run: `{s['run_id']}`")
    L.append(f"- Correlation: `{s['correlation_id']}`")
    L.append(f"- Status: {s['status']} — num_clips={s['num_clips']}, force_refresh={s['force_refresh']}")
    L.append(f"- `{s['started_at']}` → `{s['completed_at']}`")
    L.append("")
    L.append("## TOTAL WALL")
    L.append("")
    r = s["run_report"]
    L.append(f"| Metrica | Valore |")
    L.append(f"| --- | --- |")
    L.append(f"| **TOTAL WALL** | **{fmt_ms(s['total_wall_ms'])}** |")
    L.append(f"| wall_time_ms (RunReport) | {fmt_ms(r['wall_time_ms'])} |")
    L.append(f"| queue_wait | {fmt_ms(r['queue_wait_ms'])} |")
    L.append(f"| attributed stage | {fmt_ms(r['attributed_stage_ms'])} |")
    L.append(f"| unattributed | {fmt_ms(r['unattributed_ms'])} ({r['unattributed_percent']:.2f}%)" if r.get("unattributed_percent") is not None else "| unattributed | - |")
    L.append(f"| bottleneck | {r['bottleneck_stage']} / {r['bottleneck_operation']} |")
    L.append("")
    L.append("## STAGES (wall, aggregati per nome)")
    L.append("")
    L.append("| Stage | wall | # |")
    L.append("| --- | --- | --- |")
    for name, v in r["stages"].items():
        L.append(f"| {name} | {fmt_ms(v['wall_ms'])} | {v['count']} |")
    L.append("")
    L.append("## OPERATIONS (accumulati per stage/component/operation)")
    L.append("")
    L.append("| Operazione | tot ms | # | queue ms | items |")
    L.append("| --- | --- | --- | --- | --- |")
    for name, v in r["operations"].items():
        L.append(f"| {name} | {v['total_ms']} | {v['count']} | {v['queue_wait_ms']} | {v['items']} |")
    L.append("")
    L.append("## AUDIO")
    L.append("")
    L.append("| Metrica | ms |")
    L.append("| --- | --- |")
    for k in ["tts_ms", "media_fetch_ms", "timeline_compile_ms", "audio_plan_compile_ms",
              "clip_audio_prepare_ms", "mix_ms", "aac_encode_ms", "probe_ms", "hash_ms",
              "upload_ms", "total_ms"]:
        v = s["audio_metrics"].get(k)
        L.append(f"| {k} | {v} |")
    L.append("")
    L.append("| Campo | Valore |")
    L.append("| --- | --- |")
    L.append(f"| audio_duration_ms | {s['audio_metrics'].get('audio_duration_ms')} |")
    L.append(f"| tts_calls | {s['audio_metrics'].get('tts_calls')} |")
    L.append(f"| audio_rtf | {s['audio_metrics'].get('audio_rtf')} |")
    L.append(f"| audio_speed | {s['audio_metrics'].get('audio_speed')} |")
    L.append(f"| audio_encode_passes | {s['audio_metrics'].get('audio_encode_passes')} |")
    L.append("")
    L.append("## LLM / DOCS / ARTIFACTS")
    L.append("")
    L.append(f"- LLM (translation_metrics): {s['translation_metrics_llm']}")
    L.append(f"- Docs en link: {s['docs']['en_link']}")
    L.append(f"- Artifacts: {s['artifacts']['count']} → {s['artifacts']['kinds']}")
    fa = s["final_audio"]
    L.append(f"- Final audio: {fa.get('codec')} {fa.get('sample_rate')}Hz {fa.get('channels')}ch, "
             f"{fa.get('duration_ms')}ms, {fa.get('size_bytes')}B, bitrate {fa.get('bitrate')}")
    L.append("")
    return "\n".join(L)


def main():
    import argparse

    ap = argparse.ArgumentParser(
        description="Extract baseline reports from the local SQLite DBs. "
        "Without args, re-extracts the hardcoded 2026-08-26 runs. "
        "Pass --job/--run to append a new run (e.g. the next 20-clip run with HEAD)."
    )
    ap.add_argument("--tag", action="append", help="report tag (repeatable; pairs with --job/--run)")
    ap.add_argument("--job", action="append", help="jobs.id (repeatable)")
    ap.add_argument("--run", action="append", help="run_observability.run_id (repeatable, pairs with --job)")
    ap.add_argument("--note", action="append", help="optional note per run (repeatable)")
    args = ap.parse_args()

    runs = list(RUNS)
    if (args.job or args.run) and (not args.job or not args.run or len(args.job) != len(args.run)):
        ap.error("--job and --run must be provided together, paired 1:1")
    for i, (jid, rid) in enumerate(zip(args.job or [], args.run or [])):
        tag = args.tag[i] if args.tag and i < len(args.tag) else f"custom-{i + 1}"
        note = args.note[i] if args.note and i < len(args.note) else f"custom run {jid[-8:]}"
        runs.append({"tag": tag, "job_id": jid, "run_id": rid, "note": note})

    jdb = sqlite3.connect(f"file:{JOBS_DB}?mode=ro", uri=True)
    obs = sqlite3.connect(f"file:{OBS_DB}?mode=ro", uri=True)
    summaries = []
    for spec in runs:
        s = fetch(jdb, obs, spec["tag"], spec["job_id"], spec["run_id"], spec["note"])
        summaries.append(s)
        with open(os.path.join(OUT, f"{s['tag']}.report.json"), "w") as f:
            json.dump(s, f, indent=2)
        with open(os.path.join(OUT, f"{s['tag']}.md"), "w") as f:
            f.write(render_md(s))
        print(f"wrote {s['tag']} — wall {fmt_ms(s['total_wall_ms'])} — finalize stage "
              f"{fmt_ms(s['run_report']['stages'].get('post_writer_finalize', {}).get('wall_ms'))}")
    # summary table
    lines = ["# Baseline 2026-08-26 — sintesi", ""]
    lines.append("Eseguite oggi alle ~10:55 su worker `YOutube_219819_1` con binario "
                 "**pre-split finalize** (commit `28e75f86d` — split metrico + concurrency 4 — "
                 "è arrivato alle 12:22 UTC, dopo questi run).")
    lines.append("")
    lines.append("| Baseline | job | wall | post_writer_finalize | bottleneck | audio (mix+AAC+upload) | LLM (ollama) | TTS |")
    lines.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
    for s in summaries:
        r = s["run_report"]
        pfw = r["stages"].get("post_writer_finalize", {}).get("wall_ms")
        am = s["audio_metrics"]
        gen = r["stages"].get("generate", {}).get("wall_ms")
        tts = r["stages"].get("tts", {}).get("wall_ms")
        lines.append(
            f"| {s['tag']} | `{s['job_id'][-8:]}` | **{fmt_ms(s['total_wall_ms'])}** | "
            f"{fmt_ms(pfw)} | {r['bottleneck_stage']} | "
            f"{fmt_ms(am.get('mix_ms'))}+{fmt_ms(am.get('aac_encode_ms'))}+{fmt_ms(am.get('upload_ms'))} | "
            f"{fmt_ms(gen)} | {fmt_ms(tts)} |"
        )
    lines.append("")
    custom = len(runs) > len(RUNS)
    if custom:
        lines.append("> ⚙️ Run custom aggiunti via CLI: `" + "`, `".join(s["tag"] for s in summaries[len(RUNS):]) + "`.")
    lines.append("> ✅ Interventi P0 attivi dal binario HEAD (≥ `28e75f86d`): (1) split metrico di "
                 "post_writer_finalize nel RunReport (finalize.artifact_prepare / artifact_hash / "
                 "drive_publish / completion_tx); (2) pubblicazione Drive bounded-parallel a 4 worker "
                 "con CompleteWithArtifacts rimasto single-TX atomico; (3) voiceover intermedi NON più "
                 "nel manifest finalize — pubblicati dalla fase TTS con idratazione binding, finalize O(1) "
                 "(script.json + scenes.json + final_audio.m4a).")
    lines.append("")
    lines.append("## 20-clip WARM — FALLITO (da ripetere)")
    lines.append("")
    lines.append("`job_1787742092785484662_5f036405` (`matt-damon-20-clips-baseline-warm-20260826-105515-request`)")
    lines.append("- Eventi: `job_running` → `leased` (11:06:33) → `job_failed` (11:07:44, dopo 71s)")
    lines.append("- Errore: `generate job handler: read durable run result: context canceled`")
    lines.append("- Diagnosi: **cancellazione del contesto client/submitter**, non un errore di "
                 "pipeline. Il worker stava ancora lavorando quando il contesto HTTP del chiamante è "
                 "scaduto/è stato cancellato (nessun errore applicativo nel run).")
    lines.append("- Azione: rilanciare con timeout lato client adeguato (es. ≥ 15 min) e con il "
                 "binario HEAD per catturare anche lo split finalize.")
    lines.append("")
    with open(os.path.join(OUT, "SUMMARY.md"), "w") as f:
        f.write("\n".join(lines))
    print("wrote SUMMARY.md")


if __name__ == "__main__":
    main()
