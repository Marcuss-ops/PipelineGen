"""Edge TTS persistent HTTP sidecar.

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

import argparse
import asyncio
import os
import socket
import sys

from aiohttp import web
from edge_tts import Communicate

from .request import SynthesizeRequest, SynthesizeRequestError
from .voice_resolver import resolve_voice


async def handle_synthesize(request: web.Request) -> web.Response:
    """POST /synthesize — generate TTS audio via edge-tts.

    JSON-RPC body schema is enforced by SynthesizeRequest.from_dict;
    the 400 surfaces a structured `error` message.
    """
    try:
        body = await request.json()
    except Exception:
        return web.json_response(
            {"ok": False, "error": "invalid JSON body"},
            status=400,
        )

    try:
        parsed = SynthesizeRequest.from_dict(body)
    except SynthesizeRequestError as exc:
        # Response shape stays byte-equivalent with the pre-fix 400
        # surface: only `ok` + `error` keys (no `code` fields).
        return web.json_response(
            {"ok": False, "error": exc.message},
            status=400,
        )

    voice = await resolve_voice(parsed.lang, parsed.voice or None)

    out_dir = os.path.dirname(parsed.out)
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)

    try:
        communicate = Communicate(parsed.text, voice)
        await communicate.save(parsed.out)
    except Exception as exc:  # pylint: disable=broad-except
        return web.json_response({
            "ok": False,
            "error": str(exc),
        })

    if not os.path.exists(parsed.out) or os.path.getsize(parsed.out) == 0:
        return web.json_response({
            "ok": False,
            "error": "generated file is empty or missing",
        })

    return web.json_response({
        "ok": True,
        "voice": voice,
        "path": os.path.abspath(parsed.out),
    })


async def handle_health(_request: web.Request) -> web.Response:
    """GET /health — liveness probe."""
    return web.json_response({"status": "ok"})


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Edge TTS persistent HTTP server")
    parser.add_argument(
        "--host", default="127.0.0.1",
        help="Bind host (default: 127.0.0.1)")
    parser.add_argument(
        "--port", type=int, default=0,
        help="Bind port (default: 0 = OS-assigned)")
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
    except OSError as exc:
        print(f"FATAL: bind failed on {args.host}:{args.port}: {exc}",
              flush=True)
        raise SystemExit(1)
    actual_port = sock.getsockname()[1]

    # Print port to stdout — the Go side reads this line.
    print(f"PORT={actual_port}", flush=True)

    # Shutdown event — set by POST /quit.
    shutdown_event = asyncio.Event()

    app = web.Application()
    app.router.add_post("/synthesize", handle_synthesize)
    app.router.add_get("/health", handle_health)

    async def handle_quit(_request: web.Request) -> web.Response:
        """POST /quit — graceful shutdown."""
        shutdown_event.set()
        return web.json_response({"status": "shutting_down"})

    app.router.add_post("/quit", handle_quit)

    runner = web.AppRunner(app)

    async def startup():
        await runner.setup()
        # aiohttp 3.14+ removed `sock=` from web.TCPSite; the canonical
        # API to wrap a pre-bound socket is web.SockSite (stable since
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


if __name__ == "__main__":
    # godlike/07 NO-FAKE-AVAILABILITY: propagate main()'s return code (or
    # any SystemExit raised inside) so CI gates + the --fail-on-unreachable
    # probe at commit 1b553274 can detect bind/boot failures via exit code.
    sys.exit(main())
