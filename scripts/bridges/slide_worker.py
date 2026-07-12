#!/usr/bin/env python3
"""
slide_worker — CLI shim (THIN, July 2026 wave refactor).

Every orchestration concern lives in scripts/bridges/slide_worker_runtime/:

    session.py      — BrowserSession (Playwright state)
    generation.py   — GenerationContext, StepError, GenerationRunner + DOM helpers
    dispatcher.py   — ProfileWorker (slim threading.Thread), SlideDispatcher
    protocol.py     — JSONL wire types + parse_request / write_response
    diagnostics.py  — _log, _log_diag, _screenshot_on_failure
    image_quality.py — _save_image_bytes, _compute_pixel_stats, PixelStats
    candidates.py   — _extract_candidates, _clear_image_library_panel
    selectors.py    — canonical Playwright selector fragments
    config.py       — env-derived constants (MASTER_STORAGE, etc.)

Wire protocol (stdin → stdout, one JSON object per line):

  REQUEST   {"action": "warmup"|"generate"|"health"|"health_deep"|"quit", ...}
  RESPONSE  {"status": "ok"|"error"|"ready", ...canonical-shape-from-runtime}

Single-profile contract: num_profiles is hard-clamped to 1 today;
forward-expansion to multi-profile would route by hash(req["id"]) %
len(profiles). The runtime dispatcher enforces this clamp.
"""

import argparse
import signal
import sys

from slide_worker_runtime.protocol import parse_request, write_response
from slide_worker_runtime.diagnostics import _log
from slide_worker_runtime.dispatcher import SlideDispatcher


def _install_signal_handlers(dispatcher: SlideDispatcher) -> None:
    """Wire SIGINT + SIGTERM to dispatcher.shutdown_all (idempotent).

    A double-tap (SIGINT + main-loop finally) is safe — the dispatcher
    marks _shutdown_called on first invocation and short-circuits on
    subsequent calls.
    """
    def _shutdown(sig: int, _frame) -> None:
        _log(f"slide_worker: received signal {sig}; shutting down")
        try:
            dispatcher.shutdown_all()
        finally:
            sys.exit(0)

    signal.signal(signal.SIGINT, _shutdown)
    signal.signal(signal.SIGTERM, _shutdown)


def _run_protocol_loop(dispatcher: SlideDispatcher) -> int:
    """Read JSONL requests from stdin; relay to SlideDispatcher; write
    JSONL responses to stdout. Returns the process exit code."""

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = parse_request(line)
        except Exception as e:
            write_response({
                "status": "error",
                "error": f"malformed request: {type(e).__name__}: {e}",
            })
            continue

        action = req.get("action")
        if action == "warmup":
            write_response(dispatcher.warmup_all())
        elif action == "generate":
            try:
                resp = dispatcher.dispatch_generate(req)
            except Exception as e:
                # Defensive last-resort: dispatcher.dispatch_generate is
                # a blocking queue read; the runner already emits typed
                # responses for every failure path, so this catch fires
                # only against bugs in the dispatcher itself.
                resp = {
                    "id": req.get("id", ""),
                    "status": "error",
                    "error": f"{type(e).__name__}: {e}",
                    "code": "ErrInternalDispatch",
                }
            write_response(resp)
        elif action == "health":
            write_response(dispatcher.health_all())
        elif action == "health_deep":
            write_response(dispatcher.health_deep_all())
        elif action == "quit":
            write_response({"status": "ok", "action": "quit"})
            return 0
        else:
            write_response({
                "status": "error",
                "error": f"unknown action: {action!r}",
            })

    return 0  # EOF on stdin before "quit" → exit cleanly.


def main() -> int:
    parser = argparse.ArgumentParser(
        description="PipelineGen persistent Chrome/Playwright image generation worker"
    )
    parser.add_argument(
        "--profiles",
        type=int,
        default=1,
        help="Number of profiles (must be 1 today; multi-profile is WIP)",
    )
    parser.add_argument(
        "--headful",
        action="store_true",
        help="Run browser in non-headless mode (debugging surface)",
    )
    args = parser.parse_args()

    dispatcher = SlideDispatcher(num_profiles=args.profiles, headful=args.headful)

    # Install signal handlers BEFORE warmup_all so a SIGINT during a
    # hung Playwright launch still drains the dispatcher (no zombie
    # threads + no KeyboardInterrupt traceback). The idempotent
    # `_shutdown_called` short-circuit + the `self.profiles=[]` no-op
    # path make the pre-warmup invocation safe.
    _install_signal_handlers(dispatcher)

    ready = dispatcher.warmup_all()
    write_response(ready)
    if ready.get("status") != "ready":
        _log("slide_worker: warmup failed; exiting without serving requests")
        dispatcher.shutdown_all()
        return 1

    try:
        return _run_protocol_loop(dispatcher)
    finally:
        dispatcher.shutdown_all()


if __name__ == "__main__":
    sys.exit(main())
