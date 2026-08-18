"""Argos Translate persistent HTTP sidecar.

PR-ARGOS-TRANSLATION (Aug 2026): replaces the spawn-per-call subprocess
bridge with a long-lived sidecar so the OpenNMT models are loaded ONCE and
shared across all per-language translations (no per-call model reload).

Uses the stdlib ThreadingHTTPServer: Argos is blocking and CPU-bound, so
thread-per-request matches its nature without pulling in aiohttp (keeping
the Argos venv dependency-free).

Protocol:
  POST /translate  {"text":"...", "source":"en", "target":"it"}
    -> 200 {"translated_text":"...", "source":"en", "target":"it",
            "model":"argos-en-it", "via":"direct"}
    -> 400 {"error":"..."}  on missing/unsupported input
  GET /health -> 200 {"status":"ok"}
  POST /quit -> 200 {"status":"shutting_down"} (then exits)

Startup: binds with an OS-assigned port and prints "PORT=<n>" on stdout
(the Go side reads that line, mirroring tts_edge_server.py).
"""

import argparse
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .core import translate_text

# Serialise translation calls: Argos' model cache is process-global and the
# per-language calls are short, so a single lock is safer than relying on
# the library's internal caching being thread-safe.
_translate_lock = threading.Lock()


class _Handler(BaseHTTPRequestHandler):
    server_version = "ArgosTranslateSidecar/1.0"

    def _json(self, status, payload):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802 — stdlib handler method name
        if self.path == "/health":
            self._json(200, {"status": "ok"})
        else:
            self._json(404, {"error": "not found"})

    def do_POST(self):  # noqa: N802 — stdlib handler method name
        if self.path == "/quit":
            self._json(200, {"status": "shutting_down"})
            # shutdown() must run outside the serving thread.
            threading.Thread(target=self.server.shutdown, daemon=True).start()
            return
        if self.path != "/translate":
            self._json(404, {"error": "not found"})
            return

        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw.decode("utf-8"))
        except Exception:
            self._json(400, {"error": "invalid JSON body"})
            return

        if not isinstance(body, dict):
            self._json(400, {"error": "request body must be a JSON object"})
            return

        text = str(body.get("text") or "")
        source = str(body.get("source") or "")
        target = str(body.get("target") or "")
        if not text.strip():
            self._json(400, {"error": "missing \"text\" field"})
            return
        if not target:
            self._json(400, {"error": "missing \"target\" field"})
            return

        with _translate_lock:
            result = translate_text(text, source, target)

        if result.get("error"):
            self._json(400, result)
            return
        self._json(200, result)

    def log_message(self, fmt, *args):  # noqa: N802 — stdlib handler method
        # Route access logs to stderr so stdout stays clean for the PORT
        # handshake line.
        sys.stderr.write("%s - %s\n" % (self.address_string(), fmt % args))


class _Server(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    parser = argparse.ArgumentParser(
        description="Argos Translate persistent HTTP server")
    parser.add_argument("--host", default="127.0.0.1",
                        help="Bind host (default: 127.0.0.1)")
    parser.add_argument("--port", type=int, default=0,
                        help="Bind port (default: 0 = OS-assigned)")
    args = parser.parse_args()

    try:
        httpd = _Server((args.host, args.port), _Handler)
    except OSError as exc:
        print("FATAL: bind failed on %s:%s: %s" % (args.host, args.port, exc),
              flush=True)
        return 1

    actual_port = httpd.server_address[1]
    print("PORT=%d" % actual_port, flush=True)

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        httpd.server_close()
    return 0


if __name__ == "__main__":
    # Godlike/07 no-fake-availability: propagate main()'s exit code so CI
    # gates can detect bind/boot failures via exit code.
    sys.exit(main())
