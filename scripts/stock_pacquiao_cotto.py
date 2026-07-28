#!/usr/bin/env python3
"""
stock_pacquiao_cotto.py — Pacquiao vs Cotto stock pipeline test script.

Sends each fight round as a separate POST /api/stock-pipeline/run request so the
pipeline creates per-round timestamp subdirectories under the Drive root.

Usage:
    # Generate JSON payloads only (no API calls):
    python3 scripts/stock_pacquiao_cotto.py --dry-run

    # Send to local PipelineGen server (port 8000 is the canonical
    # PipelineGen port per AGENTS.md port policy; 8080 is SearXNG, NOT PipelineGen):
    python3 scripts/stock_pacquiao_cotto.py --base-url http://localhost:8000

    # Send with admin token:
    python3 scripts/stock_pacquiao_cotto.py --base-url http://localhost:8000 --token YOUR_TOKEN

    # Override Drive folder ID:
    python3 scripts/stock_pacquiao_cotto.py --drive-folder-id YOUR_FOLDER_ID

Environment variables (override CLI args):
    VELOX_ADMIN_TOKEN   — admin bearer token
    VELOX_BASE_URL      — server base URL (default: http://localhost:8000)
    STOCK_DRIVE_FOLDER_ID — Drive folder ID (default: from CLI or hardcoded)
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path

# ── YouTube source video ──────────────────────────────────────────────
YOUTUBE_VIDEO_ID = "QdSbtEo3x_Y"
YOUTUBE_URL = f"https://www.youtube.com/watch?v={YOUTUBE_VIDEO_ID}"

# ── Drive output folder ───────────────────────────────────────────────
DEFAULT_DRIVE_FOLDER_ID = "1J-zIuqroF0rkTrKxU-tmZu9e5rN20ggV"

# ── API configuration ─────────────────────────────────────────────────
DEFAULT_BASE_URL = "http://localhost:8000"
STOCK_ENDPOINT = "/api/stock-pipeline/run"
# Subfolder name under the root Drive folder
PROJECT_SUBFOLDER = "Pacquiao_vs_Cotto"

# ── Clip extraction settings ──────────────────────────────────────────
CLIP_DURATION_SEC = 4          # 4 seconds per clip (max)
NO_AUDIO = False
NO_EFFECTS = True
NO_TRANSITIONS = True

# ── Round definitions ─────────────────────────────────────────────────
# Each round: (label, start_timestamp, end_timestamp, description)
ROUNDS = [
    (
        "Round_1_La_fase_di_studio",
        "00:00:00", "00:01:44",
        "Il campione Miguel Cotto inizia con grande determinazione, imponendo fin da subito un jab sinistro potente e preciso per tenere a distanza la velocità di Pacquiao. Pacquiao studia l'avversario cercando di calcolare il timing ideale e le traiettorie di ingresso, subendo qualche colpo isolato ma chiudendo il round in gestione.",
    ),
    (
        "Round_2_Scambi_ad_alta_velocita",
        "00:01:44", "00:04:05",
        "Pacquiao inizia a sprigionare combinazioni fulminee che Cotto non riesce a decifrare completamente. Il ritmo si impenna drammaticamente: Cotto risponde colpo su colpo con ganci sinistri pesanti al corpo e alla testa, ma Pacquiao risponde creando angoli d'attacco imprevedibili.",
    ),
    (
        "Round_3_Il_primo_knockdown",
        "00:04:05", "00:06:21",
        "Pacquiao aumenta la pressione e, muovendosi in avanzamento, intercetta Cotto con un perfetto colpo d'incontro corto mandandolo al tappeto. È un flash knockdown: Cotto si rialza immediatamente al conteggio di 7 e dimostra immenso cuore, ricominciando a scambiare a viso aperto e aggredendo Pacquiao per recuperare lo svantaggio.",
    ),
    (
        "Round_4_Secondo_atterramento",
        "00:06:21", "00:08:49",
        "Cotto aggredisce con ferocia e costringe Pacquiao alle corde facendogli incassare colpi duri. Pacquiao tuttavia non si scompone, esce dalla pressione combinando e ruotando. Proprio negli ultimi secondi del round, mentre Cotto avanza, Pacquiao fa partire un montante sinistro micidiale che abbatte nuovamente Cotto. Il portoricano è visibilmente scosso e salvato solo dalla campana.",
    ),
    (
        "Round_5_Ripristino_distanze",
        "00:08:49", "00:11:15",
        "Dopo la tempesta del round precedente, il ritmo rallenta leggermente. Cotto, consapevole dei rischi, cerca di riguadagnare stabilità e lucidità ripartendo dal jab. Pacquiao d'altro canto non forza la chiusura prematura, controlla lo spazio con colpi d'incontro precisi e gestisce la ripresa senza rischiare.",
    ),
    (
        "Round_6_Il_logorio_ai_fianchi",
        "00:11:15", "00:13:30",
        "Pacquiao torna a farsi aggressivo lavorando \"sotto e sopra\" con i suoi classici inserimenti fulminei. Mette a segno colpi subdoli al corpo che iniziano a togliere fiato a Cotto. Il portoricano mette a segno una poderosa reazione d'orgoglio a metà round scuotendo Pacquiao, ma il filippino incassa magnificamente e chiude colpendo duro il volto del rivale.",
    ),
    (
        "Round_7_Cambi_di_guardia",
        "00:13:30", "00:15:25",
        "Pacquiao è molto più mobile, continuo ed efficace. Disperato, Cotto tenta ripetutamente di cambiare guardia passando a mancino nel tentativo di arginare le traiettorie dell'avversario e trovare nuovi varchi, ma senza successo. Pacquiao domina la distanza e lo anticipa sistematicamente con combinazioni corte a tre colpi.",
    ),
    (
        "Round_8_Ruoli_invertiti",
        "00:15:25", "00:17:04",
        "I ruoli fisici sono totalmente ribaltati: Pacquiao (l'ex peso mosca, teoricamente più piccolo) cammina letteralmente sopra a Cotto spingendolo indietro. Il portoricano finisce intrappolato contro le corde dove subisce valanghe di colpi, costretto a una pura azione di ritirata e sopravvivenza difensiva.",
    ),
    (
        "Round_9_Punizione_sistematica",
        "00:17:04", "00:18:52",
        "Il volto di Cotto è ormai una maschera di sangue e il naso sanguina copiosamente. Il match si trasforma in una demolizione sistematica. Pacquiao scatena un assalto furioso a metà round che fa sbandare pesantemente Cotto sulle gambe. Cotto non riesce più a controbattere e si limita a difendersi passivamente pur di restare in piedi.",
    ),
    (
        "Round_10_La_fuga_sulle_punte",
        "00:18:52", "00:20:19",
        "Cotto cambia completamente strategia, rinunciando a qualsiasi velleità offensiva per concentrarsi unicamente sul movimento laterale e la fuga ad anello continuo per evitare i colpi. Pacquiao non forza i tempi, lo insegue con calma e aspetta che il portoricano si fermi per piazzare colpi isolati ma pesanti.",
    ),
    (
        "Round_11_Controllo_totale",
        "00:20:19", "00:22:00",
        "Pacquiao amministra il match a suo piacimento: accelera quando vuole e rifiata quando preferisce, controllando totalmente il ring. Cotto, all'angolo, riceve l'indicazione di dare tutto per gli ultimi minuti, ma la disparità di energie e freschezza atletica rende impossibile ogni capovolgimento di fronte.",
    ),
    (
        "Round_12_Epilogo_per_TKO",
        "00:22:00", "00:23:10",
        "In apertura dell'ultimo round, Pacquiao decide di chiudere i conti ed entra duro con una serie di combinazioni consecutive e pulite sul volto indifeso di Cotto. Vedendo il portoricano incapace di rispondere e costantemente scosso, l'arbitro Kenny Bayless interviene prontamente per interrompere la contesa e decretare la vittoria per TKO di Manny Pacquiao, che diventa il nuovo campione WBO dei pesi welter.",
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
) -> dict:
    """Build a POST /api/stock-pipeline/run payload for one round."""
    start_sec = ts_to_seconds(start_ts)
    end_sec = ts_to_seconds(end_ts)
    duration_sec = end_sec - start_sec

    clips = split_into_clips(start_sec, end_sec, CLIP_DURATION_SEC)

    # Subfolder path: Pacquiao_vs_Cotto/Round_X_.../HH-MM-SS_to_HH-MM-SS
    ts_dir = f"{start_ts.replace(':', '-')}_to_{end_ts.replace(':', '-')}"
    subfolder = f"{PROJECT_SUBFOLDER}/{label}/{ts_dir}"

    return {
        "direct_urls": [YOUTUBE_URL],
        "clips": clips,
        "folder_id": drive_folder_id,
        "subfolder": subfolder,
        "folder_name": label.replace("_", " "),
        "total_minutes": max(1, int(duration_sec / 60) + 1),
        "clip_duration": CLIP_DURATION_SEC,
        "no_audio": NO_AUDIO,
        "no_effects": NO_EFFECTS,
        "no_transitions": NO_TRANSITIONS,
        "metadata": {
            "title": f"Pacquiao vs Cotto — {label.replace('_', ' ')}",
            "description": description,
            "tags": ["boxing", "pacquiao", "cotto", "highlights"],
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
    """POST payload to /api/stock-pipeline/run and return the JSON response."""
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
    parser = argparse.ArgumentParser(description="Pacquiao vs Cotto stock pipeline test")
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
    test_file = outdir / "stock_pacquiao_cotto_test.json"
    test_data = {
        "_meta": {
            "description": "Pacquiao vs Cotto — stock pipeline test payloads",
            "video_id": YOUTUBE_VIDEO_ID,
            "video_url": YOUTUBE_URL,
            "drive_folder_id": args.drive_folder_id,
            "project_subfolder": PROJECT_SUBFOLDER,
            "total_rounds": len(ROUNDS),
            "total_clips": sum(len(p["clips"]) for p in payloads),
            "no_audio": NO_AUDIO,
            "no_effects": NO_EFFECTS,
            "no_transitions": NO_TRANSITIONS,
            "endpoint": STOCK_ENDPOINT,
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
            print(f"  Round {i}: {len(p['clips'])} clips x {CLIP_DURATION_SEC}s → subfolder: {p['subfolder']}")
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
        print(f"  POST {args.base_url}{STOCK_ENDPOINT} ...")

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
    results_file = outdir / "stock_pacquiao_cotto_results.json"
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
