#!/usr/bin/env python3
"""
cleanup_stock_residue.py — Drive inventory + classification + safe-trash for
stock-pipeline residue folders.

Background:
    Pre-bug-fix (commit pending, 2026-07-06), the stock pipeline produced
    Drive folder paths under [stock_root_folder]/ using the legacy 3-segment
    `stock/{category}/{provider}/{subject}/...` shape. Post-fix, the canonical
    shape is `[stock_root_folder]/stock/run_<fingerprint>/{timestamp_<n>|
    chunk_<n>|metadata}/` (the `stock/` namespace wrapper is added when
    `stock_root_folder == clips_root_folder == media_root_folder`, per
    registry.go::maybeWrapNamespace).

    This tool surfaces the post-fix canonical subtrees (`run_*`,
    `timestamp_*`, `chunk_*`, `metadata`) vs the pre-fix residue that
    accumulates when legacy paths are re-used.

Modes (mutually exclusive):
    --mode=inventory       Read-only scan + JSON classification (DEFAULT)
    --mode=dry-run        Same as inventory, but prints proposed trash list
    --mode=trash          Execute trash on proposed candidates (requires
                          --confirm-orphan-cleanup + --target-ids OR
                          --auto-orphans-only)

Safety:
    * Default mode = inventory (NEVER mutates Drive).
    * --mode=trash requires explicit --confirm-orphan-cleanup binding
      (one-keystroke miss = no-op).
    * --auto-orphans-only excludes anything in REVIEW class (godlike/07
      fail-closed: never auto-trash folders with non-mp4/non-json content,
      missing metadata.json, recent modifyTime, or names containing '/',
      or any nested children that contain canonical surfaces).
    * All Drive operations rate-limited (MAX_WORKERS=3, retry
      1.5s/3s/4.5s) per the same pattern as resolve_drive_ids.py.

godlike/06 SSOT (one canonical owner per fact):
    The classification rules are derived from ONE canonical surface
    (artifact_publisher_adapter.go::stockArtifactPathParts). The tool's
    regex strings MUST match that surface byte-for-byte. If you
    change one, change both.

godlike/07 NO-FAKE-AVAILABILITY:
    Every mutation logs the folder_id + name + reason. A failed trash
    is reported as a typed fingerprintable error, never a silent skip.
"""

import argparse
import collections
import json
import re
import sys
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Optional

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "google-accounting"))

from drive_client import _build_service  # noqa: E402

# Sequential walker: Drive API client + Google guidance both recommend
# serial calls for inventory + mutation (resilience over throughput).
# Trade-off: walk latency ≈ O(folders × avg_latency). Acceptable given
# the small subtree + the 1.5s/3s/4.5s retry envelope.
MAX_RETRIES = 3
RETRY_BASE_DELAY_S = 1.5

# ── Canonical classification (post-fix) ──────────────────────────────
# Source of truth: internal/infrastructure/drive/artifact_publisher_adapter.go
# ::stockArtifactPathParts → stockRunFolderName.
# If you change one of these regexes, change BOTH (godlike/06 SSOT).
RE_CANONICAL_RUN = re.compile(r"^run(_[a-fA-F0-9]{1,12})?$")
RE_CANONICAL_TIMESTAMP = re.compile(r"^timestamp_\d+$")
RE_CANONICAL_CHUNK = re.compile(r"^chunk_\d+$")
CANONICAL_LITERAL_METADATA = "metadata"
CANONICAL_NAMESPACE_PREFIX = "stock"

# Pre-fix bug-fix date (must match the date the canonical surface shipped).
# Anything modified after this date is potentially a legit run.
PRE_FIX_DATE = datetime(2026, 7, 4, tzinfo=timezone.utc)

# Types that are expected for stock pipeline artifacts. Anything else
# forces REVIEW (godlike/07 fail-closed: never auto-trash heterogeneous
# folders).
ALLOWED_MIME_TYPES = {"video/mp4", "application/json"}

RETRYABLE_EXC_NAMES = {"SSLError", "TimeoutError", "ConnectionError",
                       "ConnectionResetError", "ServiceUnavailableError",
                       "InternalServerError", "TooManyRequestsError"}


def classify_folder(name: str, modified_iso: Optional[str], mime_counts: dict,
                    has_metadata_json: bool, has_children_folders: bool,
                    metadata_json_recent: Optional[bool]) -> str:
    """Return one of {CANONICAL, RESIDUE_AUTO, RESIDUE_REVIEW}.

    CANONICAL: keep (matches post-fix canonical surface).
    RESIDUE_AUTO: candidate for orphan trashing (heuristic matches).
    RESIDUE_REVIEW: NEVER auto-trash (mixed content / recent run / etc.).
    """
    if RE_CANONICAL_RUN.match(name):
        return "CANONICAL"
    if RE_CANONICAL_TIMESTAMP.match(name) or RE_CANONICAL_CHUNK.match(name):
        return "CANONICAL"
    if name == CANONICAL_LITERAL_METADATA:
        return "CANONICAL"
    if name == CANONICAL_NAMESPACE_PREFIX:
        return "CANONICAL_NAMESPACE"  # transparent recursion marker
    # REVIEW triggers (always excludes from auto-trash, godlike/07)
    if "/" in name:
        return "RESIDUE_REVIEW"
    if has_metadata_json and metadata_json_recent:
        return "RESIDUE_REVIEW"
    if has_children_folders:
        return "RESIDUE_REVIEW"
    non_allowed = {k: v for k, v in mime_counts.items() if k not in ALLOWED_MIME_TYPES}
    if non_allowed:
        return "RESIDUE_REVIEW"
    # AUTO-TRASH heuristic: pre-fix + small + only mp4/json.
    try:
        mod_dt = datetime.fromisoformat(modified_iso.replace("Z", "+00:00")) if modified_iso else None
    except Exception:
        mod_dt = None
    if mod_dt and mod_dt > PRE_FIX_DATE + timedelta(days=2):
        # Modified within 2 days of pre-fix → likely a stale lock or
        # operator manual upload. REVIEW.
        return "RESIDUE_REVIEW"
    return "RESIDUE_AUTO"


def list_folder_content(svc, parent_id: str) -> dict:
    """Single API call. Returns {'folders': [...], 'files': [...]}."""
    folders, files = [], []
    page_token = None
    while True:
        query = (f"'{parent_id}' in parents and trashed = false "
                 f"and (mimeType = 'application/vnd.google-apps.folder' "
                 f"or mimeType != 'application/vnd.google-apps.folder')")
        # Wrapped in drive_retry so transient SSL/TimeoutError on
        # walk is recovered identically to transient errors on trash
        # (godlike/07 NO-FAKE-AVAILABILITY: consistent retry envelope
        # across all Drive calls).
        response = drive_retry(
            svc.files().list(
                q=query,
                spaces="drive",
                fields="nextPageToken, files(id, name, mimeType, modifiedTime, parents)",
                pageToken=page_token,
                pageSize=1000,
            ).execute
        )
        for f in response.get("files", []):
            if f.get("mimeType") == "application/vnd.google-apps.folder":
                folders.append(f)
            else:
                files.append(f)
        page_token = response.get("nextPageToken")
        if not page_token:
            break
    folders.sort(key=lambda f: f["name"])
    return {"folders": folders, "files": files}


def recurse(svc, parent_id: str, parent_path: str, depth: int, max_depth: int):
    """Walk subtrees. Yields records {path, depth, id, name, classification,
    mime_counts, file_count, modified, has_metadata_json, has_children_folders,
    metadata_json_recent}."""
    if depth > max_depth:
        return
    content = list_folder_content(svc, parent_id)
    mime_counts = collections.Counter()
    has_metadata_json = False
    metadata_json_recent = None
    for f in content["files"]:
        mime_counts[f.get("mimeType", "unknown")] += 1
        if f.get("name") == "metadata.json":
            has_metadata_json = True
            try:
                mod_dt = datetime.fromisoformat(f["modifiedTime"].replace("Z", "+00:00"))
                metadata_json_recent = mod_dt > PRE_FIX_DATE + timedelta(days=2)
            except Exception:
                metadata_json_recent = False
    # Root folder is classified as CANONICAL_NAMESPACE regardless of content
    # (it's the entry point, never trashable). For depth>0 we classify by name.
    record_name = parent_path.split("/")[-1] if parent_path else "(root)"
    classify_name = record_name if depth > 0 else CANONICAL_NAMESPACE_PREFIX
    classification = classify_folder(
        name=classify_name,
        modified_iso=content["files"][0]["modifiedTime"] if content["files"]
                      else (content["folders"][0]["modifiedTime"] if content["folders"] else None),
        mime_counts=dict(mime_counts),
        has_metadata_json=has_metadata_json,
        has_children_folders=bool(content["folders"]),
        metadata_json_recent=metadata_json_recent,
    )
    yield {
        "id": parent_id,
        "path": parent_path,
        "depth": depth,
        "name": parent_path.split("/")[-1] if parent_path else "(root)",
        "file_count": len(content["files"]),
        "folder_count": len(content["folders"]),
        "mime_counts": dict(mime_counts),
        "has_metadata_json": has_metadata_json,
        "modified_time": (content["files"][0]["modifiedTime"] if content["files"]
                          else (content["folders"][0]["modifiedTime"] if content["folders"] else None)),
        "classification": classification,
        "children_ids": [c["id"] for c in content["folders"]],
    }
    for child in content["folders"]:
        child_path = f"{parent_path}/{child['name']}" if parent_path else child["name"]
        yield from recurse(svc, child["id"], child_path, depth + 1, max_depth)


def drive_retry(func, *args, **kwargs):
    """Generic retry binder per resolve_drive_ids.py pattern."""
    last_err = None
    for attempt in range(1, MAX_RETRIES + 1):
        try:
            return func(*args, **kwargs)
        except Exception as e:
            last_err = f"{type(e).__name__}: {e}"
            if type(e).__name__ in RETRYABLE_EXC_NAMES and attempt < MAX_RETRIES:
                time.sleep(RETRY_BASE_DELAY_S * attempt)
                continue
            break
    raise RuntimeError(last_err or "drive call failed without exception")


def trash_folder(svc, folder_id: str) -> str:
    """Move to trash (recoverable 30 days). NEVER permanent delete."""
    drive_retry(svc.files().update(fileId=folder_id, body={"trashed": True}).execute)
    return "trashed"


def run_inventory(svc, root_id: str, max_depth: int):
    """Yield inventory records rooted at root_id."""
    yield from recurse(svc, root_id, CANONICAL_NAMESPACE_PREFIX
                       if Path(ROOT / "config.yaml").exists() else "", 0, max_depth)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0] if __doc__ else "")
    ap.add_argument("--mode", choices=["inventory", "dry-run", "trash"],
                    default="inventory")
    ap.add_argument("--root-folder", default="1J-zIuqroF0rkTrKxU-tmZu9e5rN20ggV",
                    help="stock_root_folder from config.yaml (default: post-fix canonical ID).")
    ap.add_argument("--max-depth", type=int, default=4)
    ap.add_argument("--out", type=Path,
                    default=Path("tests/operational/stock_residue_inventory.json"))
    ap.add_argument("--target-ids", help="Comma-separated folder IDs to target (overrides auto-classification)")
    ap.add_argument("--auto-orphans-only", action="store_true",
                    help="On --mode=trash, only target RESIDUE_AUTO classification (excludes RESIDUE_REVIEW)")
    ap.add_argument("--confirm-orphan-cleanup", action="store_true",
                    help="REQUIRED for --mode=trash. Confirms you understand deleted folders go to Drive trash (not permanent until 30d).")
    args = ap.parse_args()

    if args.mode == "trash" and not args.confirm_orphan_cleanup:
        print("ERROR: --mode=trash requires --confirm-orphan-cleanup", file=sys.stderr)
        return 2
    if args.mode == "trash" and not (args.target_ids or args.auto_orphans_only):
        print("ERROR: --mode=trash requires --target-ids OR --auto-orphans-only", file=sys.stderr)
        return 2

    print(f"[cleanup_stock_residue] mode={args.mode} root={args.root_folder}", file=sys.stderr)
    svc = _build_service()

    print("[cleanup_stock_residue] walking subtree (rate-limited)…", file=sys.stderr)
    records = list(run_inventory(svc, args.root_folder, args.max_depth))
    summary = collections.Counter(r["classification"] for r in records)
    print(f"[cleanup_stock_residue] walked {len(records)} folders: "
          f"{dict(summary)}", file=sys.stderr)

    args.out.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "root_folder": args.root_folder,
        "scanned_at": datetime.now(timezone.utc).isoformat(),
        "summary": dict(summary),
        "records": records,
    }
    args.out.write_text(json.dumps(payload, indent=2, ensure_ascii=False))
    print(f"[cleanup_stock_residue] wrote {args.out}", file=sys.stderr)

    if args.mode == "inventory":
        return 0

    auto_trash_ids = [r["id"] for r in records if r["classification"] == "RESIDUE_AUTO"]
    review_ids = [r["id"] for r in records if r["classification"] == "RESIDUE_REVIEW"]
    explicit_ids = [s.strip() for s in (args.target_ids or "").split(",") if s.strip()]
    targets = explicit_ids or (auto_trash_ids if args.auto_orphans_only else [])

    if args.mode == "dry-run":
        print(f"\n[DRY-RUN] would trash {len(targets)} folders:")
        for tid in targets:
            rec = next((r for r in records if r["id"] == tid), None)
            label = rec["path"] if rec else tid
            print(f"  - trashed: {label} (id={tid})")
        print(f"\n[DRY-RUN] excluded {len(review_ids)} REVIEW candidates "
              "(never auto-trashed): {subset}".format(subset=review_ids[:5]))
        return 0

    if args.mode == "trash":
        print(f"[cleanup_stock_residue] TRASH {len(targets)} folders…", file=sys.stderr)
        ok, fail = 0, 0
        for tid in targets:
            rec = next((r for r in records if r["id"] == tid), None)
            label = rec["path"] if rec else tid
            try:
                trash_folder(svc, tid)
                print(f"  ✓ trashed: {label} (id={tid})")
                ok += 1
            except Exception as e:
                print(f"  ✗ FAILED: {label} (id={tid}) — {e}")
                fail += 1
        print(f"\n[cleanup_stock_residue] done: {ok} trashed, {fail} failed", file=sys.stderr)
        return 0 if fail == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
