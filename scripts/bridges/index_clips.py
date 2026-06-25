#!/usr/bin/env python3
"""
scripts/bridges/index_clips.py — pure-HTTP bridge to the embedding server.

QDRANT-001 (June 2026) closure: this script used to import sqlite3, load
the multilingual-e5 model, and write directly to media_assets.embedding_json.
Per QDRANT-001 (Go is sole writer of SQLite), this script now delegates
ALL persistence to the canonical Go caller (clipindexer.indexViaScript).

This script NO LONGER:
  - imports sqlite3
  - loads SentenceTransformer / CLIP / CLAP models
  - writes to media.db.sqlite
  - touches any embedding model weight

This script ALSO retains its argument surface (`--db`, `--clip-id`,
`--clip-name`, `--clip-path`) for backward compatibility with the
existing go orchestrator (clipindexer.Service.indexViaScript) and
cmd/admin/verify.go which probes for its existence. The actual
embedding compute + persistence happens in:

  POST  ${EMBEDDING_SERVER_URL:-http://127.0.0.1:8001}/index
        body: {"clip_id": ..., "name": ..., "search_text": ...}

The Go side reads the request, calls this thin shim for compatibility,
makes its own HTTP call to the embedding server, and persists.

This file is kept (not deleted) because cmd/admin/verify.go and
clipindexer.indexViaScript both reference its default path. The QDRANT-001
violation is the IMPLEMENTATION, not the file presence — and the new
implementation is now a pure bridge with zero state.
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Optional


def env_default(name: str, fallback: str) -> str:
    return (os.environ.get(name) or fallback).strip()


def find_transcript(local_path: str, name: str) -> str:
    """Return the path of the .txt sidecar if one exists; else empty."""
    candidates: list[Path] = []
    if local_path:
        candidates.append(Path(local_path).with_suffix(".txt"))
    if name:
        base = Path(name).stem
        for s_dir in ("data/media", "data/downloads", "data/youtube-clips"):
            candidates.append(Path(s_dir) / f"{base}.txt")
        if local_path:
            candidates.insert(0, Path(local_path).parent / f"{base}.txt")
    for c in candidates:
        if c.exists() and c.is_file():
            return str(c)
    return ""


def http_post_json(url: str, payload: dict, timeout: float = 30.0) -> dict:
    """POST a JSON payload to `url`; return parsed JSON body or raise."""
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read().decode("utf-8") or "{}"
        return json.loads(raw)


def read_search_text(local_path: str, name: str) -> str:
    """Read any pre-existing search text from a sidecar .txt file."""
    transcript_path = find_transcript(local_path, name)
    if not transcript_path:
        return ""
    return Path(transcript_path).read_text(encoding="utf-8", errors="ignore").strip()


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Pure-HTTP bridge to the QDRANT-001 embedding server "
        "(QDRANT-001: no local DB writes, no model load).",
    )
    parser.add_argument("--db", nargs="+", default=[],
                        help="Ignored. Kept for back-compat with the legacy "
                        "script chooser (clipindexer.indexViaScript).")
    parser.add_argument("--clip-id", default="")
    parser.add_argument("--clip-name", default="")
    parser.add_argument("--clip-path", default="")
    args = parser.parse_args()

    base_url = env_default("EMBEDDING_SERVER_URL", "http://127.0.0.1:8001")
    if not args.clip_id:
        # QDRANT-001: only the clip_id is required; the embedding server
        # can compute the embedding from the optional name + search_text
        # we hand it inline.
        sys.stderr.write(
            "warning: --clip-id not provided; "
            "QDRANT-001 closure: sidecar is compute-only. Use the "
            "Go canonical flow (clipindexer.indexViaAPI) for production "
            "ingestion.\n"
        )
        return 0

    search_text = read_search_text(args.clip_path, args.clip_name)
    transcript_path = find_transcript(args.clip_path, args.clip_name)

    try:
        out = http_post_json(
            f"{base_url.rstrip('/')}/index",
            {
                "clip_id": args.clip_id,
                "name": args.clip_name,
                "search_text": search_text,
            },
        )
        sys.stdout.write(json.dumps(out, indent=2, ensure_ascii=False) + "\n")
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError) as e:
        sys.stderr.write(
            f"embedding server unreachable at {base_url}/index: "
            f"{type(e).__name__}: {e}\n"
        )
        return 0  # never fatal — Go caller is canonical owner.

    if transcript_path:
        try:
            http_post_json(
                f"{base_url.rstrip('/')}/index_transcript",
                {
                    "clip_id": args.clip_id,
                    "transcript_path": transcript_path,
                },
            )
        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError):
            pass  # best-effort

    return 0


if __name__ == "__main__":
    sys.exit(main())
