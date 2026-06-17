import asyncio, argparse, json, os, sys
from edge_tts import Communicate, list_voices

# Customizable mapping for high-quality voices per language code.
# You can define and edit the voice chosen for each language here.
VOICE_OVERRIDES = {
    'it': 'fr-FR-RemyMultilingualNeural', # Italiano (Remy Multilingua - scelto)
    'en': 'en-US-RogerNeural',      # Inglese (Roger - Maschile, scelto)
    'es': 'es-ES-AlvaroNeural',      # Spagnolo (Alvaro - Maschile)
    'de': 'de-DE-FlorianMultilingualNeural', # Tedesco (Florian - Maschile Multilingua, scelto)
    'fr': 'fr-FR-HenriNeural',       # Francese (Henri - Maschile)
    'ru': 'ru-RU-DmitryNeural',      # Russo (Dmitry - Maschile)
    'tr': 'tr-TR-AhmetNeural',       # Turco (Ahmet - Maschile, unico disponibile)
    'pl': 'pl-PL-MarekNeural',       # Polacco (Marek - Maschile)
    'id': 'id-ID-ArdiNeural',        # Indonesiano (Ardi - Maschile)
    'pt': 'pt-BR-AntonioNeural',     # Portoghese (Antonio - Maschile, Brasile)
    'nl': 'nl-NL-MaartenNeural',     # Olandese (Maarten - Maschile)
    'ja': 'ja-JP-KeitaNeural',       # Giapponese (Keita - Maschile)
    'zh': 'zh-CN-YunyangNeural',     # Cinese Mandarino (Yunyang - Maschile, scelto)
    'ko': 'ko-KR-InJoonNeural',      # Coreano (InJoon - Maschile)
    'ar': 'ar-SA-HamedNeural',       # Arabo (Hamed - Maschile, Arabia Saudita)
    'hi': 'hi-IN-MadhurNeural',      # Hindi (Madhur - Maschile)
    'sv': 'sv-SE-MattiasNeural',     # Svedese (Mattias - Maschile, unico disponibile)
    'vi': 'vi-VN-NamMinhNeural',     # Vietnamita (NamMinh - Maschile)
    'th': 'th-TH-NiwatNeural',       # Thailandese (Niwat - Maschile)
    'el': 'el-GR-NestorasNeural',     # Greco (Nestoras - Maschile)
    'fi': 'fi-FI-HarriNeural',       # Finlandese (Harri - Maschile)
    'da': 'da-DK-JeppeNeural',       # Danese (Jeppe - Maschile)
    'no': 'no-NO-FinnNeural',        # Norvegese (Finn - Maschile)
    'cs': 'cs-CZ-AntoninNeural',     # Ceco (Antonin - Maschile)
    'hu': 'hu-HU-TamasNeural',       # Ungherese (Tamas - Maschile)
    'ro': 'ro-RO-EmilNeural',        # Rumeno (Emil - Maschile)
    'sk': 'sk-SK-LukasNeural',       # Slovacco (Lukas - Maschile)
    'he': 'he-IL-AvriNeural',        # Ebraico (Avri - Maschile)
}

async def get_voice_for_lang(lang):
    ll = lang.lower().split('-')[0]
    if ll in VOICE_OVERRIDES:
        return VOICE_OVERRIDES[ll]

    voices = await list_voices()
    ll_full = lang.lower()
    parts = ll_full.split('-')
    base = parts[0]
    region = parts[1].upper() if len(parts) > 1 else None
    if region:
        for v in voices:
            if v['Locale'].lower() == ll_full:
                return v['ShortName']
    for v in voices:
        if v['Locale'].lower().startswith(base + '-'):
            if 'Multilingual' in v['ShortName']:
                return v['ShortName']
    for v in voices:
        if v['Locale'].lower().startswith(base + '-'):
            return v['ShortName']
    return voices[0]['ShortName'] if voices else 'en-US-AriaNeural'

async def main():
    p = argparse.ArgumentParser(description='Edge TTS')
    p.add_argument('--text', required=True)
    p.add_argument('--lang', default='it')
    p.add_argument('--out', required=True)
    p.add_argument('--voice')
    a = p.parse_args()
    v = a.voice or await get_voice_for_lang(a.lang)
    try:
        c = Communicate(a.text, v)
        await c.save(a.out)
        if not os.path.exists(a.out) or os.path.getsize(a.out) == 0:
            print(json.dumps({'ok': False, 'error': 'Empty file'}))
            sys.exit(1)
        print(json.dumps({'ok': True, 'voice': v, 'path': os.path.abspath(a.out)}))
    except Exception as e:
        print(json.dumps({'ok': False, 'error': str(e)}))
        sys.exit(1)

asyncio.run(main())
