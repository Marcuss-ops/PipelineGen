#!/usr/bin/env python3
"""
Universal audio transcription + language detection via faster-whisper.

Works for ANY audio/video file regardless of source/media type.
Outputs clean JSON — no DB writes, no Go coupling.
Go orchestrator parses the JSON and handles persistence.

Usage:
  # Detect language (fast, tiny model) — returns JSON
  python3 scripts/transcribe_detect_lang.py file.mp3

  # Full transcription (base model) — returns JSON with transcript
  python3 scripts/transcribe_detect_lang.py file.mp3 --model base --transcribe

  # JSON-only output (no human-readable logs) — for Go consumption
  python3 scripts/transcribe_detect_lang.py file.mp4 --json-only
"""
import argparse
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Optional

# ---------------------------------------------------------------------------
# Module-level model cache — whisper model is loaded ONCE per process,
# shared across all transcribe() calls. Avoids reloading for batch mode.
# ---------------------------------------------------------------------------
_MODEL_CACHE: dict = {}


def _prepare_cuda_runtime() -> None:
    """Expose a CUDA 12 runtime when ctranslate2 needs it.

    ctranslate2 4.8 loads CUDA 12 libraries, while the host Python/Torch
    installation may provide CUDA 13.  Ollama ships a compatible CUDA 12
    runtime on this host.  Re-execing after changing LD_LIBRARY_PATH is
    intentional: the dynamic loader reads that variable at process startup.
    """
    if os.environ.get("VELOX_WHISPER_CUDA_RUNTIME_READY") == "1":
        return

    library_path = os.environ.get("LD_LIBRARY_PATH", "")
    search_dirs = [
        os.environ.get("VELOX_WHISPER_CUDA_LIB_DIR", ""),
        "/usr/local/lib/ollama/cuda_v12",
        "/usr/local/cuda-12/lib64",
        "/usr/local/cuda/lib64",
    ]
    for raw_dir in search_dirs:
        if not raw_dir:
            continue
        directory = Path(raw_dir)
        if not (directory / "libcublas.so.12").exists():
            continue
        if str(directory) in library_path.split(os.pathsep):
            os.environ["VELOX_WHISPER_CUDA_RUNTIME_READY"] = "1"
            return

        paths = [str(directory)]
        if library_path:
            paths.append(library_path)
        env = os.environ.copy()
        env["LD_LIBRARY_PATH"] = os.pathsep.join(paths)
        env["VELOX_WHISPER_CUDA_RUNTIME_READY"] = "1"
        os.execve(sys.executable, [sys.executable, *sys.argv], env)


def _resolve_device() -> tuple:
    """Resolve the (device, compute_type) pair for faster-whisper.

    Default "auto": use CUDA (float16) when ctranslate2 reports a
    working GPU, otherwise fall back to CPU int8. Operators can force
    either side with VELOX_WHISPER_DEVICE=cuda|cpu|auto.
    """
    choice = os.environ.get("VELOX_WHISPER_DEVICE", "auto").strip().lower()
    if choice == "cpu":
        return "cpu", "int8"
    if choice in ("auto", "cuda"):
        try:
            _prepare_cuda_runtime()
            from ctranslate2 import get_cuda_device_count

            if get_cuda_device_count() > 0:
                return "cuda", "float16"
        except Exception:
            pass
        if choice == "cuda":
            print(json.dumps({"error": "VELOX_WHISPER_DEVICE=cuda but no usable CUDA device"}), file=sys.stderr)
            sys.exit(2)
    return "cpu", "int8"


def _get_model(model_size: str = "tiny") -> "WhisperModel":
    """Get or create a cached whisper model."""
    global _MODEL_CACHE
    if model_size not in _MODEL_CACHE:
        try:
            from faster_whisper import WhisperModel
        except ImportError:
            raise ImportError(
                "faster-whisper not installed. Run: pip3 install faster-whisper"
            )
        device, compute_type = _resolve_device()
        _MODEL_CACHE[model_size] = WhisperModel(
            model_size, device=device, compute_type=compute_type
        )
    return _MODEL_CACHE[model_size]


def extract_audio(video_path: str) -> str:
    """Extract audio from video as 16kHz mono WAV.
    
    Returns path to temp WAV file (caller must clean up).
    """
    fd, out_path = tempfile.mkstemp(suffix=".wav", prefix="whisper_")
    os.close(fd)
    subprocess.run([
        "ffmpeg", "-y", "-hide_banner", "-loglevel", "warning",
        "-i", video_path,
        "-vn", "-c:a", "pcm_s16le", "-ar", "16000", "-ac", "1",
        out_path
    ], check=True)
    return out_path


def has_audio_stream(media_path: str) -> bool:
    """Return True when the media file exposes at least one audio stream."""
    proc = subprocess.run(
        [
            "ffprobe",
            "-v", "error",
            "-select_streams", "a",
            "-show_entries", "stream=index",
            "-of", "csv=p=0",
            media_path,
        ],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        return False
    return bool(proc.stdout.strip())


def detect_language(audio_path: str, model_size: str = "tiny") -> dict:
    """Quick language detection only (fast, no full transcript).
    
    For Go integration: returns {"language": "en", "probability": 0.99}.
    """
    if not os.path.exists(audio_path):
        return {"error": f"File not found: {audio_path}"}

    model = _get_model(model_size)
    segments, info = model.transcribe(audio_path, beam_size=1)
    # Materialize just the first segment to trigger detection
    for _ in segments:
        break

    return {
        "language": info.language,
        "probability": round(info.language_probability, 4),
        "duration_seconds": round(info.duration, 1),
    }


def transcribe(audio_path: str, model_size: str = "base") -> dict:
    """Full transcription with language detection.
    
    For Go integration: returns JSON with language, transcript, segments.
    """
    if not os.path.exists(audio_path):
        return {"error": f"File not found: {audio_path}"}

    model = _get_model(model_size)
    start = time.time()
    segments, info = model.transcribe(audio_path, beam_size=5)
    segments = list(segments)  # materialize generator
    elapsed = time.time() - start

    transcript = " ".join(seg.text.strip() for seg in segments)
    cues = []
    for seg in segments:
        cues.append({
            "start_ms": int(seg.start * 1000),
            "end_ms": int(seg.end * 1000),
            "text": seg.text.strip()
        })

    return {
        "language": info.language,
        "probability": round(info.language_probability, 4),
        "duration_seconds": round(info.duration, 1),
        "transcription_time_seconds": round(elapsed, 1),
        "num_segments": len(segments),
        "transcript_length": len(transcript),
        "transcript_preview": transcript[:500],
        "transcript_full": transcript,
        "cues": cues,
    }


def _log(msg: str, json_only: bool = False):
    """Print log message to stderr when in --json-only mode (so stdout stays clean JSON)."""
    if not json_only:
        print(msg, flush=True)
    else:
        print(msg, file=sys.stderr, flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Universal language detection + transcription via faster-whisper. "
                    "Works for ANY audio/video file. Returns JSON for Go consumption."
    )
    parser.add_argument("file", help="Path to audio or video file")
    parser.add_argument("--model", default="tiny",
                        help="Whisper model size: tiny (fastest, for detection), "
                             "base/small/medium/large (slower, for transcription)")
    parser.add_argument("--transcribe", action="store_true",
                        help="Full transcription (includes transcript in output). "
                             "Default: language detection only (faster)")
    parser.add_argument("--json-only", action="store_true",
                        help="Output ONLY the result JSON to stdout. "
                             "Log messages go to stderr. Use this for Go integration.")

    args = parser.parse_args()

    file_path = args.file
    if not os.path.exists(file_path):
        result = {"error": f"File not found: {file_path}"}
        print(json.dumps(result))
        sys.exit(1)

    # Auto-extract audio if video file
    audio_path = file_path
    cleanup_path = None
    ext = os.path.splitext(file_path)[1].lower()
    if ext in (".mp4", ".avi", ".mov", ".mkv", ".webm"):
        if not has_audio_stream(file_path):
            result = {
                "language": "und",
                "probability": 0.0,
                "duration_seconds": 0.0,
                "transcription_time_seconds": 0.0,
                "num_segments": 0,
                "transcript_length": 0,
                "transcript_preview": "",
                "transcript_full": "",
            }
            print(json.dumps(result))
            sys.exit(0)
        _log(f"Extracting audio from video...", args.json_only)
        audio_path = extract_audio(file_path)
        cleanup_path = audio_path

    try:
        if args.transcribe:
            _log(f"Transcribing with model '{args.model}'...", args.json_only)
            result = transcribe(audio_path, args.model)
        else:
            _log(f"Detecting language with model '{args.model}'...", args.json_only)
            result = detect_language(audio_path, args.model)
    finally:
        if cleanup_path and os.path.exists(cleanup_path):
            os.unlink(cleanup_path)

    if "error" in result:
        print(json.dumps(result))
        sys.exit(1)

    print(json.dumps(result))
