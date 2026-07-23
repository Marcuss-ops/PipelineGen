import asyncio
import argparse
import json
import os
import sys

from edge_tts import Communicate

from edge_tts.request import SynthesizeRequest, SynthesizeRequestError, normalize_language
from edge_tts.voice_resolver import resolve_voice


async def main():
    p = argparse.ArgumentParser(description="Edge TTS")
    p.add_argument("--text", default="")
    p.add_argument("--lang", default="it")
    p.add_argument("--out", required=True)
    p.add_argument("--voice")
    a = p.parse_args()

    text = a.text
    if not text:
        text = sys.stdin.read()
    if not text:
        print(json.dumps({"ok": False, "error": "No text provided (--text empty and stdin empty)"}))
        sys.exit(1)

    lang = normalize_language(a.lang)
    voice = a.voice or await resolve_voice(lang)

    try:
        communicate = Communicate(text, voice)
        await communicate.save(a.out)
        if not os.path.exists(a.out) or os.path.getsize(a.out) == 0:
            print(json.dumps({"ok": False, "error": "Empty file"}))
            sys.exit(1)
        print(json.dumps({"ok": True, "voice": voice, "path": os.path.abspath(a.out)}))
    except Exception as exc:  # pylint: disable=broad-except
        print(json.dumps({"ok": False, "error": str(exc)}))
        sys.exit(1)


asyncio.run(main())
