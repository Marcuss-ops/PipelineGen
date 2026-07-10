#!/usr/bin/env python3
"""
stock_pacquiao_broner.py — Pacquiao vs Broner stock pipeline test script.

Sends each fight round as a separate POST /api/stock/run request so the
pipeline creates per-round timestamp subdirectories under the Drive root.

Usage:
    # Generate JSON payloads only (no API calls):
    python3 scripts/stock_pacquiao_broner.py --dry-run

    # Send to local PipelineGen server (port 8000 is the canonical
    # PipelineGen port per AGENTS.md port policy; 8080 is SearXNG, NOT PipelineGen):
    python3 scripts/stock_pacquiao_broner.py --base-url http://localhost:8000

    # Send with admin token:
    python3 scripts/stock_pacquiao_broner.py --base-url http://localhost:8000 --token YOUR_TOKEN

    # Override Drive folder ID:
    python3 scripts/stock_pacquiao_broner.py --drive-folder-id YOUR_FOLDER_ID

Environment variables (override CLI args):
    VELOX_ADMIN_TOKEN   — admin bearer token
    VELOX_BASE_URL      — server base URL (default: http://localhost:8080)
    STOCK_DRIVE_FOLDER_ID — Drive folder ID (default: from CLI or hardcoded)
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path

# ── YouTube source video ──────────────────────────────────────────────
YOUTUBE_VIDEO_ID = "RRJvrDKunyA"
YOUTUBE_URL = f"https://www.youtube.com/watch?v={YOUTUBE_VIDEO_ID}"

# ── Drive output folder ───────────────────────────────────────────────
# Root Drive folder (user-provided 2026-07-06).
DEFAULT_DRIVE_FOLDER_ID = "1iAGhWidRF0hpJYvku_fIavEIY50_V1wA"

# ── API configuration ─────────────────────────────────────────────────
DEFAULT_BASE_URL = "http://localhost:8000"
STOCK_ENDPOINT = "/api/stock-pipeline/run"
# Subfolder name under the root Drive folder (user spec 2026-07-06).
PROJECT_SUBFOLDER = "Pacquiao Vs Broner"

# ── Clip extraction settings ──────────────────────────────────────────
CLIP_DURATION_SEC = 5          # 5 seconds per clip
NO_AUDIO = False               # keep audio
NO_EFFECTS = True              # no visual effects
NO_TRANSITIONS = True          # no crossfade transitions

# ── Round definitions ─────────────────────────────────────────────────
# Each round: (label, start_timestamp, end_timestamp, description)
# The end timestamp is normalized to a 60-second window so each title
# produces 12 x 5s clips under its own Drive subdir.
ROUNDS = [
    (
        "Round_1_La_fase_di_studio",
        "00:00:32", "00:01:32",
        "Inizio del match. Pacquiao mette subito in mostra la sua mobilità e rapidità di gambe, lavorando molto con il jab da mancino per prendere le misure. Broner mantiene una guardia molto larga e fatica a prendergli il tempo.",
    ),
    (
        "Round_2_Posizionamento_e_scambi",
        "00:04:07", "00:05:07",
        "Entrambi i pugili cercano di guadagnare la posizione con il piede avanzato. Pacquiao accelera il ritmo con combinazioni veloci, mentre Broner risponde principalmente di rimessa spingendo via l'avversario.",
    ),
    (
        "Round_5_Miglior_momento_Broner",
        "00:10:28", "00:11:28",
        "Broner riesce a trovare maggiore continuità con il diretto destro, colpendo il mento di Pacquiao in un paio di occasioni. Pacquiao risponde con un potente gancio sinistro al corpo prima di riprendere il controllo del ritmo a fine round.",
    ),
    (
        "Round_7_Broner_barcolla",
        "00:16:33", "00:17:33",
        "Il round più spettacolare del match. Pacquiao mette a segno una serie di colpi durissimi, tra cui un potente montante e un sinistro che scuotono visibilmente Broner. Broner è costretto a legare ed è quasi sul punto di andare KO mentre Pacquiao lo tempesta di colpi all'angolo.",
    ),
    (
        "Round_9_Pacquiao_all_attacco",
        "00:21:16", "00:22:16",
        "Un altro ottimo round per il filippino. Pacquiao intercetta Broner con un potente gancio sinistro d'incontro che lo fa arretrare vistosamente sui tacchi, costringendolo nuovamente a subire una raffica di colpi all'angolo.",
    ),
    (
        "Round_10_11_Controllo_Pacquiao",
        "00:23:02", "00:24:02",
        "Viene evidenziato il divario nei colpi portati: Pacquiao domina per volume, mentre Broner lancia pochissimi destri, facendo sospettare un infortunio alla mano. Al Round 11 le statistiche mostrano 109 colpi a segno per Pacquiao contro i soli 49 di Broner.",
    ),
    (
        "Round_12_Finale",
        "00:27:37", "00:28:37",
        "Negli ultimi 30 secondi Broner non mostra l'urgenza di dover recuperare lo svantaggio e Pacquiao controlla agevolmente fino al suono della campana finale.",
    ),
    (
        "Verdetto_Unanime",
        "00:28:47", "00:29:47",
        "I giudici assegnano una netta decisione unanime a favore di Manny Pacquiao (117-111, 116-112, 116-112), che conserva il titolo mondiale WBA dei pesi welter.",
    ),
]


def ts_to_seconds(ts: str) -> float:
    """Convert HH:MM:SS to seconds."""
    parts = ts.split(":")
    h, m, s = int(parts[0]), int(parts[1]), float(parts[2])
    return h * 3600 + m * 60 + s


def split_into_clips(
    start_sec: float,
    end_sec: float,
    clip_dur: int = CLIP_DURATION_SEC,
) -> list[dict]:
    """Split a time range into fixed-duration ClipSpec objects."""
    clips = []
    t = start_sec
    idx = 0
    while t < end_sec:
        clip_end = min(t + clip_dur, end_sec)
        clips.append({
            "title": f"clip_{idx:03d}_{int(t)}s-{int(clip_end)}s",
            "url": YOUTUBE_URL,
            "start_sec": round(t, 2),
            "end_sec": round(clip_end, 2),
        })
        t = clip_end
        idx += 1
    return clips


def build_payload_for_round(
    label: str,
    start_ts: str,
    end_ts: str,
    description: str,
    drive_folder_id: str,
    clip_duration: int = CLIP_DURATION_SEC,
) -> dict:
    """Build a POST /api/stock/run payload for one round."""
    start_sec = ts_to_seconds(start_ts)
    end_sec = ts_to_seconds(end_ts)
    clips = split_into_clips(start_sec, end_sec, clip_duration)

    # Subfolder path: Pacquiao_Vs_Broner/Round_1_.../00-00-32_to_00-03-51
    ts_dir = f"{start_ts.replace(':', '-')}_to_{end_ts.replace(':', '-')}"
    subfolder = f"{PROJECT_SUBFOLDER}/{label}/{ts_dir}"

    return {
        "direct_urls": [YOUTUBE_URL],
        "clips": clips,
        "folder_id": drive_folder_id,
        "subfolder": subfolder,
        "folder_name": label,
        "total_minutes": max(1, int((end_sec - start_sec) / 60) + 1),
        "clip_duration": clip_duration,
        "no_audio": NO_AUDIO,
        "no_effects": NO_EFFECTS,
        "no_transitions": NO_TRANSITIONS,
        "metadata": {
            "title": f"Pacquiao vs Broner — {label.replace('_', ' ')}",
            "description": description,
            "tags": ["boxing", "pacquiao", "broner", "highlights"],
            "category": "sport",
        },
        "async": True,
    }


def build_all_payloads(drive_folder_id: str) -> list[dict]:
    """Build payloads for all rounds."""
    payloads = []
    for label, start_ts, end_ts, desc in ROUNDS:
        p = build_payload_for_round(label, start_ts, end_ts, desc, drive_folder_id)
        payloads.append(p)
    return payloads


def send_request(base_url: str, token: str, payload: dict) -> dict:
    """POST payload to /api/stock/run and return the JSON response."""
    import urllib.request
    import urllib.error

    url = f"{base_url.rstrip('/')}{STOCK_ENDPOINT}"
    data = json.dumps(payload).encode("utf-8")
    headers = {
        "Content-Type": "application/json",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"

    req = urllib.request.Request(url, data=data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            return {"status": resp.status, "body": body}
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        try:
            body = json.loads(body)
        except json.JSONDecodeError:
            pass
        return {"status": e.code, "body": body, "error": str(e)}
    except Exception as e:
        return {"status": 0, "body": None, "error": str(e)}


def poll_job(base_url: str, token: str, job_id: str, max_iter: int = 60, interval: int = 3) -> dict:
    """Poll GET /api/jobs/{job_id}/full until terminal state."""
    import urllib.request
    import urllib.error

    terminal = {"SUCCEEDED", "FAILED", "COMPLETED", "DEAD_LETTERED"}
    url = f"{base_url.rstrip('/')}/api/jobs/{job_id}/full"
    headers = {}
    if token:
        headers["Authorization"] = f"Bearer {token}"

    for i in range(max_iter):
        req = urllib.request.Request(url, headers=headers, method="GET")
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                body = json.loads(resp.read().decode("utf-8"))
                status = body.get("status", body.get("state", "")).upper()
                if status in terminal:
                    return body
        except Exception as e:
            print(f"  [poll {i}] error: {e}")
        time.sleep(interval)
    return {"status": "TIMEOUT", "polls": max_iter}


def main():
    parser = argparse.ArgumentParser(description="Pacquiao vs Broner stock pipeline test")
    parser.add_argument("--dry-run", action="store_true", help="Generate JSON only, no API calls")
    parser.add_argument("--base-url", default=os.environ.get("VELOX_BASE_URL", DEFAULT_BASE_URL))
    parser.add_argument("--token", default=os.environ.get("VELOX_ADMIN_TOKEN", ""))
    parser.add_argument("--drive-folder-id", default=os.environ.get("STOCK_DRIVE_FOLDER_ID", DEFAULT_DRIVE_FOLDER_ID))
    parser.add_argument("--output-dir", default="tests/operational")
    parser.add_argument("--poll", action="store_true", help="Poll job status until terminal")
    parser.add_argument("--round", type=int, help="Send only this round index (0-based)")
    args = parser.parse_args()

    outdir = Path(args.output_dir)
    outdir.mkdir(parents=True, exist_ok=True)

    payloads = build_all_payloads(args.drive_folder_id)

    # ── Always save JSON test file ────────────────────────────────────
    test_file = outdir / "stock_pacquiao_broner_test.json"
    test_data = {
        "_meta": {
            "description": "Pacquiao vs Broner — stock pipeline test payloads",
            "video_id": YOUTUBE_VIDEO_ID,
            "video_url": YOUTUBE_URL,
            "drive_folder_id": args.drive_folder_id,
            "project_subfolder": PROJECT_SUBFOLDER,
            "clip_duration_sec": CLIP_DURATION_SEC,
            "total_rounds": len(ROUNDS),
            "total_clips": sum(len(p["clips"]) for p in payloads),
            "no_audio": NO_AUDIO,
            "no_effects": NO_EFFECTS,
            "no_transitions": NO_TRANSITIONS,
            "endpoint": "POST /api/stock/run",
            "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        },
        "rounds": [],
    }
    for i, (label, start_ts, end_ts, desc) in enumerate(ROUNDS):
        test_data["rounds"].append({
            "index": i,
            "label": label,
            "start": start_ts,
            "end": end_ts,
            "description": desc,
            "payload": payloads[i],
        })

    with open(test_file, "w") as f:
        json.dump(test_data, f, indent=2)
    print(f"[saved] {test_file}  ({len(payloads)} rounds, {test_data['_meta']['total_clips']} clips total)")

    if args.dry_run:
        print("\n[dry-run] Payloads generated. No API calls made.")
        for i, p in enumerate(payloads):
            print(f"  Round {i}: {len(p['clips'])} clips → subfolder: {p['subfolder']}")
        return

    # ── Send requests to API ──────────────────────────────────────────
    results = []
    rounds_to_send = [args.round] if args.round is not None else range(len(payloads))

    for i in rounds_to_send:
        p = payloads[i]
        label = ROUNDS[i][0]
        print(f"\n{'='*60}")
        print(f"[round {i}] {label}")
        print(f"  clips: {len(p['clips'])}")
        print(f"  subfolder: {p['subfolder']}")
        print(f"  POST {args.base_url}/api/stock/run ...")

        resp = send_request(args.base_url, args.token, p)
        status = resp.get("status", 0)
        body = resp.get("body", {})
        job_id = body.get("job_id", "") if isinstance(body, dict) else ""
        error = resp.get("error", "")

        result = {
            "round": i,
            "label": label,
            "http_status": status,
            "job_id": job_id,
            "response": body,
        }

        if error:
            print(f"  ERROR: {error}")
            result["error"] = error
        elif status == 200 and job_id:
            print(f"  OK — job_id: {job_id}")
            print(f"  status_url: /api/jobs/{job_id}/full")

            if args.poll:
                print(f"  polling job {job_id} ...")
                final = poll_job(args.base_url, args.token, job_id)
                result["final_status"] = final.get("status", "UNKNOWN")
                print(f"  final: {result['final_status']}")
        else:
            print(f"  HTTP {status}: {body}")

        results.append(result)

        # Small delay between rounds to avoid rate-limiting.
        if i != rounds_to_send[-1]:
            time.sleep(2)

    # ── Save results ──────────────────────────────────────────────────
    results_file = outdir / "stock_pacquiao_broner_results.json"
    with open(results_file, "w") as f:
        json.dump({
            "run_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "base_url": args.base_url,
            "drive_folder_id": args.drive_folder_id,
            "results": results,
        }, f, indent=2)
    print(f"\n[saved] {results_file}")

    # ── Summary ───────────────────────────────────────────────────────
    ok = sum(1 for r in results if r.get("job_id"))
    fail = len(results) - ok
    print(f"\n{'='*60}")
    print(f"Summary: {ok} enqueued, {fail} failed, {len(results)} total rounds")


if __name__ == "__main__":
    main()
