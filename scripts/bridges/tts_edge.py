import asyncio
import argparse
import json
import os
import sys

from edge_tts import Communicate

from edge_tts_bridge.boundaries import boundary_line, remove_partials
from edge_tts_bridge.request import edge_boundary_mode, normalize_language
from edge_tts_bridge.voice_resolver import VoiceResolutionError, resolve_voice


async def main():
    p = argparse.ArgumentParser(description="Edge TTS")
    p.add_argument("--text", default="")
    p.add_argument("--lang", default="it")
    p.add_argument("--out", required=True)
    p.add_argument("--voice")
    p.add_argument("--allow-voice-fallback", action="store_true",
                   help="Allow automatic voice selection when --voice is omitted (debug/smoke tests only)")
    p.add_argument("--boundary", default="word", choices=("word", "sentence"))
    a = p.parse_args()

    text = a.text
    if not text:
        text = sys.stdin.read()
    if not text:
        print(json.dumps({"ok": False, "error": "No text provided (--text empty and stdin empty)"}))
        sys.exit(1)

    lang = normalize_language(a.lang)
    if a.voice:
        voice = a.voice
    elif a.allow_voice_fallback:
        voice = await resolve_voice(lang, allow_fallback=True)
    else:
        print(json.dumps({
            "ok": False,
            "error": (
                f"No voice provided for language {lang!r}. "
                f"Pass --voice or --allow-voice-fallback (debug/smoke tests only)."
            )
        }))
        sys.exit(1)

    out_path = os.path.abspath(a.out)
    audio_part = out_path + ".part"
    meta_path = out_path + ".metadata.jsonl"
    meta_part = meta_path + ".part"

    boundary_count = 0
    try:
        # WordBoundary is requested EXPLICITLY (edge-tts defaults to
        # SentenceBoundary) and audio + boundaries come from the SAME
        # stream in one synthesis pass.
        communicate = Communicate(text, voice, boundary=edge_boundary_mode(a.boundary))
        with open(audio_part, "wb") as audio_fh, \
                open(meta_part, "w", encoding="utf-8") as meta_fh:
            async for chunk in communicate.stream():
                if chunk["type"] == "audio":
                    audio_fh.write(chunk["data"])
                elif chunk["type"] == "WordBoundary":
                    meta_fh.write(boundary_line(chunk))
                    meta_fh.write("\n")
                    boundary_count += 1

        if not os.path.exists(audio_part) or os.path.getsize(audio_part) == 0:
            remove_partials(audio_part, meta_part)
            print(json.dumps({"ok": False, "error": "Empty file"}))
            sys.exit(1)

        os.replace(audio_part, out_path)
        if boundary_count > 0 and os.path.exists(meta_part):
            os.replace(meta_part, meta_path)
        else:
            remove_partials(meta_part, meta_path)

        print(json.dumps({
            "ok": True,
            "voice": voice,
            "path": out_path,
            "metadata_path": meta_path if boundary_count > 0 else "",
            "boundary_count": boundary_count,
        }))
    except Exception as exc:  # pylint: disable=broad-except
        remove_partials(audio_part, meta_part)
        print(json.dumps({"ok": False, "error": str(exc)}))
        sys.exit(1)


asyncio.run(main())
