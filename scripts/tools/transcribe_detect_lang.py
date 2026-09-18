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

try:
    from whisper_runtime import prepare_cuda_runtime
except ImportError:
    from scripts.tools.whisper_runtime import prepare_cuda_runtime

# Canonical ASR model identity comes from the registry mirror
# (scripts/services/model_registry_generated.py, generated from
# internal/kernel/models by cmd/model-registry-gen). Do NOT hardcode
# the Whisper model id here. VELOX_WHISPER_MODEL remains the explicit
# operator override (validated by whisper_preflight.py); the registry
# value is the default for transcription. Size aliases (tiny/base/...)
# are accepted ONLY for fast language detection and explicit operator
# overrides.
try:
    from scripts.services.model_registry_generated import WHISPER_MODEL_NAME
except ModuleNotFoundError:  # direct execution from scripts/tools
    sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
    from scripts.services.model_registry_generated import (  # type: ignore[no-redef]
        WHISPER_MODEL_NAME,
    )

# ---------------------------------------------------------------------------
# Module-level model cache — whisper model is loaded ONCE per process,
# shared across all transcribe() calls. Avoids reloading for batch mode.
# ---------------------------------------------------------------------------
_MODEL_CACHE: dict = {}

# faster-whisper loads CTranslate2 builds, while the canonical ASR identity in
# internal/kernel/models is the upstream OpenAI repo (transformers weights).
# Handing that identity straight to WhisperModel fails with
# "Unable to open file 'model.bin'", so it is mapped to the published
# CTranslate2 conversion of the SAME model. Any other value (a faster-whisper
# size alias or an explicit local path) passes through untouched, so the
# documented VELOX_WHISPER_MODEL override keeps working.
_CT2_MODEL_ALIASES = {
    "openai/whisper-large-v3-turbo": "large-v3-turbo",
    "openai/whisper-large-v3": "large-v3",
    "openai/whisper-large-v2": "large-v2",
    "openai/whisper-medium": "medium",
    "openai/whisper-small": "small",
    "openai/whisper-base": "base",
    "openai/whisper-tiny": "tiny",
}


def _resolve_ct2_model(model_size: str) -> str:
    """Map a canonical transformers model id to its CTranslate2 build."""
    return _CT2_MODEL_ALIASES.get(model_size, model_size)


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
            prepare_cuda_runtime()
            from ctranslate2 import get_cuda_device_count

            if get_cuda_device_count() > 0:
                return "cuda", "float16"
        except Exception:
            pass
        if choice == "cuda":
            print(json.dumps({"error": "VELOX_WHISPER_DEVICE=cuda but no usable CUDA device"}), file=sys.stderr)
            sys.exit(2)
    return "cpu", "int8"


# Voice-activity filtering. ON by default (VELOX_WHISPER_VAD_FILTER=0 disables):
# without it the canonical large-v3-turbo model keeps generating segments over
# trailing silence and noise — a 60 s clip produced cue text like "Stay in a
# way. / Stay in a way. / Good bye." extending past 77-87 s, i.e. text that is
# not in the media at all. The clamp below still bounds the windows (the
# validator rejects anything past the clip), but VAD removes the invented text
# instead of merely trimming it. Measured on the 60 s reference clip: 18 real
# cues ending at 59.53 s, and FASTER than the unfiltered run because the
# silence is never decoded.
_VAD_DEFAULT = True


def _vad_enabled() -> bool:
    raw = os.environ.get("VELOX_WHISPER_VAD_FILTER", "").strip().lower()
    if raw == "":
        return _VAD_DEFAULT
    return raw not in ("0", "false", "no", "off")


def _get_model(model_size: str = "tiny") -> "WhisperModel":
    """Get or create a cached whisper model."""
    global _MODEL_CACHE
    model_size = _resolve_ct2_model(model_size)
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


def detect_language(audio_path: str, model_size: str = "tiny", language: Optional[str] = None) -> dict:
    """Quick language detection only (fast, no full transcript).
    
    For Go integration: returns {"language": "en", "probability": 0.99}.
    """
    if not os.path.exists(audio_path):
        return {"error": f"File not found: {audio_path}"}

    model = _get_model(model_size)
    segments, info = model.transcribe(audio_path, beam_size=1, language=language)
    # Materialize just the first segment to trigger detection
    for _ in segments:
        break

    return {
        "language": info.language,
        "probability": round(info.language_probability, 4),
        "duration_seconds": round(info.duration, 1),
    }


# Cue windows are clamped to the MEDIA duration. faster-whisper models (the
# canonical large-v3-turbo included) keep emitting segments past the end of the
# audio on trailing silence/noise — a 60 s clip produced cues ending at 77-87 s
# of hallucinated text. That is not a cosmetic problem: the canonical ASS
# validator (texttracks.validateASSFile) rejects "last cue end exceeds clip
# duration", so EVERY subtitle artifact for a Whisper-sourced clip would be
# persisted as FAILED. Clamping here — the single projection point shared by
# transcribe() and transcribe_pcm_stream() — keeps the source timing inside the
# media, which is the only honest owner of "this segment exists".

def _clamp_cues(segments, duration_seconds: float) -> tuple[list, int, int]:
    """Project whisper segments into in-range cues.

    Returns (cues, dropped, trimmed):
      dropped — segments starting at/after the media end (hallucinated tail);
      trimmed — segments whose end was clamped to the media end.
    """
    duration_ms = int(round(duration_seconds * 1000)) if duration_seconds else 0
    cues = []
    dropped = 0
    trimmed = 0
    for seg in segments:
        text = seg.text.strip()
        if not text:
            # An empty cue would serialize as an empty ASS Dialogue line,
            # which the canonical validator rejects as invalid.
            continue
        start_ms = int(seg.start * 1000)
        end_ms = int(seg.end * 1000)
        if duration_ms > 0 and start_ms >= duration_ms:
            dropped += 1
            continue
        if duration_ms > 0 and end_ms > duration_ms:
            end_ms = duration_ms
            trimmed += 1
        if end_ms <= start_ms:
            continue
        cues.append({
            "start_ms": start_ms,
            "end_ms": end_ms,
            "text": text,
        })
    return cues, dropped, trimmed


# A forced decode cannot prove what language the audio is in: faster-whisper
# ECHOES the language it was told to use, so `info.language` is the request, not
# a detection. The two facts are emitted separately ("language" = the language
# actually used to decode, "detected_language" = what the model hears with NO
# language forced) because the Go side records the second one as provenance and
# may not substitute the first one for it.
_DETECTION_MAX_SECONDS = 30


def _detect_language_unforced(model, audio, max_seconds: float = _DETECTION_MAX_SECONDS):
    """Return (language, probability) detected with NO language forced.

    Runs a separate beam_size=1 detection over the head of the media — the same
    bounded window Whisper's own language detection uses — so the cost stays a
    fraction of the full transcript it accompanies. Any failure degrades to
    ("und", 0.0): a missing detection must never fail a transcription that
    otherwise succeeded, and "und" is the honest value for "we cannot tell".
    """
    try:
        probe = audio
        if not isinstance(audio, str):
            probe = audio[: int(max_seconds * 16000)]
        _segments, info = model.transcribe(probe, beam_size=1, language=None, vad_filter=False)
        probability = getattr(info, "language_probability", 0.0) or 0.0
        return (info.language or "und"), float(probability)
    except Exception:
        return "und", 0.0


def _segments_to_result(
    segments,
    info,
    elapsed: float,
    detected_language: Optional[str] = None,
    detected_probability: Optional[float] = None,
    language_forced: bool = False,
) -> dict:
    """Project materialized whisper segments + info into the canonical JSON
    result shape shared by transcribe() and transcribe_pcm_stream().

    detected_language/detected_probability carry the UNFORCED detection when the
    caller forced a language (see _detect_language_unforced); when nothing was
    forced they default to the decode language, which IS the detection.
    """
    cues, dropped, trimmed = _clamp_cues(segments, getattr(info, "duration", 0.0))
    # The transcript is the text of the cues that were emitted, so the text and
    # the timing always describe the same content (a transcript holding text the
    # cue list dropped would be text no artifact can ever render).
    transcript = " ".join(cue["text"] for cue in cues)
    probability = info.language_probability if detected_probability is None else detected_probability
    return {
        "language": info.language,
        "language_forced": bool(language_forced),
        "detected_language": detected_language or info.language,
        "language_probability": round(probability, 4),
        "probability": round(info.language_probability, 4),
        "duration_seconds": round(info.duration, 1),
        "transcription_time_seconds": round(elapsed, 1),
        "num_segments": len(cues),
        "vad_filter": _vad_enabled(),
        "cues_dropped_beyond_duration": dropped,
        "cues_trimmed_to_duration": trimmed,
        "transcript_length": len(transcript),
        "transcript_preview": transcript[:500],
        "transcript_full": transcript,
        "cues": cues,
    }


def transcribe(audio_path: str, model_size: str = WHISPER_MODEL_NAME, language: Optional[str] = None) -> dict:
    """Full transcription with language detection.

    Defaults to the canonical registry ASR model (WHISPER_MODEL_NAME);
    explicit size aliases remain available for operator overrides.

    For Go integration: returns JSON with language, transcript, segments.
    """
    if not os.path.exists(audio_path):
        return {"error": f"File not found: {audio_path}"}

    model = _get_model(model_size)
    language_forced = bool(language)
    detected_language, detected_probability = (None, None)
    if language_forced:
        detected_language, detected_probability = _detect_language_unforced(model, audio_path)
    start = time.time()
    segments, info = model.transcribe(
        audio_path, beam_size=5, language=language, vad_filter=_vad_enabled()
    )
    segments = list(segments)  # materialize generator
    elapsed = time.time() - start
    return _segments_to_result(
        segments, info, elapsed, detected_language, detected_probability, language_forced
    )


def transcribe_pcm_stream(pcm_bytes: bytes, model_size: str = WHISPER_MODEL_NAME, language: Optional[str] = None) -> dict:
    """Transcribe raw PCM fed through a pipe — zero WAV on disk.

    The Go side decodes the source audio with FFmpeg straight to a
    s16le 16kHz mono pipe and streams the bytes into this helper's stdin.
    faster-whisper accepts a numpy float32 array, so the raw PCM is
    converted in memory (never written to a temp WAV).
    """
    if not pcm_bytes:
        return {"error": "empty PCM stream"}
    try:
        import numpy as np
    except ImportError:
        return {"error": "numpy not installed; required for PCM streaming"}
    audio = np.frombuffer(pcm_bytes, dtype=np.int16).astype(np.float32) / 32768.0
    if audio.size == 0:
        return {"error": "empty PCM stream after conversion"}

    model = _get_model(model_size)
    language_forced = bool(language)
    detected_language, detected_probability = (None, None)
    if language_forced:
        detected_language, detected_probability = _detect_language_unforced(model, audio)
    start = time.time()
    segments, info = model.transcribe(
        audio, beam_size=5, language=language, vad_filter=_vad_enabled()
    )
    segments = list(segments)  # materialize generator
    elapsed = time.time() - start
    return _segments_to_result(
        segments, info, elapsed, detected_language, detected_probability, language_forced
    )


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
    parser.add_argument("file", nargs="?", help="Path to audio or video file (not required with --pcm-stdin)")
    parser.add_argument("--model", default=None,
                        help="Whisper model (repo id or faster-whisper size alias). "
                             "Default: canonical registry model "
                             "(WHISPER_MODEL_NAME) for transcription, "
                             "'tiny' (fastest) for language detection")
    parser.add_argument("--transcribe", action="store_true",
                        help="Full transcription (includes transcript in output). "
                             "Default: language detection only (faster)")
    parser.add_argument("--json-only", action="store_true",
                        help="Output ONLY the result JSON to stdout. "
                             "Log messages go to stderr. Use this for Go integration.")
    parser.add_argument("--language", default=None,
                        help="Force Whisper language, e.g. en; omit to auto-detect")
    parser.add_argument("--pcm-stdin", action="store_true",
                        help="Read raw s16le 16kHz mono PCM from stdin instead of a file "
                             "(zero temp WAV — the caller pipes FFmpeg's PCM decode)")

    args = parser.parse_args()

    # Centralized model selection: transcription uses the canonical
    # registry ASR model by default; language detection stays on the
    # fast 'tiny' size alias (deliberate, documented in the registry
    # comment). An explicit --model overrides both.
    model_size = args.model or (WHISPER_MODEL_NAME if args.transcribe else "tiny")

    if args.pcm_stdin:
        # prepare_cuda_runtime may re-exec this script (os.execve) to put
        # the CUDA lib dirs on LD_LIBRARY_PATH. The re-exec'd process
        # inherits the SAME stdin pipe — so the (possible) re-exec MUST
        # happen BEFORE draining stdin, or the second process would read
        # an already-consumed pipe (empty PCM).
        prepare_cuda_runtime()
        pcm = sys.stdin.buffer.read()
        _log("Transcribing streaming PCM with model '%s'..." % model_size, args.json_only)
        result = transcribe_pcm_stream(pcm, model_size, args.language)
        if "error" in result:
            print(json.dumps(result))
            sys.exit(1)
        print(json.dumps(result))
        sys.exit(0)

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
            _log(f"Transcribing with model '{model_size}'...", args.json_only)
            result = transcribe(audio_path, model_size, args.language)
        else:
            _log(f"Detecting language with model '{model_size}'...", args.json_only)
            result = detect_language(audio_path, model_size, args.language)
    finally:
        if cleanup_path and os.path.exists(cleanup_path):
            os.unlink(cleanup_path)

    if "error" in result:
        print(json.dumps(result))
        sys.exit(1)

    print(json.dumps(result))
