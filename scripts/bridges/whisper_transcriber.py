#!/usr/bin/env python3
"""
scripts/bridges/whisper_transcriber.py — Whisper transcriber bridge.

Usage:
    python3 scripts/bridges/whisper_transcriber.py <local_path>

Output (stdout, JSON):
    {
        "text": "...",
        "detected_language": "en",
        "confidence": 0.92
    }

This bridge delegates to the repository's faster-whisper helper
(`scripts/tools/transcribe_detect_lang.py`) so the Go adapter can
consume real transcripts instead of a placeholder stub. The helper
already handles audio extraction for video inputs, language detection,
and CPU-friendly faster-whisper transcription.
"""

import json
import os
import subprocess
import sys
from pathlib import Path


def _helper_script_path() -> Path:
    return Path(__file__).resolve().parents[1] / "tools" / "transcribe_detect_lang.py"


def _model_name() -> str:
    return os.environ.get("VELOX_WHISPER_MODEL", "base").strip() or "base"


def _run_helper(local_path: str) -> dict:
    helper = _helper_script_path()
    if not helper.is_file():
        return {"error": f"helper script not found: {helper}"}

    cmd = [
        sys.executable,
        str(helper),
        local_path,
        "--model",
        _model_name(),
        "--transcribe",
        "--json-only",
    ]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        stderr = (proc.stderr or "").strip()
        stdout = (proc.stdout or "").strip()
        if stdout:
            try:
                payload = json.loads(stdout)
            except json.JSONDecodeError:
                payload = None
            if isinstance(payload, dict) and payload.get("error"):
                return {"error": str(payload["error"])}
        if stderr:
            return {"error": stderr}
        return {"error": f"whisper helper exited with status {proc.returncode}"}

    try:
        payload = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        return {"error": f"invalid JSON from whisper helper: {exc}"}

    if not isinstance(payload, dict):
        return {"error": "invalid whisper helper payload"}
    if payload.get("error"):
        return {"error": str(payload["error"])}

    transcript = payload.get("transcript_full") or payload.get("text") or ""
    return {
        "text": transcript,
        "detected_language": payload.get("language", "und"),
        "confidence": payload.get("probability", 0.0),
        "duration_ms": int((payload.get("duration_seconds") or 0.0) * 1000),
        "cues": payload.get("cues") or []
    }


def main() -> int:
    if len(sys.argv) < 2:
        print(json.dumps({"error": "usage: whisper_transcriber.py <local_path>"}), file=sys.stderr)
        return 2

    local_path = sys.argv[1]
    if not os.path.isfile(local_path):
        print(json.dumps({"error": f"file not found: {local_path}"}), file=sys.stderr)
        return 3

    result = _run_helper(local_path)
    if result.get("error"):
        print(json.dumps(result), file=sys.stderr)
        return 1

    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
