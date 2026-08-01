#!/usr/bin/env python3
"""Canary preflight — single aggregated check (August 2026 protocol).

Checks: service active, /ready, token, registry asset states (ACTIVE+INDEXED),
outbox queue depth. Prints only the essential facts.
"""

from __future__ import annotations

import json
import os
import re
import sqlite3
import subprocess
import sys
import urllib.request

BASE = os.environ.get("VELOX_BASE_URL") or "http://127.0.0.1:8000"
DB = os.environ.get("VELOX_DB", "data/media/media.db.sqlite")
TOKEN_FILE = "/etc/pipelinegen/pipelinegen.env"
REGISTRY = "tests/operational/boxers-generate/fixtures/boxers_stock_registry.json"


def load_token() -> str:
    # Canonical SSOT (AGENTS.md): the file token wins. The process env may
    # carry a stale rotated token, so prefer /etc/pipelinegen/pipelinegen.env
    # and fall back to the env var only when the file is unavailable.
    tok = ""
    try:
        with open(TOKEN_FILE, encoding="utf-8") as fh:
            for line in fh:
                if line.startswith("VELOX_ADMIN_TOKEN="):
                    tok = line.split("=", 1)[1].strip()
                    break
    except OSError as exc:
        print(f"token file unreadable: {exc}")
    if not tok:
        tok = os.environ.get("VELOX_ADMIN_TOKEN", "")
    if not re.fullmatch(r"[a-fA-F0-9]{64}", tok or ""):
        print("token INVALID (not 64-hex)")
        return ""
    return tok


def http_get(path: str, token: str) -> tuple[int, str]:
    req = urllib.request.Request(BASE + path, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:  # noqa: BLE001
        return -1, str(exc)


def main() -> int:
    print("=== CANARY PREFLIGHT ===")
    # 1. service + ready
    try:
        svc = subprocess.run(["systemctl", "is-active", "pipelinegen"], capture_output=True, text=True, timeout=10)
        print("service:", svc.stdout.strip() or svc.stderr.strip())
    except Exception as exc:  # noqa: BLE001
        print("service check failed:", exc)
    code, body = http_get("/ready", "")
    try:
        ready = json.loads(body)
        print(f"/ready: ok={ready.get('ok')} status={ready.get('status')}")
        chk = ready.get("checks", {})
        for key in ("outbox", "jobs", "qdrant", "drive_root"):
            if key in chk:
                print(f"  {key}: ok={chk[key].get('ok')}")
    except Exception:  # noqa: BLE001
        print(f"/ready HTTP={code} raw={body[:200]}")

    # 2. token
    token = load_token()
    if token:
        code, _ = http_get("/api/jobs", token)
        print(f"token auth /api/jobs: HTTP={code} (401=unauthorized, 200=ok)")

    # 3. registry asset states
    with open(REGISTRY, encoding="utf-8") as fh:
        reg = json.load(fh)
    ids: dict[str, tuple[str, str]] = {}
    for boxer_key, boxer in reg["boxers"].items():
        for role, asset in boxer.get("assets", {}).items():
            ids[asset["asset_id"]] = (boxer_key, role)
    print(f"registry assets: {len(ids)} (target 15 = 5 boxers x 3 roles)")
    if not os.path.exists(DB):
        print(f"DB NOT FOUND: {DB}")
    else:
        conn = sqlite3.connect(DB)
        placeholders = ",".join("?" * len(ids))
        rows = conn.execute(
            f"SELECT id, lifecycle_state, index_state, drive_file_id, drive_link "
            f"FROM media_assets WHERE id IN ({placeholders})",
            list(ids),
        ).fetchall()
        found = {r[0]: r for r in rows}
        ok = 0
        for aid, (boxer, role) in sorted(ids.items(), key=lambda kv: (kv[1][0], kv[1][1])):
            r = found.get(aid)
            if r is None:
                print(f"  MISS {aid} [{boxer}.{role}] ABSENT")
            else:
                good = r[1] == "ACTIVE" and r[2] == "INDEXED" and r[3]
                if good:
                    ok += 1
                print(f"  {'OK  ' if good else 'MISS'} {aid} [{boxer}.{role}] {r[1]}/{r[2]} drive={'yes' if r[3] else 'no'}")
        print(f"ACTIVE+INDEXED+drive: {ok}/{len(ids)}")
        print("--- outbox queue ---")
        for status in ("pending", "processing", "completed", "dead_letter"):
            n = conn.execute("SELECT COUNT(*) FROM outbox_events WHERE status=?", (status,)).fetchone()[0]
            print(f"  outbox {status}: {n}")
        conn.close()
    print("=== PREFLIGHT DONE ===")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
