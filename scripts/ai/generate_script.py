#!/usr/bin/env python3
"""
Thin Python wrapper for the Go script pipeline.

Python only prepares input text and calls the Go backend.
All generation, clip matching, and document building live in Go.

Examples:
  python3 scripts/generate_script.py --topic "Gervonta Davis" --text-file notes.txt
  python3 scripts/generate_script.py --topic "Floyd Mayweather" --youtube-url "https://youtube.com/watch?v=..."
  python3 scripts/generate_script.py --topic "Boxing history" --text "Raw notes..."
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import requests
from pathlib import Path
from typing import Any


def _bootstrap_imports() -> None:
    repo_root = Path(__file__).resolve().parents[1]
    src_dir = repo_root / "src"
    if str(src_dir) not in sys.path:
        sys.path.insert(0, str(src_dir))


_bootstrap_imports()

from python.youtube_transcript import extract_vtt_from_youtube, parse_vtt_to_text  # noqa: E402


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Thin wrapper around the Go script pipeline")
    parser.add_argument("--topic", required=True, help="Topic or title of the script")
    parser.add_argument("--language", default="en", help="Target language, e.g. en, it, es")
    parser.add_argument("--duration", type=int, default=120, help="Target duration in seconds")
    parser.add_argument("--title", help="Optional title override")
    parser.add_argument("--text", help="Source text passed directly on the command line")
    parser.add_argument("--text-file", help="Path to a local text file")
    parser.add_argument("--youtube-url", help="Generate from YouTube transcript")
    parser.add_argument("--api-url", default=os.environ.get("VELOX_API_URL", "http://127.0.0.1:8080"), help="Base URL of the local Go API")
    parser.add_argument("--output", help="Optional JSON output file for the backend response")
    return parser


def read_source_text(args: argparse.Namespace) -> str:
    if args.text_file:
        with open(args.text_file, "r", encoding="utf-8") as handle:
            return handle.read()
    if args.text is not None:
        return args.text
    return ""


def health_check(api_url: str) -> bool:
    try:
        response = requests.get(f"{api_url.rstrip('/')}/health", timeout=10)
        return response.status_code == 200
    except requests.RequestException:
        return False


def post_json(api_url: str, path: str, payload: dict[str, Any], timeout: int = 240) -> dict[str, Any]:
    url = f"{api_url.rstrip('/')}{path}"
    response = requests.post(url, json=payload, timeout=timeout)
    response.raise_for_status()
    return response.json()


def build_source_text(args: argparse.Namespace) -> tuple[str, str]:
    if args.youtube_url:
        language_code = (args.language or "en").split("-")[0].lower()
        vtt = extract_vtt_from_youtube(args.youtube_url, language_code)
        if not vtt and language_code != "en":
            vtt = extract_vtt_from_youtube(args.youtube_url, "en")
        if vtt:
            return parse_vtt_to_text(vtt), "youtube"
        return args.youtube_url, "youtube"

    source_text = read_source_text(args)
    if not source_text.strip():
        raise ValueError("source text is empty")
    return source_text, "text"


def main() -> int:
    args = build_parser().parse_args()

    # Source is now optional to allow the backend to generate from topic if none provided
    source_count = sum(1 for value in (args.text, args.text_file, args.youtube_url) if value)
    if source_count > 1:
        print("Error: provide AT MOST one source: --text, --text-file, or --youtube-url", file=sys.stderr)
        return 2

    api_url = args.api_url
    if not health_check(api_url):
        print(f"Error: API server not reachable at {api_url}", file=sys.stderr)
        return 1

    source_text = ""
    source_label = "none (backend generation)"
    if source_count == 1:
        try:
            source_text, source_label = build_source_text(args)
        except ValueError as exc:
            print(f"Error: {exc}", file=sys.stderr)
            return 2

    payload = {
        "topic": args.topic,
        "duration": args.duration,
        "language": args.language,
        "template": "documentary",
        "preview_only": True,
        "source_text": source_text,
    }
    if args.title:
        payload["title"] = args.title

    response = post_json(api_url, "/api/script-docs/preview", payload, timeout=300)
    if not response.get("ok"):
        print(f"Error: {json.dumps(response, ensure_ascii=False)}", file=sys.stderr)
        return 1

    doc_url = response.get("doc_url", "")
    print(f"Doc URL: {doc_url}")
    print(f"Topic: {args.topic}")
    print(f"Source: {source_label}")

    if args.output:
        output_path = Path(args.output).expanduser().resolve()
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(json.dumps(response, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(f"Saved response to {output_path}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
