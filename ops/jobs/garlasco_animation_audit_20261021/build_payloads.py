#!/usr/bin/env python3
"""Build the two Garlasco 01 animation-audit payloads from the r9 payload.

The source payload is never modified: both audit payloads are copies with a
new identity, every overlay category enabled, charts explicitly opted in and
an independent bullet quota. `overlay.json` certifies the per-item overlay
render; `final.json` additionally requests the Master 51 composite.

Usage:
  python3 ops/jobs/garlasco_animation_audit_20261021/build_payloads.py
"""

from __future__ import annotations

import copy
import json
from pathlib import Path

TASK_DIR = Path(__file__).resolve().parent
REPO = TASK_DIR.parents[2]
SOURCE = (
    REPO
    / "ops/jobs/garlasco_batch_10_20261019_sequential_v7/resubmit_r9/01_cronologia.r9.json"
)
PAYLOAD_DIR = TASK_DIR / "payloads"

# The r9 payload caps grounded phrases at 10 short + 10 long. Bullets get a
# separate lane so enabling them cannot evict a requested phrase.
BULLET_QUOTA = 4

AUDIT_STYLE = (
    "Mantieni ogni source_text esattamente verbatim. "
    "Non inventare né cambiare fatti, date, persone, "
    "numeri o attribuzioni. Distingui sempre la "
    "condanna definitiva di Alberto Stasi dal distinto "
    "procedimento che riguarda Andrea Sempio. "
    "Usa immagini verificate per persone e oggetti "
    "effettivamente nominati. Raggruppa immagini "
    "di entità co-menzionate quando tutti gli asset "
    "e i timing sono certificati. "
    "Mappe solo di località reali nominate, come "
    "Garlasco, Pavia, Milano e Vigevano. "
    "Estrai frasi brevi e lunghe dalla narrazione. "
    "Estrai bullet grounded, senza inventarli. "
    "Genera charts solo per dati numerici "
    "effettivamente comparabili nella stessa scena; "
    "mai grafici costruiti soltanto da date. "
    "Grafica Playfair Display Italic, "
    "testo nero su fondo bianco pieno."
)


def build(profile: str, final_job: bool, base: dict) -> dict:
    payload = copy.deepcopy(base)
    item = payload["items"][0]
    identity = f"garlasco-01-animation-audit-20261021-{profile}"

    payload["correlation_id"] = identity
    payload["final_job"] = final_job
    payload["force_refresh"] = False

    item["id"] = identity
    item.setdefault("output", {}).setdefault("render", {})["drive_subfolder_name"] = identity
    item["output"]["drive_subfolder_name"] = identity

    for category in ("maps", "phrases", "images", "numbers", "bullets", "charts"):
        item["overlay_categories"][category] = "enabled"

    # Charts require the explicit opt-in in addition to the category toggle.
    item["enable_charts"] = True
    item["disable_chart_overlays"] = False
    item["disable_editorial_overlays"] = False

    # r9 explicitly disabled geocoding at the media-plan level. The maps
    # category alone cannot make grounded place coordinates available.
    item["media_plan"]["provider_policy"]["geocoding"] = "enabled"

    # Keep the 10+10 phrase contract and open an independent bullet lane.
    item["max_phrase_overlays"] = 24
    limits = dict(item.get("overlay_group_limits") or {})
    limits["bullets"] = BULLET_QUOTA
    item["overlay_group_limits"] = limits

    item.pop("phrase_motions", None)
    item["phrase_motion_family"] = "modern_product_v1"
    item["style"] = AUDIT_STYLE
    return payload


def main() -> int:
    base = json.loads(SOURCE.read_text(encoding="utf-8"))
    PAYLOAD_DIR.mkdir(parents=True, exist_ok=True)
    for profile, final_job in (("overlay", False), ("final", True)):
        payload = build(profile, final_job, base)
        target = PAYLOAD_DIR / f"{profile}.json"
        target.write_text(
            json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        item = payload["items"][0]
        print(f"{profile} -> {target.relative_to(REPO)}")
        print(f"  final_job: {final_job}")
        print(f"  scenes: {len(item['script_params']['segments'])}")
        print(f"  categories: {item['overlay_categories']}")
        print(f"  group_limits: {item['overlay_group_limits']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
