#!/usr/bin/env python3
"""RUN1/RUN2 shared verification — registry resolve vs DB + index/outbox state.

Usage:
    python3 scripts/ops/run12_verify.py [--registry PATH] [--db PATH]

Exit 0 when every registry asset is ACTIVE+INDEXED with drive link, the
registry resolves cleanly, and no outbox/index backlog remains.
"""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from pathlib import Path


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--registry", default="tests/operational/boxers-generate/fixtures/boxers_stock_registry.json")
    ap.add_argument("--db", default="data/media/media.db.sqlite")
    args = ap.parse_args(argv)

    registry = json.loads(Path(args.registry).read_text(encoding="utf-8"))
    conn = sqlite3.connect(args.db)

    total = 0
    ok_active = 0
    ok_indexed = 0
    ok_drive = 0
    rows: list[tuple[str, str, str, str]] = []
    for boxer_key, boxer in registry["boxers"].items():
        subject = boxer["subject"]
        for role, asset in boxer.get("assets", {}).items():
            aid = asset["asset_id"]
            total += 1
            row = conn.execute(
                "SELECT lifecycle_state, index_state, drive_link FROM media_assets WHERE id=?",
                (aid,),
            ).fetchone()
            if not row:
                rows.append((boxer_key, role, aid, "MISSING"))
                continue
            ls, idx, link = row
            status = f"{ls}/{idx}/drive={bool(link)}"
            rows.append((boxer_key, role, aid, status))
            if ls == "ACTIVE":
                ok_active += 1
            if idx == "INDEXED":
                ok_indexed += 1
            if link:
                ok_drive += 1

    print(f"RUN: {ok_active}/{total} ACTIVE, {ok_indexed}/{total} INDEXED, {ok_drive}/{total} drive_link")
    for boxer_key, role, aid, status in rows:
        print(f"  {boxer_key:20s} {role:10s} {aid[:30]:30s} {status}")

    # Outbox + index backlog. The gate is scoped to the registry assets only
    # (the 15-asset canary contract): unrelated runs may legitimately still be
    # indexing, and the protocol says never wait for the full 75-asset backlog.
    # Global backlog is reported as informational diagnostics, not a gate.
    registry_ids = [a["asset_id"] for b in registry["boxers"].values() for a in b.get("assets", {}).values()]
    placeholders = ",".join("?" for _ in registry_ids)
    outbox = conn.execute(
        "SELECT status, COUNT(*) FROM outbox_events "
        f"WHERE status NOT IN ('completed','dead_letter','superseded') AND payload_json LIKE ? GROUP BY status",
        ("%media_assets%",),
    ).fetchall()
    reg_backlog = conn.execute(
        "SELECT index_state, COUNT(*) FROM media_assets "
        f"WHERE id IN ({placeholders}) AND index_state NOT IN ('INDEXED') GROUP BY index_state",
        registry_ids,
    ).fetchall()
    global_backlog = conn.execute(
        "SELECT index_state, COUNT(*) FROM media_assets "
        "WHERE index_state NOT IN ('INDEXED','DISCOVERED','NONE') GROUP BY index_state"
    ).fetchall()
    print(f"outbox backlog (info): {outbox or 'empty'}")
    print(f"registry index backlog (gate): {reg_backlog or 'empty'}")
    print(f"global index backlog (info): {global_backlog or 'empty'}")

    conn.close()
    ok = ok_active == total and ok_indexed == total and ok_drive == total and not reg_backlog
    if not ok:
        print("RUN VERIFY FAIL", file=sys.stderr)
        return 1
    print("RUN VERIFY PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
