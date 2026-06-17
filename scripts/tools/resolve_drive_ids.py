#!/usr/bin/env python3
"""
resolve_drive_ids.py — One-shot resolver for one or more Google Drive IDs.

Reuses the OAuth2 token wired into google-accounting/drive_client.py so the
9 IDs the operator gave can be resolved to {id, name, mime_type, parent, trashed}
without going through the Go server.

Usage:
    python3 scripts/tools/resolve_drive_ids.py <id1> [id2 ...]
    python3 scripts/tools/resolve_drive_ids.py --file ids.txt
    # ids.txt: one Drive URL or raw ID per line
"""

import argparse
import concurrent.futures as cf
import json
import re
import sys
import time
from pathlib import Path
from typing import Optional

# Reuse the existing OAuth2 plumbing so we don't duplicate the auth flow.
ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(ROOT / "google-accounting"))

from drive_client import _build_service  # noqa: E402

# Bound concurrent Files.Get calls under Drive's 10 req/s/user rate limit.
# Reduce to 3 (down from 8) and add retry-with-backoff to absorb transient
# SSL / Timeout errors that come from over-aggressive parallel bursts.
MAX_WORKERS = 3
MAX_RETRIES = 3
RETRY_BASE_DELAY_S = 1.5  # 1.5s, 3s, 4.5s exit delays

URL_FOLDER_RE = re.compile(r"/folders/([a-zA-Z0-9_-]+)")

# Error classes worth retrying on (network/transient). Auth errors (HttpError 401/403)
# are NOT retried — those mean the token is wrong or you don't have access.
RETRYABLE_EXC_NAMES = {"SSLError", "TimeoutError", "ConnectionError",
                       "ConnectionResetError", "ServiceUnavailableError",
                       "InternalServerError", "TooManyRequestsError"}


def extract_id(input_str: str) -> Optional[str]:
    """Accept a raw Drive ID OR a Drive URL, return ID (or None)."""
    s = (input_str or "").strip()
    if not s:
        return None
    if s.startswith("http://") or s.startswith("https://"):
        m = URL_FOLDER_RE.search(s)
        return m.group(1) if m else None
    return s


def resolve_one(svc, file_id: str) -> dict:
    """Drive Files.Get for a single ID with bounded retries. Returns dict."""
    last_err: Optional[str] = None
    for attempt in range(1, MAX_RETRIES + 1):
        try:
            f = (
                svc.files()
                .get(
                    fileId=file_id,
                    fields="id,name,mimeType,parents,trashed,webViewLink,size,modifiedTime",
                )
                .execute(num_retries=0)  # we handle retries ourselves
            )
            return {
                "input_id": file_id,
                "id": f.get("id", ""),
                "name": f.get("name", ""),
                "mime_type": f.get("mimeType", ""),
                "parents": f.get("parents", []),
                "trashed": bool(f.get("trashed", False)),
                "web_view_link": f.get("webViewLink", ""),
                "size": f.get("size", ""),
                "modified_time": f.get("modifiedTime", ""),
                "error": None,
                "attempts": attempt,
            }
        except Exception as e:
            last_err = f"{type(e).__name__}: {e}"
            if type(e).__name__ in RETRYABLE_EXC_NAMES and attempt < MAX_RETRIES:
                delay = RETRY_BASE_DELAY_S * attempt
                time.sleep(delay)
                continue
            break
    return {
        "input_id": file_id,
        "id": "",
        "name": "",
        "mime_type": "",
        "parents": [],
        "trashed": False,
        "web_view_link": "",
        "error": last_err,
        "attempts": MAX_RETRIES,
    }


def resolve_batch(ids: list[str]) -> list[dict]:
    """Fan out the Files.Get calls under MAX_WORKERS concurrency."""
    svc = _build_service()
    out: list[dict] = [None] * len(ids)  # type: ignore[list-item]
    with cf.ThreadPoolExecutor(max_workers=MAX_WORKERS) as pool:
        future_to_idx = {pool.submit(resolve_one, svc, fid): i for i, fid in enumerate(ids)}
        for fut in cf.as_completed(future_to_idx):
            i = future_to_idx[fut]
            out[i] = fut.result()
    return out  # type: ignore[return-value]


def read_ids_from_file(path: Path) -> list[str]:
    """One Drive URL or raw ID per line. Comments (#) and blanks ignored."""
    out: list[str] = []
    for line in path.read_text().splitlines():
        s = line.strip()
        if not s or s.startswith("#"):
            continue
        out.append(s)
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description="Resolve Drive IDs/URLs to metadata.")
    ap.add_argument("ids", nargs="*", help="Drive folder/file IDs or full URLs.")
    ap.add_argument("--file", type=Path, help="Path to a file with one ID/URL per line.")
    ap.add_argument("--out", type=Path, help="Optional JSON output path (defaults to stdout).")
    args = ap.parse_args()

    raw_inputs: list[str] = list(args.ids)
    if args.file:
        raw_inputs.extend(read_ids_from_file(args.file))
    if not raw_inputs:
        ap.error("Provide at least one Drive ID/URL on the cmdline or via --file.")
        return 2

    # Extract & dedupe while keeping the original index for reporting.
    extracted: list[tuple[int, str]] = []  # (orig_idx, id)
    seen: set[str] = set()
    for i, raw in enumerate(raw_inputs):
        fid = extract_id(raw)
        if not fid:
            print(f"[{i}] SKIP invalid input: {raw!r}", file=sys.stderr)
            continue
        if fid in seen:
            continue
        seen.add(fid)
        extracted.append((i, fid))

    if not extracted:
        print("No valid IDs to resolve.", file=sys.stderr)
        return 1

    t0 = time.time()
    results = resolve_batch([fid for _, fid in extracted])
    dt = time.time() - t0

    # Rewrite to a flat list of {input_idx, ...} so caller can correlate.
    payload = []
    for (orig_idx, fid), r in zip(extracted, results):
        r = dict(r)
        r["input_idx"] = orig_idx
        r["input_raw"] = raw_inputs[orig_idx]
        payload.append(r)

    payload.sort(key=lambda x: x["input_idx"])
    output = {"resolved_count": len([x for x in payload if not x["error"]]),
              "error_count":    len([x for x in payload if     x["error"]]),
              "elapsed_sec":    round(dt, 2),
              "items":          payload}

    text = json.dumps(output, indent=2, ensure_ascii=False)
    if args.out:
        args.out.write_text(text)
        print(f"[ok] wrote {len(payload)} entries to {args.out}", file=sys.stderr)
    else:
        print(text)

    # Exit nonzero only if EVERY entry errored (catastrophic auth failure
    # usually). Partial failures are normal and visible in the JSON output.
    failed = sum(1 for x in payload if x["error"])
    return 0 if failed < len(payload) else 3


if __name__ == "__main__":
    sys.exit(main())
