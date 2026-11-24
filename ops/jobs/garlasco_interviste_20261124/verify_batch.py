#!/usr/bin/env python3
"""Verify the Garlasco interview batch and optionally remove local MP4 copies.

Checks, per clip:
  * local MP4 exists, size and SHA-256 match the job result
  * Drive file id exists in the destination folder with the same size
  * media_assets row exists, ACTIVE, index_state=INDEXED, SHA matches
  * asset_locations has the Drive location with the same file id and size
  * asset_text_tracks has READY title/summary/description rows (it)
  * media_embeddings has at least one text embedding

With --apply, and only if every check passes:
  * deletes the verified local MP4 files (and same-clip .part.mp4 leftovers)
  * removes the stale local asset_locations rows and clears local_path /
    relative_path on media_assets, leaving Drive as the canonical location
  * with --delete-cas, removes the content-addressed blob too when it is not
    shared by another media_assets row and not hardlinked elsewhere

Usage:
  python3 verify_batch.py
  python3 verify_batch.py --apply
  python3 verify_batch.py --apply --delete-cas
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import urllib.parse
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
REFACTORED = HERE.parents[2]
ITEMS_PATH = HERE / "items.json"
DRIVE_PATH = HERE / "drive_ls.json"
REPORT_PATH = HERE / "verification.json"
CLEANUP_PATH = HERE / "cleanup.json"
CONTAINER = os.environ.get("PIPELINEGEN_MEDIA_PG_CONTAINER", "pipelinegen-postgres-test")


def sql_lit(value: str) -> str:
    return "'" + str(value).replace("'", "''") + "'"


def read_dsn() -> str:
    candidates: list[Path] = []
    try:
        pid = subprocess.check_output(["pgrep", "-f", "pipelinegen --mode all"], text=True).split()[0]
        candidates.append(Path(f"/proc/{pid}/environ"))
    except Exception:
        pass
    candidates.append(Path("/proc/2209/environ"))
    for path in candidates:
        try:
            raw = path.read_bytes()
        except OSError:
            continue
        for entry in raw.split(b"\0"):
            if entry.startswith(b"PIPELINEGEN_MEDIA_POSTGRES_DSN="):
                return entry.split(b"=", 1)[1].decode()
    env = os.environ.get("PIPELINEGEN_MEDIA_POSTGRES_DSN", "")
    if env:
        return env
    raise SystemExit("PIPELINEGEN_MEDIA_POSTGRES_DSN non trovato")


def pg_conn() -> tuple[str, str, str, str]:
    dsn = read_dsn()
    u = urllib.parse.urlparse(dsn)
    user = urllib.parse.unquote(u.username or "")
    password = urllib.parse.unquote(u.password or "")
    database = urllib.parse.unquote((u.path or "").lstrip("/"))
    if not user or not database:
        raise SystemExit("DSN PostgreSQL incompleto (user/database)")
    return user, password, database, dsn


def run_psql(sql: str) -> str:
    user, password, database, _ = pg_conn()
    env = os.environ.copy()
    env["PGPASSWORD"] = password
    cmd = [
        "docker", "exec", "-i", "-e", "PGPASSWORD", CONTAINER,
        "psql", "-U", user, "-d", database,
        "-v", "ON_ERROR_STOP=1", "-A", "-t", "-F", "\t", "-P", "pager=off", "-f", "-",
    ]
    proc = subprocess.run(cmd, input=sql, text=True, capture_output=True, env=env)
    if proc.returncode != 0:
        raise SystemExit(f"psql failed (rc={proc.returncode}):\n{proc.stderr.strip()}")
    return proc.stdout


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def batch_sql_rows(items: list[dict]) -> str:
    rows = []
    for item in items:
        rows.append(
            "("
            + ", ".join([
                sql_lit(item["clip_id"]),
                sql_lit(item["drive_file_id"]),
                sql_lit(item["content_sha256"]),
                str(int(item["size_bytes"])),
            ])
            + ")"
        )
    return ",\n".join(rows)


def check_db(items: list[dict]) -> dict:
    sql = f"""
WITH batch(id, drive_id, sha, size) AS (VALUES
{batch_sql_rows(items)}
)
SELECT
    b.id,
    CASE WHEN m.id IS NULL THEN 0 ELSE 1 END AS asset_exists,
    COALESCE(m.index_state, '') AS index_state,
    COALESCE(m.lifecycle_state, '') AS lifecycle_state,
    CASE WHEN m.drive_file_id = b.drive_id THEN 1 ELSE 0 END AS drive_id_ok,
    CASE WHEN m.content_sha256 = b.sha THEN 1 ELSE 0 END AS sha_ok,
    COALESCE((SELECT COUNT(*) FROM asset_locations l
              WHERE l.asset_id = b.id AND l.location_kind = 'drive'
                AND l.external_id = b.drive_id AND l.file_size_bytes = b.size), 0) AS drive_locs,
    COALESCE((SELECT COUNT(*) FROM asset_locations l
              WHERE l.asset_id = b.id AND l.location_kind = 'local'), 0) AS local_locs,
    COALESCE((SELECT MAX(l.is_primary) FROM asset_locations l
              WHERE l.asset_id = b.id AND l.location_kind = 'local'), 0) AS local_primary,
    COALESCE((SELECT MAX(l.is_primary) FROM asset_locations l
              WHERE l.asset_id = b.id AND l.location_kind = 'drive'), 0) AS drive_primary,
    COALESCE((SELECT COUNT(DISTINCT t.text_kind) FROM asset_text_tracks t
              WHERE t.asset_id = b.id AND t.is_current = 1 AND t.status = 'READY'
                AND t.language_code = 'it'
                AND t.text_kind IN ('title', 'summary', 'description')
                AND t.text_content <> ''), 0) AS text_kinds,
    COALESCE((SELECT COUNT(*) FROM media_embeddings e
              WHERE e.asset_id = b.id AND e.embedding_type = 'text'), 0) AS text_embeddings,
    COALESCE((SELECT COUNT(*) FROM media_assets x
              WHERE x.content_sha256 = b.sha), 0) AS sha_refs,
    COALESCE(m.local_path, '') AS asset_local_path
FROM batch b
LEFT JOIN media_assets m ON m.id = b.id
ORDER BY b.id
"""
    out = run_psql(sql)
    rows: dict[str, dict] = {}
    for line in out.splitlines():
        if not line.strip():
            continue
        parts = line.split("\t")
        if len(parts) < 14:
            raise SystemExit(f"riga psql inattesa ({len(parts)} campi): {line!r}")
        key = parts[0]
        rows[key] = {
            "asset_exists": int(parts[1]),
            "index_state": parts[2],
            "lifecycle_state": parts[3],
            "drive_id_ok": int(parts[4]),
            "sha_ok": int(parts[5]),
            "drive_locs": int(parts[6]),
            "local_locs": int(parts[7]),
            "local_primary": int(parts[8]),
            "drive_primary": int(parts[9]),
            "text_kinds": int(parts[10]),
            "text_embeddings": int(parts[11]),
            "sha_refs": int(parts[12]),
            "asset_local_path": parts[13],
        }
    return rows


def cleanup_local(items: list[dict], local_paths: dict[str, Path]) -> dict:
    deleted = []
    parts_deleted = []
    freed = 0
    for item in items:
        path = local_paths.get(item["clip_id"])
        if path and path.is_file():
            try:
                size = path.stat().st_size
                path.unlink()
                freed += size
                deleted.append(str(path.relative_to(REFACTORED)))
            except OSError as exc:
                return {"error": f"delete {path}: {exc}", "deleted": deleted, "parts_deleted": parts_deleted}
        directory = path.parent if path else None
        if directory and directory.is_dir():
            for leftover in directory.glob(item["clip_id"] + "*.mp4"):
                if leftover.is_file():
                    try:
                        size = leftover.stat().st_size
                        leftover.unlink()
                        freed += size
                        parts_deleted.append(str(leftover.relative_to(REFACTORED)))
                    except OSError as exc:
                        return {"error": f"delete {leftover}: {exc}", "deleted": deleted, "parts_deleted": parts_deleted}
    return {"deleted": deleted, "parts_deleted": parts_deleted, "freed_bytes": freed}


def cleanup_db(items: list[dict]) -> dict:
    ids = ",\n".join(f"({sql_lit(item['clip_id'])})" for item in items)
    sql = f"""
BEGIN;
CREATE TEMP TABLE batch_ids(id text PRIMARY KEY) ON COMMIT DROP;
INSERT INTO batch_ids(id) VALUES
{ids};
UPDATE asset_locations SET is_primary = 1
 WHERE asset_id IN (SELECT id FROM batch_ids) AND location_kind = 'drive' AND is_primary = 0;
DELETE FROM asset_locations
 WHERE asset_id IN (SELECT id FROM batch_ids) AND location_kind = 'local';
UPDATE media_assets SET local_path = '', relative_path = ''
 WHERE id IN (SELECT id FROM batch_ids);
COMMIT;
"""
    out = run_psql(sql)
    # The command tags are emitted on stdout with -A -t; report them verbatim.
    return {"psql": [line for line in out.splitlines() if line.strip()]}


def cleanup_cas(items: list[dict], local_paths: dict[str, Path], db_rows: dict[str, dict]) -> dict:
    deleted = []
    kept = []
    freed = 0
    for item in items:
        sha = item["content_sha256"]
        cas = REFACTORED / "data" / "cas" / sha[:2] / sha[2:4] / sha
        if not cas.is_file():
            continue
        st = cas.stat()
        media = local_paths.get(item["clip_id"])
        same_inode = False
        if media and media.exists():
            try:
                same_inode = os.path.samefile(media, cas)
            except OSError:
                same_inode = False
        refs = db_rows.get(item["clip_id"], {}).get("sha_refs", 0)
        safe = refs <= 1 and st.st_nlink <= 1
        if same_inode and st.st_nlink <= 2:
            safe = refs <= 1
        if same_inode:
            # The media file was just deleted, so one link is already gone.
            safe = refs <= 1 and st.st_nlink <= 2
        if not safe:
            kept.append({"clip_id": item["clip_id"], "sha256": sha, "nlink": st.st_nlink, "sha_refs": refs})
            continue
        try:
            size = st.st_size
            cas.unlink()
            freed += size
            deleted.append({"clip_id": item["clip_id"], "sha256": sha, "bytes": size})
        except OSError as exc:
            kept.append({"clip_id": item["clip_id"], "sha256": sha, "error": str(exc)})
    return {"deleted": deleted, "kept": kept, "freed_bytes": freed}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true", help="delete verified local MP4 files and stale local DB locations")
    ap.add_argument("--delete-cas", action="store_true", help="also delete unshared CAS blobs")
    args = ap.parse_args()

    items = json.loads(ITEMS_PATH.read_text(encoding="utf-8"))
    drive = json.loads(DRIVE_PATH.read_text(encoding="utf-8"))
    if len(items) != 154:
        raise SystemExit(f"attesi 154 item, trovati {len(items)}")
    if len({x['clip_id'] for x in items}) != 154:
        raise SystemExit("clip_id duplicati in items.json")
    if len({x['drive_file_id'] for x in items}) != 154:
        raise SystemExit("drive_file_id duplicati in items.json")

    drive_entries = {}
    for entry in drive.get("entries", []):
        if entry.get("type") != "file":
            continue
        if entry.get("mime_type") == "video/mp4" or str(entry.get("name", "")).endswith(".mp4"):
            drive_entries[entry["id"]] = entry

    drive_report = {
        "entries": len(drive_entries),
        "expected_ids": len(items),
        "missing": [],
        "size_mismatch": [],
        "type_mismatch": [],
        "extra": sorted(set(drive_entries) - {x['drive_file_id'] for x in items}),
    }
    for item in items:
        entry = drive_entries.get(item["drive_file_id"])
        if entry is None:
            drive_report["missing"].append(item["clip_id"])
            continue
        if int(entry.get("size") or 0) != int(item["size_bytes"]):
            drive_report["size_mismatch"].append({
                "clip_id": item["clip_id"],
                "drive_size": entry.get("size"),
                "expected_size": item["size_bytes"],
            })
        if entry.get("mime_type") != "video/mp4" and not str(entry.get("name", "")).endswith(".mp4"):
            drive_report["type_mismatch"].append(item["clip_id"])

    db_rows = check_db(items)
    db_failures = []
    text_missing = []
    embedding_missing = []
    for item in items:
        row = db_rows.get(item["clip_id"])
        if not row:
            db_failures.append({"clip_id": item["clip_id"], "error": "row assente"})
            continue
        problems = []
        if row["asset_exists"] != 1:
            problems.append("asset assente")
        if row["index_state"] != "INDEXED":
            problems.append(f"index_state={row['index_state']}")
        if row["lifecycle_state"] != "ACTIVE":
            problems.append(f"lifecycle_state={row['lifecycle_state']}")
        if row["sha_ok"] != 1:
            problems.append("content_sha256 diverso")
        if row["drive_locs"] < 1:
            problems.append("asset_locations drive assente o size diversa")
        if row["text_kinds"] < 3:
            text_missing.append({"clip_id": item["clip_id"], "text_kinds": row["text_kinds"]})
        if row["text_embeddings"] < 1:
            embedding_missing.append({"clip_id": item["clip_id"], "text_embeddings": row["text_embeddings"]})
        if problems:
            db_failures.append({"clip_id": item["clip_id"], "problems": problems, **row})

    local_report = {
        "expected": len(items),
        "present": 0,
        "already_absent": [],
        "size_mismatch": [],
        "sha_mismatch": [],
        "invalid_path": [],
        "bytes_present": 0,
    }
    local_paths: dict[str, Path] = {}
    base = (REFACTORED / "data" / "media" / "clips").resolve()
    for item in items:
        raw = item.get("local_path") or ""
        path = Path(raw)
        if not path.is_absolute():
            path = REFACTORED / path
        try:
            resolved = path.resolve()
        except OSError:
            resolved = path
        if not str(resolved).startswith(str(base) + os.sep):
            local_report["invalid_path"].append({"clip_id": item["clip_id"], "path": str(path)})
            continue
        local_paths[item["clip_id"]] = path
        if not path.is_file():
            local_report["already_absent"].append(item["clip_id"])
            continue
        local_report["present"] += 1
        size = path.stat().st_size
        local_report["bytes_present"] += size
        if size != int(item["size_bytes"]):
            local_report["size_mismatch"].append({
                "clip_id": item["clip_id"], "actual": size, "expected": item["size_bytes"]
            })
            continue
        actual_sha = sha256_file(path)
        if actual_sha != item["content_sha256"]:
            local_report["sha_mismatch"].append({
                "clip_id": item["clip_id"], "actual": actual_sha, "expected": item["content_sha256"]
            })

    failures = (
        len(drive_report["missing"]) + len(drive_report["size_mismatch"]) + len(drive_report["type_mismatch"])
        + len(drive_report["extra"]) + len(db_failures) + len(text_missing) + len(embedding_missing)
        + len(local_report["size_mismatch"]) + len(local_report["sha_mismatch"])
        + len(local_report["invalid_path"])
    )
    report = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "apply": args.apply,
        "delete_cas": args.delete_cas,
        "totals": {
            "items": len(items),
            "expected_bytes": sum(int(x["size_bytes"]) for x in items),
            "drive_entries": len(drive_entries),
            "db_rows": len(db_rows),
            "local_present": local_report["present"],
            "failures": failures,
        },
        "drive": drive_report,
        "db": {
            "indexed": sum(1 for x in db_rows.values() if x["index_state"] == "INDEXED"),
            "active": sum(1 for x in db_rows.values() if x["lifecycle_state"] == "ACTIVE"),
            "drive_locations": sum(1 for x in db_rows.values() if x["drive_locs"] >= 1),
            "local_locations": sum(1 for x in db_rows.values() if x["local_locs"] >= 1),
            "local_paths_nonempty": sum(1 for x in db_rows.values() if x["asset_local_path"]),
            "failures": db_failures,
            "text_missing": text_missing,
            "embedding_missing": embedding_missing,
        },
        "local": local_report,
    }

    print(f"Drive: {len(drive_entries)}/154 MP4, missing={len(drive_report['missing'])}, "
          f"size_mismatch={len(drive_report['size_mismatch'])}, extra={len(drive_report['extra'])}")
    print(f"DB: indexed={report['db']['indexed']}/154, active={report['db']['active']}/154, "
          f"drive_locations={report['db']['drive_locations']}/154, "
          f"local_locations={report['db']['local_locations']}/154, "
          f"local_paths_nonempty={report['db']['local_paths_nonempty']}/154, "
          f"text_missing={len(text_missing)}, embedding_missing={len(embedding_missing)}, failures={len(db_failures)}")
    print(f"Local: present={local_report['present']}/154, bytes={local_report['bytes_present']}, "
          f"absent={len(local_report['already_absent'])}, size_mismatch={len(local_report['size_mismatch'])}, "
          f"sha_mismatch={len(local_report['sha_mismatch'])}, invalid_path={len(local_report['invalid_path'])}")
    print(f"Failures totali: {failures}")

    if failures:
        print("\nVerifica fallita: nessuna cancellazione eseguita.", file=sys.stderr)
        for key in ("missing", "size_mismatch", "type_mismatch", "extra"):
            if drive_report[key]:
                print(f"  drive.{key}: {drive_report[key][:5]}", file=sys.stderr)
        for row in db_failures[:5]:
            print(f"  db: {row}", file=sys.stderr)
        for row in text_missing[:5]:
            print(f"  text: {row}", file=sys.stderr)
        for row in embedding_missing[:5]:
            print(f"  embedding: {row}", file=sys.stderr)
        REPORT_PATH.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        return 1

    if not args.apply:
        REPORT_PATH.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print("Verifica OK. Nessuna cancellazione (usa --apply).")
        return 0

    # Final safety gate: every check above passed.
    cleanup = cleanup_local(items, local_paths)
    if "error" in cleanup:
        report["cleanup"] = cleanup
        REPORT_PATH.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"ERRORE durante la cancellazione: {cleanup['error']}", file=sys.stderr)
        return 1
    cleanup["db"] = cleanup_db(items)
    if args.delete_cas:
        cleanup["cas"] = cleanup_cas(items, local_paths, db_rows)
    report["cleanup"] = cleanup
    CLEANUP_PATH.write_text(json.dumps(cleanup, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    REPORT_PATH.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    print(f"Cleanup: deleted={len(cleanup['deleted'])} mp4, parts={len(cleanup['parts_deleted'])}, "
          f"freed_bytes={cleanup['freed_bytes']}")
    print(f"DB cleanup: {cleanup['db'].get('psql')}")
    if args.delete_cas:
        print(f"CAS cleanup: deleted={len(cleanup['cas']['deleted'])}, kept={len(cleanup['cas']['kept'])}, "
              f"freed_bytes={cleanup['cas']['freed_bytes']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
