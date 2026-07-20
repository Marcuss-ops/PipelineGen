#!/usr/bin/env python3
"""Test: Pacquiao vs Cotto 12-round stock pipeline with Drive folder creation.

Sends 12 sequential API calls (one per round). Each round:
  - 60-second max clip from a YouTube URL
  - Split into 4-second segments (seconds_per_segment=4)
  - Uploaded to a dedicated Drive folder under Pacquiao_vs_Cotto/

Pre-creates the parent "Pacquiao_vs_Cotto" folder on the first call,
then creates per-round subfolders underneath it.

Usage:
  export VELOX_ADMIN_TOKEN="***"
  python3 scripts/test_pacquiao_cotto_12rounds.py [--base-url http://127.0.0.1:8000]
"""

import json
import os
import ssl
import sys
import time
import urllib.request
import urllib.error
from datetime import datetime

DEFAULT_BASE_URL = "http://127.0.0.1:8000"
ROOT_FOLDER_ID = "1eB269ASKAadiT58rm_QmzbLitHu1JsYR"
VIDEO_URL = "https://www.youtube.com/watch?v=QdSbtEo3x_Y"
PARENT_FOLDER_NAME = "Pacquiao_vs_Cotto"
SECONDS_PER_SEGMENT = 4

ROUNDS = [
    {
        "num": 1,
        "name": "Round_1_La_fase_di_studio",
        "start": 0.0, "end": 60.0,
        "title": "Pacquiao vs Cotto — Round 1",
        "description": "Fase iniziale: Cotto impone il jab sinistro, Pacquiao studia le traiettorie.",
        "tags": ["boxing", "pacquiao", "cotto", "round 1"],
    },
    {
        "num": 2,
        "name": "Round_2_Scambi_ad_alta_velocita",
        "start": 60.0, "end": 120.0,
        "title": "Pacquiao vs Cotto — Round 2",
        "description": "Combinazioni fulminee. Cotto risponde con ganci pesanti al corpo.",
        "tags": ["boxing", "pacquiao", "cotto", "speed"],
    },
    {
        "num": 3,
        "name": "Round_3_Il_primo_knockdown",
        "start": 120.0, "end": 180.0,
        "title": "Pacquiao vs Cotto — Round 3",
        "description": "Pacquiao atterra Cotto con un colpo d'incontro. Cotto si rialza al conteggio di 7.",
        "tags": ["boxing", "knockdown", "pacquiao", "cotto"],
    },
    {
        "num": 4,
        "name": "Round_4_Secondo_atterramento",
        "start": 180.0, "end": 240.0,
        "title": "Pacquiao vs Cotto — Round 4",
        "description": "Cotto spinge alle corde, ma subisce un secondo atterramento devastante.",
        "tags": ["boxing", "knockdown", "war", "pacquiao"],
    },
    {
        "num": 5,
        "name": "Round_5_Ripristino_distanze",
        "start": 240.0, "end": 300.0,
        "title": "Pacquiao vs Cotto — Round 5",
        "description": "Fase tattica. Cotto prova a riordinare le idee, Pacquiao boxa d'intelligenza.",
        "tags": ["boxing", "tactical", "pacquiao", "cotto"],
    },
    {
        "num": 6,
        "name": "Round_6_Il_logorio_ai_fianchi",
        "start": 300.0, "end": 360.0,
        "title": "Pacquiao vs Cotto — Round 6",
        "description": "Logorio al corpo. Cotto reagisce con orgoglio ma Pacquiao chiude duro.",
        "tags": ["boxing", "body shots", "power", "pacquiao"],
    },
    {
        "num": 7,
        "name": "Round_7_Cambi_di_guardia",
        "start": 360.0, "end": 420.0,
        "title": "Pacquiao vs Cotto — Round 7",
        "description": "Cotto cambia guardia in mancino. Pacquiao neutralizza ogni mossa.",
        "tags": ["boxing", "tactics", "southpaw", "pacquiao"],
    },
    {
        "num": 8,
        "name": "Round_8_Ruoli_invertiti",
        "start": 420.0, "end": 480.0,
        "title": "Pacquiao vs Cotto — Round 8",
        "description": "Pacquiao cammina sopra Cotto, inchiodandolo alle corde.",
        "tags": ["boxing", "pressure", "ropes", "dominance"],
    },
    {
        "num": 9,
        "name": "Round_9_Punizione_sistematica",
        "start": 480.0, "end": 540.0,
        "title": "Pacquiao vs Cotto — Round 9",
        "description": "Demolizione sistematica. Cotto è ferito e sbanda pesantemente.",
        "tags": ["boxing", "heavy damage", "attrition", "pacquiao"],
    },
    {
        "num": 10,
        "name": "Round_10_La_fuga_sulle_punte",
        "start": 540.0, "end": 600.0,
        "title": "Pacquiao vs Cotto — Round 10",
        "description": "Cotto boxa per sopravvivere, eludendo lo scontro diretto.",
        "tags": ["boxing", "evasive", "survival", "cotto"],
    },
    {
        "num": 11,
        "name": "Round_11_Controllo_totale",
        "start": 600.0, "end": 660.0,
        "title": "Pacquiao vs Cotto — Round 11",
        "description": "Pacquiao domina senza forzare, gestendo il match a piacimento.",
        "tags": ["boxing", "masterclass", "control", "pacquiao"],
    },
    {
        "num": 12,
        "name": "Round_12_Epilogo_per_TKO",
        "start": 660.0, "end": 720.0,
        "title": "Pacquiao vs Cotto — Round 12",
        "description": "TKO. Pacquiao investe Cotto, l'arbitro Bayless interrompe.",
        "tags": ["boxing", "tko", "finish", "champion"],
    },
]


def make_round_payload(rnd, folder_id):
    """Build the stock pipeline JSON payload for one round."""
    clip_duration_sec = rnd["end"] - rnd["start"]
    return {
        "direct_urls": [VIDEO_URL],
        "clips": [
            {
                "title": rnd["name"],
                "url": VIDEO_URL,
                "start_sec": rnd["start"],
                "end_sec": rnd["end"],
            }
        ],
        "folder_id": folder_id,
        "folder_name": rnd["name"],
        "subfolder": f"Pacquiao_vs_Cotto/Round_{rnd['num']}_{rnd['name'].split('_', 1)[1]}/{_ts(rnd['start'])}_to_{_ts(rnd['end'])}",
        "total_minutes": max(1, int(clip_duration_sec / 60)),
        "seconds_per_segment": SECONDS_PER_SEGMENT,
        "no_audio": False,
        "no_effects": True,
        "no_transitions": True,
        "metadata": {
            "title": rnd["title"],
            "description": rnd["description"],
            "tags": rnd["tags"],
            "category": "sport",
        },
        "async": True,
    }


def _ts(seconds):
    """Format seconds as HH-MM-SS."""
    h = int(seconds) // 3600
    m = (int(seconds) % 3600) // 60
    s = int(seconds) % 60
    return f"{h:02d}-{m:02d}-{s:02d}"


def api_call(base_url, token, payload, run_dir, label):
    """Send one stock pipeline API call and save request/response."""
    url = f"{base_url.rstrip('/')}/api/stock-pipeline/run"
    body = json.dumps(payload).encode("utf-8")

    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")

    # Save request
    req_file = os.path.join(run_dir, f"{label}_request.json")
    with open(req_file, "w") as f:
        json.dump(payload, f, indent=2)

    try:
        ctx = ssl._create_unverified_context()
        with urllib.request.urlopen(req, context=ctx, timeout=30) as resp:
            resp_body = resp.read().decode("utf-8")
            status = resp.status
            try:
                resp_json = json.loads(resp_body)
                formatted = json.dumps(resp_json, indent=2)
            except json.JSONDecodeError:
                formatted = resp_body

            resp_file = os.path.join(run_dir, f"{label}_response.json")
            with open(resp_file, "w") as f:
                f.write(formatted)

            return status, resp_json if resp_body.startswith("{") else {"raw": resp_body}

    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8")
        err_file = os.path.join(run_dir, f"{label}_error_{e.code}.json")
        with open(err_file, "w") as f:
            f.write(err_body)
        return e.code, {"error": err_body}

    except Exception as e:
        return 0, {"error": str(e)}


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Pacquiao vs Cotto 12-round stock pipeline test")
    parser.add_argument("--base-url", default=os.getenv("VELOX_MASTER_URL", DEFAULT_BASE_URL))
    parser.add_argument("--token", default=os.getenv("VELOX_ADMIN_TOKEN"))
    parser.add_argument("--start-round", type=int, default=1, help="Start from round N (1-12)")
    parser.add_argument("--end-round", type=int, default=12, help="End at round N (1-12)")
    parser.add_argument("--dry-run", action="store_true", help="Print payloads without sending")
    args = parser.parse_args()

    token = args.token
    if not token and not args.dry_run:
        print("WARNING: VELOX_ADMIN_TOKEN not set. Requests will likely fail.", file=sys.stderr)

    timestamp = datetime.now().strftime("%Y-%m-%d_%H-%M-%S")
    run_dir = os.path.join("runs", "pacquiao_cotto_test", timestamp)
    os.makedirs(run_dir, exist_ok=True)
    print(f"Run directory: {run_dir}")

    # Phase 1: create parent folder "Pacquiao_vs_Cotto" under ROOT_FOLDER_ID
    parent_payload = {
        "direct_urls": [VIDEO_URL],
        "clips": [
            {
                "title": "placeholder",
                "url": VIDEO_URL,
                "start_sec": 0.0,
                "end_sec": 4.0,
            }
        ],
        "folder_id": ROOT_FOLDER_ID,
        "folder_name": PARENT_FOLDER_NAME,
        "subfolder": "",
        "total_minutes": 1,
        "seconds_per_segment": SECONDS_PER_SEGMENT,
        "no_effects": True,
        "no_transitions": True,
        "async": True,
    }

    if args.dry_run:
        print("\n=== PARENT FOLDER PAYLOAD ===")
        print(json.dumps(parent_payload, indent=2))
        parent_folder_id = "DRY_RUN_PARENT_ID"
    else:
        print(f"\n[0/13] Creating parent folder '{PARENT_FOLDER_NAME}' under {ROOT_FOLDER_ID}...")
        status, resp = api_call(args.base_url, token, parent_payload, run_dir, "00_parent")
        print(f"  Status: {status}  Response: {json.dumps(resp, indent=2)}")
        if status not in (200, 201, 202):
            print(f"  ERROR: Failed to create parent folder. Aborting.", file=sys.stderr)
            sys.exit(1)
        # The response should contain job_id or run_id; use ROOT_FOLDER_ID as fallback
        # since the folder is created server-side and we can't get its ID from the async response
        parent_folder_id = ROOT_FOLDER_ID
        print(f"  Using parent_folder_id: {parent_folder_id}")
        time.sleep(2)

    # Phase 2: send each round
    results = []
    rounds_to_send = [r for r in ROUNDS if args.start_round <= r["num"] <= args.end_round]
    total = len(rounds_to_send)

    for i, rnd in enumerate(rounds_to_send, 1):
        payload = make_round_payload(rnd, parent_folder_id)
        label = f"{rnd['num']:02d}_round_{rnd['num']}"

        if args.dry_run:
            print(f"\n=== ROUND {rnd['num']} PAYLOAD ===")
            print(json.dumps(payload, indent=2))
            continue

        print(f"\n[{i}/{total}] Round {rnd['num']}: {rnd['name']} ({_ts(rnd['start'])} -> {_ts(rnd['end'])})")
        status, resp = api_call(args.base_url, token, payload, run_dir, label)
        print(f"  Status: {status}")
        job_id = resp.get("job_id", "N/A") if isinstance(resp, dict) else "N/A"
        print(f"  Job ID: {job_id}")
        results.append({"round": rnd["num"], "status": status, "job_id": job_id, "response": resp})

        # Brief pause between requests to avoid overwhelming the broker
        if i < total:
            time.sleep(1)

    # Summary
    if not args.dry_run:
        print(f"\n{'='*60}")
        print(f"SUMMARY: {len(results)} rounds sent")
        print(f"{'='*60}")
        ok = sum(1 for r in results if r["status"] in (200, 201, 202))
        fail = len(results) - ok
        print(f"  Accepted: {ok}")
        print(f"  Failed:   {fail}")
        for r in results:
            marker = "OK" if r["status"] in (200, 201, 202) else "FAIL"
            print(f"  Round {r['round']:2d}: [{marker}] job={r['job_id']}")
        print(f"\nResponses saved to: {run_dir}/")


if __name__ == "__main__":
    main()
