"""Persistent HTTP sidecar for local (open-weight) TTS engines.

Wire contract (deliberately identical to edge_tts_bridge/server.py so the
Go processor needs no second protocol):

  POST /synthesize
    {"text":"...", "lang":"en", "voice":"alba", "out":"/path.mp3",
     "engine":"pocket", "boundary":"none"}
      -> {"ok":true, "engine":"pocket", "voice":"alba",
          "path":"/abs/path.mp3", "metadata_path":"", "boundary_count":0,
          "synth_ms":812, "audio_duration_ms":9100, "rtf":0.089}
      -> {"ok":false, "error":"..."}

  GET  /health   -> {"status":"ok","engines":[...]}
  GET  /engines  -> {"engines":[{"name":"pocket","available":true,...}]}
  POST /quit     -> {"status":"shutting_down"}

Timing: local engines do not expose native word boundaries. The sidecar
therefore returns zero boundaries and an empty metadata_path — it never
invents timings. The caller's best_effort policy degrades to "timing
unavailable"; a timing_required request must stay on the Edge bridge until
a forced aligner (C2) is wired.

Audio: the engine writes a WAV; the sidecar transcodes it to the requested
extension (mp3 by default, 48 kHz stereo) so the existing processor's
duration/loudness/format contract is unchanged.
"""

import argparse
import asyncio
import os
import socket
import subprocess
import sys
import time
from pathlib import Path

from aiohttp import web

from .engines import EngineError, build_registry
from .request import SynthesizeRequest, SynthesizeRequestError


def _ffmpeg() -> str:
    return os.environ.get("FFMPEG_PATH", "ffmpeg")


def _ffprobe() -> str:
    return os.environ.get("FFPROBE_PATH", "ffprobe")


def _transcode(src: str, dst_part: str) -> None:
    """Transcode the engine WAV to the requested container/codec.

    The destination extension drives the codec: mp3 (default) keeps the
    existing `format=mp3-48k-stereo` fingerprint contract, wav is used by
    the bench harness for lossless A/B listening.
    """
    ext = Path(dst_part).suffix.lower()
    if ext == ".wav":
        codec = ["-c:a", "pcm_s16le"]
    elif ext in (".ogg", ".opus"):
        codec = ["-c:a", "libopus", "-b:a", "128k"]
    else:
        codec = ["-c:a", "libmp3lame", "-b:a", "192k"]
    subprocess.run(
        [
            _ffmpeg(),
            "-y",
            "-loglevel",
            "error",
            "-i",
            src,
            "-ac",
            "2",
            "-ar",
            "48000",
            *codec,
            dst_part,
        ],
        check=True,
    )


def _duration_ms(path: str) -> int:
    proc = subprocess.run(
        [
            _ffprobe(),
            "-v",
            "error",
            "-show_entries",
            "format=duration",
            "-of",
            "default=nw=1:nk=1",
            path,
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    raw = proc.stdout.strip()
    if not raw:
        return 0
    try:
        return int(round(float(raw) * 1000))
    except ValueError:
        return 0


def _remove(*paths: str) -> None:
    for path in paths:
        try:
            if path and os.path.exists(path):
                os.remove(path)
        except OSError:
            pass


async def handle_synthesize(request: web.Request) -> web.Response:
    default_engine = request.app["default_engine"]
    registry = request.app["registry"]

    try:
        body = await request.json()
    except Exception:
        return web.json_response({"ok": False, "error": "invalid JSON body"}, status=400)

    try:
        parsed = SynthesizeRequest.from_dict(body, default_engine)
    except SynthesizeRequestError as exc:
        return web.json_response({"ok": False, "error": exc.message}, status=400)

    engine = registry.get(parsed.engine)
    if engine is None:
        known = ", ".join(sorted(registry))
        return web.json_response(
            {"ok": False, "error": f"unknown engine {parsed.engine!r} (known: {known})"},
            status=400,
        )

    available, detail = engine.available()
    if not available:
        return web.json_response(
            {"ok": False, "error": f"engine {parsed.engine!r} unavailable: {detail}"},
            status=503,
        )

    out_path = os.path.abspath(parsed.out)
    out_dir = os.path.dirname(out_path)
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)

    tmp_wav = out_path + ".engine.wav"
    # Keep the FINAL extension on the temp file: ffmpeg infers the muxer from
    # the output suffix, and a bare `.part` suffix fails with
    # "Error initializing the muxer ... Invalid argument".
    part = out_path + ".part" + Path(out_path).suffix

    try:
        async with request.app["sem"]:
            started = time.monotonic()
            result = await asyncio.to_thread(
                engine.synthesize, parsed.text, parsed.lang, parsed.voice, tmp_wav
            )
            synth_ms = result.synth_ms or int((time.monotonic() - started) * 1000)
            await asyncio.to_thread(_transcode, result.wav_path, part)

        if not os.path.exists(part) or os.path.getsize(part) == 0:
            raise EngineError("transcoded output is empty or missing")

        os.replace(part, out_path)
        duration_ms = await asyncio.to_thread(_duration_ms, out_path)
        rtf = round(synth_ms / duration_ms, 4) if duration_ms > 0 else None

        return web.json_response(
            {
                "ok": True,
                "engine": parsed.engine,
                "voice": result.voice or parsed.voice,
                "path": out_path,
                "metadata_path": "",
                "boundary_count": 0,
                "synth_ms": synth_ms,
                "audio_duration_ms": duration_ms,
                "rtf": rtf,
            }
        )
    except Exception as exc:  # pylint: disable=broad-except
        # Never delete a pre-existing valid output: only the partial files
        # produced by this request are disposable.
        _remove(part)
        return web.json_response(
            {"ok": False, "error": f"{type(exc).__name__}: {exc}"}, status=500
        )
    finally:
        _remove(tmp_wav)


async def handle_health(request: web.Request) -> web.Response:
    engines = [
        {
            "name": name,
            "available": engine.available()[0],
            "detail": engine.available()[1],
        }
        for name, engine in sorted(request.app["registry"].items())
    ]
    return web.json_response({"status": "ok", "engines": engines})


async def handle_engines(request: web.Request) -> web.Response:
    payload = []
    for name, engine in sorted(request.app["registry"].items()):
        available, detail = engine.available()
        payload.append(
            {
                "name": name,
                "available": available,
                "detail": detail,
                "license": engine.license,
                "supports_timing": False,
            }
        )
    return web.json_response({"engines": payload})


def main() -> int:
    parser = argparse.ArgumentParser(description="Local TTS persistent HTTP server")
    parser.add_argument("--host", default="127.0.0.1", help="Bind host")
    parser.add_argument("--port", type=int, default=0, help="Bind port (0 = OS-assigned)")
    parser.add_argument(
        "--default-engine",
        default=os.environ.get("LOCAL_TTS_ENGINE", "pocket"),
        help="Engine used when the request omits it",
    )
    args = parser.parse_args()

    registry = build_registry()
    if args.default_engine not in registry:
        print(
            f"FATAL: default engine {args.default_engine!r} is not registered "
            f"(known: {', '.join(sorted(registry))})",
            file=sys.stderr,
            flush=True,
        )
        return 1

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        sock.bind((args.host, args.port))
    except OSError as exc:
        print(f"FATAL: bind failed on {args.host}:{args.port}: {exc}", flush=True)
        raise SystemExit(1)
    actual_port = sock.getsockname()[1]
    print(f"PORT={actual_port}", flush=True)

    shutdown_event = asyncio.Event()

    app = web.Application()
    app["registry"] = registry
    app["default_engine"] = args.default_engine
    app["sem"] = asyncio.Semaphore(max(1, int(os.environ.get("LOCAL_TTS_CONCURRENCY", "1"))))
    app.router.add_post("/synthesize", handle_synthesize)
    app.router.add_get("/health", handle_health)
    app.router.add_get("/engines", handle_engines)

    async def handle_quit(_request: web.Request) -> web.Response:
        shutdown_event.set()
        return web.json_response({"status": "shutting_down"})

    app.router.add_post("/quit", handle_quit)

    runner = web.AppRunner(app)

    async def startup():
        await runner.setup()
        site = web.SockSite(runner, sock)
        await site.start()
        print("SERVER_READY", flush=True)
        await shutdown_event.wait()
        await runner.cleanup()

    try:
        asyncio.run(startup())
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
