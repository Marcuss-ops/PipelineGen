#!/usr/bin/env python3
"""Canary gap analysis — registry roles vs DB assets.

For each boxer, compare the registry-defined role assets (fight/interview/
training) against what exists in media_assets, and probe search_text /
metadata_json for role hints on unbound clips.
"""

from __future__ import annotations

import json
import os
import sqlite3

DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")
REGISTRY = "tests/operational/boxers-generate/fixtures/boxers_stock_registry.json"

ROLES = ("fight", "interview", "training")


def main() -> None:
    with open(REGISTRY, encoding="utf-8") as fh:
        reg = json.load(fh)

    conn = sqlite3.connect(DB)

    print("=== REGISTRY vs DB (per boxer, per role) ===")
    for boxer_key, boxer in reg["boxers"].items():
        subject = boxer.get("subject", boxer_key)
        assets = boxer.get("assets", {})
        print(f"\n{boxer_key} ({subject}):")
        for role in ROLES:
            entry = assets.get(role)
            if entry is None:
                print(f"  {role:10s} MISSING from registry (no asset bound)")
                continue
            aid = entry.get("asset_id", "")
            row = conn.execute(
                "SELECT id, lifecycle_state, index_state, drive_file_id, search_text, metadata_json "
                "FROM media_assets WHERE id = ?",
                (aid,),
            ).fetchone()
            if row is None:
                print(f"  {role:10s} registry={aid[:38]:38s} -> ABSENT from DB")
            else:
                ok = row[1] == "ACTIVE" and row[2] == "INDEXED" and row[3]
                print(f"  {role:10s} registry={aid[:38]:38s} -> {'OK ' if ok else 'BAD'} {row[1]}/{row[2]} drive={'yes' if row[3] else 'no'}")

    print("\n=== ALL ACTIVE clips under Floyd/SugarRay folders (role hints) ===")
    rows = conn.execute(
        "SELECT id, category, source_video_id, search_text, metadata_json, folder_path "
        "FROM media_assets "
        "WHERE (folder_path LIKE 'Floyd Mayweather%' OR folder_path LIKE 'Sugar Ray Robinson%') "
        "AND lifecycle_state='ACTIVE' ORDER BY folder_path, id LIMIT 60"
    ).fetchall()
    for r in rows:
        st = str(r[3] or "").lower()
        meta = str(r[4] or "")
        hint = "UNKNOWN"
        for role in ROLES:
            if role in st or role in meta:
                hint = role
                break
        print(
            f"  id={str(r[0])[:44]:44s} cat={str(r[1])[:8]:8s} "
            f"vid={str(r[2])[:12]:12s} hint={hint:8s} folder={str(r[5])[:32]:32s} search={str(r[3])[:36]}"
        )
    conn.close()


if __name__ == "__main__":
    main()
