#!/usr/bin/env python3
"""align_words_cues.py — word-level alignment → RenderingGen cue JSON.

Produces the word-exact timing document PipelineGen needs to place overlay
cues on the master voiceover: faster-whisper runs with word_timestamps=True
and each recognized word becomes one cue with millisecond start/end plus a
whisper confidence score.

Bridge contract (mirrors transcribe_detect_lang.py):
    .venv-whisper/bin/python3 scripts/tools/align_words_cues.py <audio_or_video> \
        [--language it] [--model <name>] [--min-cue-ms 120] [--max-cue-ms 3000] \
        [--group-n 1] [--json-only]

Output (stdout, single JSON object):
    {
      "language": "it",
      "duration_seconds": 62.3,
      "alignment_time_seconds": 4.1,
      "cues": [
        {"start_ms": 810, "end_ms": 1120, "text": "Ciao",
         "probability": 0.97, "words": [
            {"start_ms": 810, "end_ms": 960, "text": "Ci",
             "probability": 0.98}, ...]}
      ]
    }

Grouping: --group-n > 1 merges N consecutive words into one cue (useful for
kinetic typography at phrase level); the inner "words" array always preserves
the word-exact timestamps so the overlay compiler can animate per word.
"""
import argparse
import json
import os
import subprocess
import sys
import time

# Repo-root import bootstrap (same pattern as scripts/bridges/semantic_tagger).
_HERE = os.path.dirname(os.path.abspath(__file__))
_ROOT = os.path.dirname(os.path.dirname(_HERE))
if _ROOT not in sys.path:
    sys.path.insert(0, _ROOT)

try:
    from scripts.services.model_registry_generated import WHISPER_MODEL_NAME
except Exception:  # pragma: no cover - registry is generator-managed
    WHISPER_MODEL_NAME = "openai/whisper-large-v3-turbo"


def _has_audio_stream(path: str) -> bool:
    probe = subprocess.run(
        ["ffprobe", "-v", "error", "-select_streams", "a:0",
         "-show_entries", "stream=index", "-of", "csv=p=0", path],
        capture_output=True, text=True,
    )
    return bool(probe.stdout.strip())


def _extract_audio(path: str) -> str:
    out = path + ".align.wav"
    subprocess.run(
        ["ffmpeg", "-y", "-v", "error", "-i", path, "-vn",
         "-ac", "1", "-ar", "16000", out],
        check=True, capture_output=True,
    )
    return out


def _get_model(model_size: str):
    try:
        from whisper_runtime import prepare_cuda_runtime
    except ImportError:
        from scripts.tools.whisper_runtime import prepare_cuda_runtime
    prepare_cuda_runtime()
    from faster_whisper import WhisperModel
    return WhisperModel(model_size, device="auto", compute_type="auto")


def align_cues(audio_path: str, model_size: str, language=None,
               min_cue_ms: int = 120, max_cue_ms: int = 3000,
               group_n: int = 1) -> dict:
    """Transcribe with word timestamps and project into RenderingGen cues."""
    if not os.path.exists(audio_path):
        return {"error": f"File not found: {audio_path}"}

    model = _get_model(model_size)
    start = time.time()
    segments, info = model.transcribe(
        audio_path, beam_size=5, language=language, word_timestamps=True,
    )
    segments = list(segments)  # materialize generator
    elapsed = time.time() - start

    cues = []
    for seg in segments:
        words = []
        for w in (seg.words or []):
            token = (w.word or "").strip()
            if not token:
                continue
            words.append({
                "start_ms": int(round(w.start * 1000)),
                "end_ms": int(round(w.end * 1000)),
                "text": token,
                "probability": round(float(w.probability), 4),
            })
        if not words:
            continue

        if group_n <= 1:
            # One cue per word: word-exact overlays (kinetic words).
            cues.extend(words)
            continue

        # Group N consecutive words per cue, clamped to max_cue_ms; a cue is
        # dropped when still shorter than min_cue_ms (invisible on screen).
        for i in range(0, len(words), group_n):
            chunk = words[i:i + group_n]
            start_ms = chunk[0]["start_ms"]
            end_ms = chunk[-1]["end_ms"]
            if end_ms - start_ms > max_cue_ms:
                continue
            if end_ms - start_ms < min_cue_ms:
                continue
            cues.append({
                "start_ms": start_ms,
                "end_ms": end_ms,
                "text": " ".join(w["text"] for w in chunk),
                "probability": round(
                    sum(w["probability"] for w in chunk) / len(chunk), 4),
                "words": chunk,
            })

    return {
        "language": info.language,
        "duration_seconds": round(info.duration, 1),
        "alignment_time_seconds": round(elapsed, 1),
        "cues": cues,
    }


def _log(msg: str, json_only: bool):
    print(msg, file=sys.stderr, flush=True) if json_only else print(msg, flush=True)


def main():
    parser = argparse.ArgumentParser(
        description="Word-level alignment → RenderingGen cue JSON via faster-whisper."
    )
    parser.add_argument("file", help="Path to audio or video file")
    parser.add_argument("--model", default=None,
                        help="Whisper model (default: canonical registry ASR model)")
    parser.add_argument("--language", default=None,
                        help="Force language (e.g. it); omit to auto-detect")
    parser.add_argument("--min-cue-ms", type=int, default=120,
                        help="Drop cues shorter than this (grouped mode)")
    parser.add_argument("--max-cue-ms", type=int, default=3000,
                        help="Drop cues longer than this (grouped mode)")
    parser.add_argument("--group-n", type=int, default=1,
                        help="Words per cue: 1 = word-exact, N = grouped phrases")
    parser.add_argument("--json-only", action="store_true",
                        help="Only the result JSON on stdout; logs on stderr")
    args = parser.parse_args()

    model_size = args.model or WHISPER_MODEL_NAME

    if not os.path.exists(args.file):
        print(json.dumps({"error": f"File not found: {args.file}"}))
        sys.exit(1)

    audio_path = args.file
    cleanup = None
    ext = os.path.splitext(args.file)[1].lower()
    if ext in (".mp4", ".avi", ".mov", ".mkv", ".webm"):
        if not _has_audio_stream(args.file):
            print(json.dumps({"language": "und", "cues": []}))
            sys.exit(0)
        _log("Extracting audio from video...", args.json_only)
        audio_path = _extract_audio(args.file)
        cleanup = audio_path

    try:
        result = align_cues(
            audio_path, model_size, args.language,
            min_cue_ms=args.min_cue_ms, max_cue_ms=args.max_cue_ms,
            group_n=args.group_n,
        )
    finally:
        if cleanup and os.path.exists(cleanup):
            os.unlink(cleanup)

    print(json.dumps(result))
    sys.exit(1 if "error" in result else 0)


if __name__ == "__main__":
    main()
