#!/usr/bin/env python3
"""
scripts/bridges/argos_translator.py — one-shot Argos Translate CLI bridge.

Usage:
    echo "text to translate" | python3 scripts/bridges/argos_translator.py --source en --target it
    # or
    python3 scripts/bridges/argos_translator.py --text "text" --source en --target it

Output (stdout, JSON):
    {
        "translated_text": "...",
        "source": "en",
        "target": "it",
        "model": "argos-en-it",
        "via": "direct"
    }

For per-language batch workloads prefer the persistent sidecar
(argos_server.py) so models are loaded once. This CLI remains for
standalone one-shot use and the Go subprocess adapter's tests.
"""

import argparse
import json
import sys

from argos_bridge.core import translate_text


def main():
    parser = argparse.ArgumentParser(
        description="Offline translation via Argos Translate (OpenNMT)."
    )
    parser.add_argument("--text", default=None, help="Text to translate (or pipe via stdin)")
    parser.add_argument("--source", default=None, help="Source BCP-47 code")
    parser.add_argument("--target", default=None, help="Target BCP-47 code")
    args = parser.parse_args()

    text = args.text
    if text is None:
        text = sys.stdin.read()

    if not args.target:
        print(
            json.dumps({"error": "usage: argos_translator.py --target <code> [--source <code>] (text via stdin or --text)"}),
            file=sys.stderr,
        )
        return 2

    result = translate_text(text, args.source or "", args.target)
    if result.get("error"):
        print(json.dumps(result), file=sys.stderr)
        return 1

    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
