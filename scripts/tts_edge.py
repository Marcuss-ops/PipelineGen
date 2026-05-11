import asyncio
import argparse
import json
import os
import sys
from edge_tts import Communicate

async def main():
    parser = argparse.ArgumentParser(description="Edge TTS Generator")
    parser.add_argument("--text", required=True, help="Text to speak")
    parser.add_argument("--lang", default="it", help="Language code (e.g., it, en-US)")
    parser.add_argument("--out", required=True, help="Output file path")
    parser.add_argument("--voice", help="Specific voice name")
    
    args = parser.parse_args()
    
    # Map simple lang codes to edge-tts voices if not provided
    voice = args.voice
    if not voice:
        voice_map = {
            "it": "it-IT-ElsaNeural",
            "en": "en-US-AriaNeural",
            "es": "es-ES-ElviraNeural",
            "fr": "fr-FR-DeniseNeural",
            "de": "de-DE-KatjaNeural"
        }
        # Try to find a match or use a default
        base_lang = args.lang.split('-')[0].lower()
        voice = voice_map.get(base_lang, "it-IT-ElsaNeural")

    try:
        communicate = Communicate(args.text, voice)
        await communicate.save(args.out)
        
        result = {
            "ok": True,
            "voice": voice,
            "path": os.path.abspath(args.out)
        }
        print(json.dumps(result))
    except Exception as e:
        result = {
            "ok": False,
            "error": str(e)
        }
        print(json.dumps(result))
        sys.exit(1)

if __name__ == "__main__":
    asyncio.run(main())
