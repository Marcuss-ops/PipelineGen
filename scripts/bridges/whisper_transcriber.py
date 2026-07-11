#!/usr/bin/env python3
"""
scripts/bridges/whisper_transcriber.py — minimal Whisper transcriber
bridge (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5, July 2026).

Usage:
    python3 scripts/bridges/whisper_transcriber.py <local_path>

Output (stdout, JSON):
    {
        "text": "...",
        "detected_language": "en",
        "confidence": 0.92
    }

Errors (stderr):
    {"error": "..."}

This is a MINIMAL stub for Fase 5 wiring. The concrete Whisper
model integration (calling faster-whisper, openai-whisper, or
the Ollama whisper API) is a follow-up (Fase 5.c). The current
implementation:
  1. Checks the file exists and is readable.
  2. Returns a placeholder transcript (the file basename as
     a signal that the bridge was invoked correctly).
  3. Reports the detected language as "und" (BCP-47
     undetermined) so the chain can fall through if the
     operator hasn't configured a real Whisper model.

The Go adapter (internal/infrastructure/youtube/whisper_transcriber.go)
spawns this script via subprocess, parses the JSON output, and
returns the typed asset.TranscriptResult. The chain falls
through gracefully when this script returns an error or an
empty text field.
"""

import json
import os
import sys
from pathlib import Path


def main() -> int:
    if len(sys.argv) < 2:
        print(json.dumps({"error": "usage: whisper_transcriber.py <local_path>"}), file=sys.stderr)
        return 2

    local_path = sys.argv[1]
    if not os.path.isfile(local_path):
        print(json.dumps({"error": f"file not found: {local_path}"}), file=sys.stderr)
        return 3

    # Minimal stub: return the file basename as a placeholder
    # transcript. A real implementation would invoke Whisper
    # here (faster-whisper, openai-whisper, or Ollama's
    # whisper API) and return the typed result.
    basename = Path(local_path).stem
    result = {
        "text": f"[whisper stub: {basename}]",
        "detected_language": "und",
        "confidence": 0.0,
    }
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
