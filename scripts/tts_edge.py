import asyncio, argparse, json, os, sys
from edge_tts import Communicate, list_voices

async def get_voice_for_lang(lang):
    voices = await list_voices()
    ll = lang.lower()
    parts = ll.split('-')
    base = parts[0]
    region = parts[1].upper() if len(parts) > 1 else None
    if region:
        for v in voices:
            if v['Locale'].lower() == ll:
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
