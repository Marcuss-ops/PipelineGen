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
  POST /quit   → 200 {"status":"shutting_down"} (then exits)

Startup: pre-binds a TCP socket to port 0 (OS-assigned), prints the
assigned port on a single line to stdout (e.g. "PORT=12345"), then
starts the aiohttp server. The Go side reads that line to discover
the port. No private-attribute access — the port is known before
the server starts.

Graceful shutdown: POST /quit sets a shutdown event, which unblocks
the startup() coroutine and triggers runner.cleanup(). The Go side
sends quit, waits 5s, then sends SIGKILL if still alive.
"""

import asyncio
import json
import os
import socket
import argparse
import sys
from dataclasses import dataclass

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


# ── JSON-RPC body schema (July 2026) ────────────────────────────────
#
# SynthesizeRequest defines the validated shape of POST /synthesize bodies.
# The fields are intentionally narrow:
#   - `text` is required because edge-tts will reject empty input anyway,
#     but we surface a typed 400 BEFORE contacting the upstream so the
#     operator's structured error path stays intact.
#   - `out` is required because there is no implicit destination — the
#     Go side passes the runtime path.
#   - `lang` defaults to "en" so a bare-bones caller doesn't have to
#     pin a language; fall-through to network `list_voices` happens
#     when `lang` is not in VOICE_OVERRIDES.
#   - `voice` is Optional[str] — when supplied, bypasses VOICE_OVERRIDES
#     and list_voices so the operator's explicit choice is honored verbatim.
#
# Validation lives in `from_dict` (raises SynthesizeRequestError on
# shape errors); `handle_synthesize` catches it and returns the 400
# JSON-RPC error with a human-readable message. The previous manual
# `body.get('text', '')` chain is replaced with the canonical
# dataclass extraction so the schema is a single source of truth.


class SynthesizeRequestError(Exception):
    """Raised by SynthesizeRequest.from_dict on shape errors.

    The orchestrator (handle_synthesize) catches this and forwards
    `message` verbatim as the JSON-RPC `error` field. No `code`
    attribute on the response shape — the pre-fix 400 surface was
    `{"ok": false, "error": "..."}` only and we keep it byte-equivalent.
    """
    def __init__(self, message: str):
        super().__init__(message)
        self.message = message


@dataclass
class SynthesizeRequest:
    """Validated JSON-RPC body for POST /synthesize."""
    text: str
    out: str
    lang: str = "en"
    voice: str = ""

    @classmethod
    def from_dict(cls, body) -> "SynthesizeRequest":
        """Validate + parse. Raises SynthesizeRequestError on shape errors."""
        if not isinstance(body, dict):
            raise SynthesizeRequestError(
                "request body must be a JSON object"
            )
        # Required field: text (non-empty after strip).
        text = str(body.get("text") or "").strip()
        if not text:
            # Catches missing key, None value, empty string, or
            # whitespace-only string. The pre-fix `body.get('text', '')`
            # accepted whitespace-only and let edge-tts fail at
            # voice.resolve — this surfaces the rejection at the
            # boundary so the typed 400 path fires. (Documented
            # behaviour change in the commit body.)
            raise SynthesizeRequestError(
                'missing "text" field'
            )
        # Required field: out.
        out_path = str(body.get("out") or "").strip()
        if not out_path:
            raise SynthesizeRequestError(
                'missing "out" field'
            )
        # Optional field: lang (default "en", normalised to lowercase).
        lang = str(body.get("lang") or "en").strip().lower() or "en"
        # Optional field: voice (verbatim, may be empty).
        voice = str(body.get("voice") or "").strip()
        return cls(text=text, out=out_path, lang=lang, voice=voice)


async def get_voice_for_lang(lang: str) -> str:
    """Resolve the best voice for a language code using VOICE_OVERRIDES.

    Network-fallback observability (July 2026): the previous silent
    `except Exception: voices = []` branch masked transient network
    failures during `list_voices()`. Operator diagnostic for that
    path is now emitted to stderr (one line per category). Happy path
    is byte-identical — the log lines are conditional on the failure /
    fallback branch firing.
    """
    ll = lang.lower().split('-')[0]
    if ll in VOICE_OVERRIDES:
        return VOICE_OVERRIDES[ll]
    # Fallback: try to list voices (requires network).
    try:
        from edge_tts import list_voices
        voices = await list_voices()
    except Exception as e:
        sys.stderr.write(
            f"[tts_edge] list_voices network call failed for lang={lang!r}: "
            f"{type(e).__name__}: {e}\n"
        )
        sys.stderr.flush()
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
    # Both fallback paths surface observability so the operator sees
    # when the canonical "en-US-AriaNeural" / first-voice fallback
    # fires for a non-English query (originally silent).
    if not voices:
        sys.stderr.write(
            f"[tts_edge] no voices available for lang={lang!r}: "
            f"returning canonical fallback 'en-US-AriaNeural'\n"
        )
        sys.stderr.flush()
        return 'en-US-AriaNeural'
    sys.stderr.write(
        f"[tts_edge] no locale match for lang={lang!r}: "
        f"returning first network voice {voices[0]['ShortName']!r} "
        f"(locale={voices[0]['Locale']!r})\n"
    )
    sys.stderr.flush()
    return voices[0]['ShortName']


# ── JSON-RPC handlers ──────────────────────────────────────────────


async def handle_synthesize(request: web.Request) -> web.Response:
    """POST /synthesize — generate TTS audio via edge-tts.

    JSON-RPC body schema is enforced by SynthesizeRequest.from_dict;
    the 400 surfaces a structured `error` message.
    """
    try:
        body = await request.json()
    except Exception:
        return web.json_response(
            {'ok': False, 'error': 'invalid JSON body'},
            status=400,
        )

    try:
        parsed = SynthesizeRequest.from_dict(body)
    except SynthesizeRequestError as e:
        # Response shape stays byte-equivalent with the pre-fix 400
        # surface: only `ok` + `error` keys (no `code` fields).
        return web.json_response(
            {'ok': False, 'error': e.message},
            status=400,
        )

    text = parsed.text
    lang = parsed.lang
    voice_override = parsed.voice
    out_path = parsed.out

    voice = voice_override or await get_voice_for_lang(lang)

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


def main():
    parser = argparse.ArgumentParser(
        description='Edge TTS persistent HTTP server')
    parser.add_argument('--host', default='127.0.0.1',
                        help='Bind host (default: 127.0.0.1)')
    parser.add_argument('--port', type=int, default=0,
                        help='Bind port (default: 0 = OS-assigned)')
    args = parser.parse_args()

    # Pre-bind a TCP socket to discover the port before starting aiohttp.
    # We use web.SockSite below to pass the pre-bound socket directly to
    # aiohttp 3.14+, preventing the EADDRINUSE-on-TIME_WAIT trap that
    # internal socket.close + immediate rebind can hit on rapid restarts.
    # SO_REUSEADDR=1 lets the kernel reclaim the port while the previous
    # TCP connection is in TIME_WAIT — preserves the original behavior
    # (godlike/07 minimum-blast-radius: same intent as the pre-fix code,
    # different aiohttp 3.14 API path).
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        sock.bind((args.host, args.port))
    except OSError as e:
        print(f"FATAL: bind failed on {args.host}:{args.port}: {e}", flush=True)
        raise SystemExit(1)
    actual_port = sock.getsockname()[1]  # e.g. (host, port) → port index 1

    # Print port to stdout — the Go side reads this line.
    print(f"PORT={actual_port}", flush=True)

    # Shutdown event — set by POST /quit.
    shutdown_event = asyncio.Event()

    app = web.Application()
    app.router.add_post('/synthesize', handle_synthesize)
    app.router.add_get('/health', handle_health)

    async def handle_quit(_request: web.Request) -> web.Response:
        """POST /quit — graceful shutdown."""
        shutdown_event.set()
        return web.json_response({'status': 'shutting_down'})

    app.router.add_post('/quit', handle_quit)

    runner = web.AppRunner(app)

    async def startup():
        await runner.setup()
        # aiohttp 3.14+ removed `sock=` from web.TCPSite; the canonical
        # api to wrap a pre-bound socket is web.SockSite (stable since
        # aiohttp 3.x). Keeps the pre-bind free of TOCTOU (the kernel
        # still owns the bound fd when SockSite.start() engages).
        site = web.SockSite(runner, sock)
        await site.start()

        print("SERVER_READY", flush=True)

        # Block until shutdown is requested.
        await shutdown_event.wait()

        # Graceful cleanup.
        await runner.cleanup()

    try:
        asyncio.run(startup())
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == '__main__':
    # godlike/07 NO-FAKE-AVAILABILITY: propagate main()'s return code (or
    # any SystemExit raised inside) so CI gates + the --fail-on-unreachable
    # probe at commit 1b553274 can detect bind/boot failures via exit code.
    sys.exit(main())
