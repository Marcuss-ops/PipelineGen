#!/usr/bin/env python3
import asyncio
import argparse
import sys
import edge_tts
import json

VOICES = {
    "it": "it-IT-ElsaNeural",
    "it-it": "it-IT-ElsaNeural",
    "it-it-giuseppe": "it-IT-GiuseppeNeural",
    "en": "en-US-AriaNeural",
    "en-us": "en-US-AriaNeural",
    "en-gb": "en-GB-SoniaNeural",
    "en-au": "en-AU-NatashaNeural",
    "es": "es-ES-ElviraNeural",
    "es-es": "es-ES-ElviraNeural",
    "es-mx": "es-MX-DaliaNeural",
    "fr": "fr-FR-DeniseNeural",
    "fr-fr": "fr-FR-DeniseNeural",
    "fr-ca": "fr-CA-SylvieNeural",
    "de": "de-DE-KatjaNeural",
    "pt": "pt-BR-FranciscaNeural",
    "pt-br": "pt-BR-FranciscaNeural",
    "pt-pt": "pt-PT-RaquelNeural",
    "ru": "ru-RU-SvetlanaNeural",
    "zh": "zh-CN-XiaoxiaoNeural",
    "ja": "ja-JP-NanamiNeural",
    "ko": "ko-KR-SunHiNeural"
}

async def generate_voiceover(text, language, output_path):
    lang_key = language.lower()
    
    # Logica di selezione:
    # 1. Se lang_key è esattamente nella nostra mappa (es. "it" o "en-gb")
    # 2. Se l'utente ha passato direttamente un nome voce valido (contiene '-')
    # 3. Fallback su Inglese
    
    voice = VOICES.get(lang_key)
    if not voice:
        if "-" in language and len(language) > 5: # Probabile nome voce completo
            voice = language
        else:
            # Prova a prendere solo la prima parte (es. "it-IT" -> "it")
            base_lang = lang_key.split("-")[0]
            voice = VOICES.get(base_lang, VOICES["en"])
    
    communicate = edge_tts.Communicate(text, voice)
    await communicate.save(output_path)
    
    # Estrai info base
    # (In un'implementazione reale potremmo usare ffprobe per la durata esatta)
    return {
        "ok": True,
        "voice": voice,
        "path": output_path
    }

def main():
    parser = argparse.ArgumentParser(description="EdgeTTS Voiceover Generator")
    parser.add_argument("--text", required=True, help="Text to convert to speech")
    parser.add_argument("--lang", default="it", help="Language code (it, en, etc.)")
    parser.add_argument("--out", required=True, help="Output MP3 path")
    
    args = parser.parse_args()
    
    try:
        loop = asyncio.get_event_loop_policy().get_event_loop()
        result = loop.run_until_complete(generate_voiceover(args.text, args.lang, args.out))
        print(json.dumps(result))
    except Exception as e:
        print(json.dumps({"ok": false, "error": str(e)}))
        sys.exit(1)

if __name__ == "__main__":
    main()
