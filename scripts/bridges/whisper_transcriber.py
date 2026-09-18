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

try:
    from scripts.services.model_registry_generated import WHISPER_MODEL_NAME
except ModuleNotFoundError:  # direct execution from scripts/bridges
    sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
    from scripts.services.model_registry_generated import WHISPER_MODEL_NAME  # type: ignore[no-redef]


def _helper_script_path() -> Path:
    return Path(__file__).resolve().parents[1] / "tools" / "transcribe_detect_lang.py"


def _model_name() -> str:
    return os.environ.get("VELOX_WHISPER_MODEL", WHISPER_MODEL_NAME).strip() or WHISPER_MODEL_NAME


def _argv_language(argv: list) -> str:
    """Return the language requested on the command line, or "".

    The Go adapter passes `--language <tag>` for a per-request forced decode.
    Reading it here is what makes that flag real: it used to be accepted by argv
    and silently ignored, so the caller believed it had forced a language while
    the transcription ran on auto-detect.
    """
    for index, arg in enumerate(argv):
        if arg == "--language" and index + 1 < len(argv):
            return argv[index + 1].strip()
        if arg.startswith("--language="):
            return arg.split("=", 1)[1].strip()
    return ""


def _language(argv_language: str = "") -> str:
    """Language to force: the per-invocation request wins over the deployment
    default (VELOX_WHISPER_LANGUAGE). Empty means auto-detect."""
    if argv_language:
        return argv_language.strip()
    return os.environ.get("VELOX_WHISPER_LANGUAGE", "").strip()


def _run_helper(local_path: str, pcm_stdin: bool = False, argv_language: str = "") -> dict:
    helper = _helper_script_path()
    if not helper.is_file():
        return {"error": f"helper script not found: {helper}"}

    cmd = [
        sys.executable,
        str(helper),
        "--model",
        _model_name(),
        "--transcribe",
        "--json-only",
    ]
    if pcm_stdin:
        # Streaming PCM mode: the caller pipes raw s16le 16kHz mono PCM into
        # this bridge's stdin; the helper feeds it to Whisper as a numpy
        # array. No temp WAV is ever written (feature spec §4).
        cmd.append("--pcm-stdin")
    else:
        cmd.append(local_path)
    language = _language(argv_language)
    if language:
        cmd.extend(["--language", language])
    pcm = sys.stdin.buffer.read() if pcm_stdin else None
    proc = subprocess.run(cmd, capture_output=True, input=pcm)
    proc.stdout = proc.stdout.decode("utf-8", errors="replace")
    proc.stderr = proc.stderr.decode("utf-8", errors="replace")
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
    # Two DIFFERENT facts, never conflated:
    #   language          — the language the text was decoded as (forced or detected)
    #   detected_language — what the model heard with no language forced
    # When a language is forced the model echoes it back, so reporting the echo as
    # the detection would turn the caller's language check into a tautology.
    used_language = payload.get("language", "und")
    return {
        "text": transcript,
        "language": used_language,
        "detected_language": payload.get("detected_language") or used_language,
        "language_forced": bool(payload.get("language_forced", bool(language))),
        "confidence": payload.get("probability", 0.0),
        "language_probability": payload.get("language_probability", payload.get("probability", 0.0)),
        "duration_ms": int((payload.get("duration_seconds") or 0.0) * 1000),
        "cues": payload.get("cues") or []
    }


def main() -> int:
    argv = sys.argv[1:]
    argv_language = _argv_language(argv)
    pcm_stdin = "--pcm-stdin" in argv
    if pcm_stdin:
        # Streaming PCM mode: no local path required — raw s16le 16kHz mono
        # PCM is read from stdin (piped by the Go side from FFmpeg's decode).
        result = _run_helper("", pcm_stdin=True, argv_language=argv_language)
        if result.get("error"):
            print(json.dumps(result), file=sys.stderr)
            return 1
        print(json.dumps(result))
        return 0

    if len(sys.argv) < 2:
        print(json.dumps({"error": "usage: whisper_transcriber.py <local_path> | --pcm-stdin"}), file=sys.stderr)
        return 2

    # The local path is the first non-flag argument; a flag's VALUE must not be
    # mistaken for it ("--language de clip.mp4" names clip.mp4).
    local_path = ""
    for index, arg in enumerate(argv):
        if arg.startswith("-"):
            continue
        if index > 0 and argv[index - 1] == "--language":
            continue
        local_path = arg
        break
    if not local_path:
        print(json.dumps({"error": "usage: whisper_transcriber.py <local_path> | --pcm-stdin"}), file=sys.stderr)
        return 2
    if not os.path.isfile(local_path):
        print(json.dumps({"error": f"file not found: {local_path}"}), file=sys.stderr)
        return 3

    result = _run_helper(local_path, argv_language=argv_language)
    if result.get("error"):
        print(json.dumps(result), file=sys.stderr)
        return 1

    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
