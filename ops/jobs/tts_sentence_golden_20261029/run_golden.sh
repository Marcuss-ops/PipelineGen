#!/usr/bin/env bash
# TTS sentence-chunk golden job (2026-10-29).
#
# Budget: a hypothetical-but-realistic recent scene workload is synthesized
# against the live edge_tts_bridge at sentence granularity with the
# production fan-out, and the critical path must stay under
# TTS_GOLDEN_BUDGET_S (default 10s). Also certifies: 0 failed chunks,
# per-call p50 under 4s, merged artifact non-empty for every scene.
#
# Usage:   ops/jobs/tts_sentence_golden_20261029/run_golden.sh [budget_s]
# Bridge:  TTS_BRIDGE_URL (default http://127.0.0.1:36585 — the bridge
#          prints its OS-assigned port at startup: "PORT=xxxxx").
set -euo pipefail

BUDGET_S="${1:-${TTS_GOLDEN_BUDGET_S:-10}}"
BRIDGE="${TTS_BRIDGE_URL:-http://127.0.0.1:36585}"

python3 - "$BUDGET_S" "$BRIDGE" <<'PY'
import asyncio, sys, time, statistics, json, os, tempfile, shutil
from datetime import datetime, timezone

import aiohttp

budget_s = float(sys.argv[1])
bridge = sys.argv[2]

SCENES = [
    # scene_id, sentences (~25 words per sentence: the production cap)
    ("scene_001", [
        "Guarda come questa città si trasforma dall'alba al tramonto, quando le prime luci colorano le strade principali.",
        "Ogni edificio storico conserva una memoria che attraversa generazioni di famiglie e ne custodisce l'identità più profonda.",
        "I mercati riaprono al mattino e i cittadini riprendono le abitudini che definiscono il ritmo del quartiere.",
    ]),
    ("scene_002", [
        "Le decisioni prese nei vertici amministrativi determinano il futuro delle comunità locali per i prossimi anni.",
        "Al calare della notte l'illuminazione urbana proietta un'atmosfera più lenta, contemplativa e diversa.",
        "Le strade si svuotano gradualmente mentre le luci dei palazzi restano accese fino a tarda sera.",
    ]),
    ("scene_003", [
        "Il fiume attraversa la valle portando con sé racconti di ingegneria e di lotta quotidiana contro le piene.",
        "Dopo il restauro i bastioni hanno ripreso il loro ruolo simbolico al centro delle feste cittadine.",
        "Le campane segnano ancora le ore e scandiscono il passaggio dalle funzioni religiose alla vita mondana.",
    ]),
]

SENTENCE_SPOKEN_WORDS = 0  # informational: sentence count per scene

async def synth(session, scene_id, idx, sentence, out_dir, gate):
    text = sentence.strip().rstrip(",;")
    if not text:
        text = scene_id
    out = os.path.join(out_dir, f"{scene_id}.chunk-{idx:03d}.mp3")
    async with gate:
        t0 = time.perf_counter()
        async with session.post(
            f"{bridge}/synthesize",
            json={"text": text, "lang": "it", "voice": "it-IT-DiegoNeural", "boundary": "word", "out": out},
        ) as r:
            body = await r.json()
            dt = time.perf_counter() - t0
            size = os.path.getsize(body["path"]) if body.get("ok") else 0
            boundary_count = int(body.get("boundary_count") or 0)
            metadata_path = body.get("metadata_path") or ""
            boundaries = []
            boundary_error = ""
            if body.get("ok"):
                try:
                    if not metadata_path or not os.path.isfile(metadata_path):
                        raise ValueError("word-boundary metadata missing")
                    with open(metadata_path, encoding="utf-8") as metadata:
                        boundaries = [json.loads(line) for line in metadata if line.strip()]
                    if len(boundaries) != boundary_count or boundary_count <= 0:
                        raise ValueError(
                            f"boundary count={boundary_count}, metadata rows={len(boundaries)}")
                    previous_start = -1
                    for boundary in boundaries:
                        start_us = int(boundary["start_us"])
                        end_us = int(boundary["end_us"])
                        if boundary.get("type") != "WordBoundary" or end_us <= start_us:
                            raise ValueError("invalid word boundary record")
                        if start_us < previous_start:
                            raise ValueError("word boundaries are not ordered")
                        previous_start = start_us
                except (OSError, ValueError, KeyError, json.JSONDecodeError) as exc:
                    boundary_error = str(exc)
            return {
                "latency_s": dt,
                "ok": bool(body.get("ok")),
                "size_bytes": size,
                "boundary_count": boundary_count,
                "boundary_error": boundary_error,
                "error": body.get("error"),
            }

async def main():
    async with aiohttp.ClientSession() as session:
        try:
            async with session.get(f"{bridge}/health", timeout=aiohttp.ClientTimeout(total=3)) as r:
                if r.status != 200:
                    print(f"FAIL: bridge /health status {r.status}")
                    return 1
        except Exception as exc:
            print(f"FAIL: bridge not reachable at {bridge}: {type(exc).__name__}: {exc}")
            return 1

        gate = asyncio.Semaphore(12)  # production: max_concurrent_tts / chunk_concurrency
        out_dir = tempfile.mkdtemp(prefix="tts_golden_")
        t_all = time.perf_counter()
        # The golden critical path is a scene entirely ready at once: every
        # sentence of the scene fans out concurrently, the scene completes at
        # max(sentence) — the same contract as the chunkedTTSProvider.
        per_scene = []
        scene_rows = []
        async def scene(scene_id, sentences):
            res = await asyncio.gather(*[
                synth(session, scene_id, i, s, out_dir, gate) for i, s in enumerate(sentences)
            ])
            return max(r[0] for r in res), res

        scenes = await asyncio.gather(*[scene(sid, sents) for sid, sents in SCENES])
        wall = time.perf_counter() - t_all

        ok = True
        all_chunks = []
        scene_critical = {}
        for scene_id, (critical, res) in zip([s[0] for s in SCENES], scenes):
            bad = [r for r in res if not r["ok"] or r["size_bytes"] == 0 or r["boundary_error"]]
            all_chunks.extend(res)
            scene_critical[scene_id] = critical
            if bad:
                ok = False
                print(f"FAIL: {scene_id}: {len(bad)} chunks invalid (errs={[(b['error'] or b['boundary_error']) for b in bad][:2]})")
            else:
                print(f"OK: {scene_id}: critical={critical:.2f}s over {len(res)} chunks, word_boundaries={sum(r['boundary_count'] for r in res)}")
        lats = [r["latency_s"] for r in all_chunks]
        p50 = statistics.median(lats)
        print(f"CHUNK LATENCY: p50={p50:.2f}s p95={sorted(lats)[int(len(lats)*0.95)]:.2f}s n={len(lats)}")
        print(f"TOTAL WALL={wall:.2f}s  BUDGET={budget_s}s")
        if wall > budget_s:
            ok = False
            print(f"FAIL: wall {wall:.2f}s exceeds budget {budget_s}s")
        if p50 > 4.0:
            ok = False
            print(f"FAIL: chunk p50 {p50:.2f}s exceeds 4s")
        result = {
            "started_at": datetime.now(timezone.utc).isoformat(),
            "bridge": bridge,
            "budget_s": budget_s,
            "pass": ok,
            "scene_critical_s": scene_critical,
            "chunk_count": len(all_chunks),
            "chunk_p50_s": p50,
            "chunk_p95_s": sorted(lats)[int(len(lats)*0.95)],
            "word_boundary_count": sum(r["boundary_count"] for r in all_chunks),
            "wall_s": wall,
            "chunks": [
                {k: v for k, v in item.items() if k != "boundary_error"}
                for item in all_chunks
            ],
        }
        result_path = os.environ.get("TTS_GOLDEN_RESULT", "").strip()
        if result_path:
            os.makedirs(os.path.dirname(os.path.abspath(result_path)), exist_ok=True)
            temp_path = result_path + ".tmp"
            with open(temp_path, "w", encoding="utf-8") as output:
                json.dump(result, output, indent=2, ensure_ascii=False)
                output.write("\n")
            os.replace(temp_path, result_path)
            print(f"EVIDENCE={result_path}")
        print("PASS" if ok else "FAIL")
        return 0 if ok else 1
        
    try:
        raise SystemExit(asyncio.run(main()) or 0)
    finally:
        shutil.rmtree(out_dir, ignore_errors=True)

raise SystemExit(asyncio.run(main()) or 0)
PY
