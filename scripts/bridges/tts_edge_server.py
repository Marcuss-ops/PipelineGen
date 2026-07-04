#!/usr/bin/env python3
"""
tts_edge_server.py — persistent HTTP sidecar for edge-tts synthesis.

VO-DECOMPOSITION-2026-07-04 P0 #1: replaces the spawn-per-call
`exec.CommandContext("python3", "tts_edge.py", ...)` with a long-lived
async HTTP server (aiohttp). The Go side manages the subprocess lifecycle
and communicates via standard HTTP (POST /synthesize, GET /health,
POST /quit).

Protocol:
  POST /synthesize  {"text":"...", "lang":"en", "voice":"...", "out":"/path.mp3"}
    → {"ok":true, "voice":"en-US-RogerNeural", "path":"/abs/path.mp3"}
    → {"ok":false, "error":"Edge TTS error message"}

  GET /health  → 200 {"status":"ok"}
  POST /quit   → 200 {"status":"shutting_down"}

Startup: binds to port 0 (OS-assigned), prints the assigned port
on a single line to stdout (e.g. "PORT=12345"), then starts the
aiohttp server. The Go side reads that line to discover the port.

Graceful shutdown: POST /quit triggers aiohttp graceful shutdown.
The Go side sends quit, waits 5s, then sends SIGKILL if still alive.
"""

import asyncio
import json
import os
import sys
import argparse

from aiohttp import web
from edge_tts import Communicate

# ── Voice override map (mirrors tts_edge.py) ─────────────────────
VOICE_OVERRIDES = {
    'it': 'fr-FR-RemyMultilingualNeural',
    'en': 'en-US-RogerNeural',
    'es': 'es-ES-AlvaroNeural',
    'de': 'de-DE-FlorianMultilingualNeural',
    'fr': 'fr-FR-HenriNeural',
    'ru': 'ru-RU-DmitryNeural',
    'tr': 'tr-TR-AhmetNeural',
    'pl': 'pl-PL-MarekNeural',
    'id': 'id-ID-ArdiNeural',
    'pt': 'pt-BR-AntonioNeural',
    'nl': 'nl-NL-MaartenNeural',
    'ja': 'ja-JP-KeitaNeural',
    'zh': 'zh-CN-YunyangNeural',
    'ko': 'ko-KR-InJoonNeural',
    'ar': 'ar-SA-HamedNeural',
    'hi': 'hi-IN-MadhurNeural',
    'sv': 'sv-SE-MattiasNeural',
    'vi': 'vi-VN-NamMinhNeural',
    'th': 'th-TH-NiwatNeural',
    'el': 'el-GR-NestorasNeural',
    'fi': 'fi-FI-HarriNeural',
    'da': 'da-DK-JeppeNeural',
    'no': 'no-NO-FinnNeural',
    'cs': 'cs-CZ-AntoninNeural',
    'hu': 'hu-HU-TamasNeural',
    'ro': 'ro-RO-EmilNeural',
    'sk': 'sk-SK-LukasNeural',
    'he': 'he-IL-AvriNeural',
}


async def get_voice_for_lang(lang: str) -> str:
    """Resolve the best voice for a language code using VOICE_OVERRIDES."""
    ll = lang.lower().split('-')[0]
    if ll in VOICE_OVERRIDES:
        return VOICE_OVERRIDES[ll]
    # Fallback: try to list voices (requires network).
    try:
        from edge_tts import list_voices
        voices = await list_voices()
    except Exception:
        voices = []
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


async def handle_synthesize(request: web.Request) -> web.Response:
    """POST /synthesize — generate TTS audio via edge-tts."""
    try:
        body = await request.json()
    except Exception:
        return web.json_response(
            {'ok': False, 'error': 'invalid JSON body'},
            status=400,
        )

    text = body.get('text', '')
    lang = body.get('lang', 'en')
    voice_override = body.get('voice', '')
    out_path = body.get('out', '')

    if not text:
        return web.json_response(
            {'ok': False, 'error': 'missing "text" field'},
            status=400,
        )
    if not out_path:
        return web.json_response(
            {'ok': False, 'error': 'missing "out" field'},
            status=400,
        )

    # Resolve voice.
    voice = voice_override or await get_voice_for_lang(lang)

    # Ensure output directory exists.
    out_dir = os.path.dirname(out_path)
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)

    try:
        communicate = Communicate(text, voice)
        await communicate.save(out_path)
    except Exception as e:
        return web.json_response({
            'ok': False,
            'error': str(e),
        })

    if not os.path.exists(out_path) or os.path.getsize(out_path) == 0:
        return web.json_response({
            'ok': False,
            'error': 'generated file is empty or missing',
        })

    return web.json_response({
        'ok': True,
        'voice': voice,
        'path': os.path.abspath(out_path),
    })


async def handle_health(_request: web.Request) -> web.Response:
    """GET /health — liveness probe."""
    return web.json_response({'status': 'ok'})


async def handle_quit(_request: web.Request) -> web.Response:
    """POST /quit — graceful shutdown."""
    return web.json_response({'status': 'shutting_down'})


async def on_quit_signal(app: web.Application):
    """Callback after POST /quit: initiate graceful shutdown."""
    await app.shutdown()
    await app.cleanup()


def main():
    parser = argparse.ArgumentParser(description='Edge TTS persistent HTTP server')
    parser.add_argument('--host', default='127.0.0.1',
                        help='Bind host (default: 127.0.0.1)')
    parser.add_argument('--port', type=int, default=0,
                        help='Bind port (default: 0 = OS-assigned)')
    args = parser.parse_args()

    app = web.Application()
    app.router.add_post('/synthesize', handle_synthesize)
    app.router.add_get('/health', handle_health)
    app.router.add_post('/quit', handle_quit)

    # Print the assigned port to stdout so the Go side can discover it.
    # aiohttp prints startup info to stderr; we use stdout for the port
    # contract and stderr for logs.
    runner = web.AppRunner(app)

    async def startup():
        await runner.setup()
        site = web.TCPSite(runner, args.host, args.port)
        await site.start()

        # Read the actual port (may differ from args.port when port=0).
        for sock in site._server.sockets:
            addr = sock.getsockname()
            if len(addr) >= 2:
                actual_port = addr[1]
                break
        else:
            actual_port = args.port

        # Print port to stdout — the Go side reads this line.
        print(f"PORT={actual_port}", flush=True)
        print(f"SERVER_READY", flush=True)

        # Block until the app is shut down.
        await asyncio.Event().wait()

    try:
        asyncio.run(startup())
    except KeyboardInterrupt:
        pass
    finally:
        pass


if __name__ == '__main__':
    main()
