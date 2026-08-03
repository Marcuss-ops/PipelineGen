"""ProfileWorker slim + SlideDispatcher — dispatch layer of slide_worker.

Wave Commit 4 (July 2026):
  - ProfileWorker is now THIN: it owns ONLY the threading primitives
    (`threading.Thread` subclass), the queues (in_queue /
    out_queue), and warmup-state events (`_warmed` Event,
    `_warmup_error` str-cast, `_running` flag). It delegates:
      - All Playwright interactions → BrowserSession
        (runtime.session)
      - All per-request orchestration → GenerationRunner
        (runtime.generation)
  - SlideDispatcher stays here too: it owns multi-profile coordination
    (warmup_all / dispatch_generate / health_all /
    health_deep_all / shutdown_all). Multi-profile remains a
    single-profile model today (num_profiles is hard-clamped to 1),
    but the surface expansion is forward-compat.

Per godlike/06 SSOT:
  - ProfileWorker is the SINGLE canonical thread owner for
    BrowserSession (Playwright sync API requires single thread
    ownership of context; the threaded dispatch model would
    violate this without explicit ownership).
  - SlideDispatcher is the SINGLE request-routing surface. It is
    what the CLI shim (slide_worker.py) talks to.

Per godlike/07 fail-closed:
  - ProfileWorker.run() catches `runner.run()` failures ONLY as a
    last-resort defensive guard against bugs in the runner itself
    (runner.run() should already convert all exceptions to typed
    response dicts internally; this catch is for unexpected bugs).
"""

from __future__ import annotations

import queue
import threading
from typing import List, Optional

from .diagnostics import _log, _log_diag
from .session import BrowserSession
from .generation import GenerationRunner


class ProfileWorker(threading.Thread):
    """Slim threading.Thread owning one BrowserSession + one GenerationRunner.

    Lifecycle:
      construction (callers add it to a SlideDispatcher.profiles list)
      → start() → run() begins
        → BrowserSession.start() (warmup)
        → GenerationRunner(session, profile_id) instantiation
        → loop: in_queue.get() per request → runner.run(request)
          → out_queue.put(result)
        → BrowserSession.close() (final)
      stop() → in_queue.put(None) → run loop exits on next iteration
    """

    def __init__(self, profile_id: int, headful: bool = False) -> None:
        super().__init__(daemon=True, name=f"profile-{profile_id}")
        self.profile_id = profile_id
        self.headful = headful
        self.in_queue: "queue.Queue" = queue.Queue(maxsize=1)
        self.out_queue: "queue.Queue" = queue.Queue()
        # session + runner are populated in run() after warmup
        self.session: Optional[BrowserSession] = None
        self.runner: Optional[GenerationRunner] = None
        # Warmup state observers (consumed by SlideDispatcher.warmup_all)
        self._warmed = threading.Event()
        self._warmup_error: Optional[str] = None
        self._running = True

    # ── Thread loop ──────────────────────────────────────────────────

    def run(self) -> None:
        """Run once per thread. Owns the session lifecycle + the
        request-routing queue loop. Side effects centralised: NO
        Playwright calls outside `self.session.XXX`. NO
        per-request orchestration outside `self.runner.run(req)`.
        """
        try:
            self.session = BrowserSession(self.profile_id, headful=self.headful)
            self.session.start()
            self.runner = GenerationRunner(
                self.session, profile_id=self.profile_id
            )
        except Exception as e:
            # godlike/07: surface the warmup failure as `_warmup_error`
            # so SlideDispatcher.warmup_all can return the typed
            # response shape (preserving the wire contract).
            self._warmup_error = str(e)
            self._warmed.set()
            _log(f"[profile-{self.profile_id}] warmup failed: {e}")
            # BrowserSession.start() may have launched Chromium before
            # failing (for example on an auth guard or navigation error).
            # Close the session here so a failed warmup cannot orphan the
            # browser when the parent worker is reaped by the Go supervisor.
            try:
                if self.session:
                    self.session.close()
            except Exception as close_error:
                _log(
                    f"[profile-{self.profile_id}] warmup cleanup failed: "
                    f"{type(close_error).__name__}: {close_error}"
                )
            return

        self._warmed.set()
        _log(f"[profile-{self.profile_id}] warmup: ready")

        while self._running:
            try:
                req = self.in_queue.get(timeout=1)
            except queue.Empty:
                continue
            if req is None:
                break
            try:
                result = self.runner.run(req)
            except Exception as e:
                # Defensive last-resort: runner.run is supposed to
                # convert all exceptions to typed dict internally;
                # this catch exists only to keep the thread alive
                # against surprising bugs in the runner code.
                _log(
                    f"[profile-{self.profile_id}] runner.run unhandled "
                    f"exception (BUG): {type(e).__name__}: {e}"
                )
                result = {
                    "id": req.get("id", ""),
                    "status": "error",
                    "error": f"{type(e).__name__}: {e}",
                    "code": "ErrInternalRunner",
                }
            self.out_queue.put(result)

        _log(f"[profile-{self.profile_id}] shutting down...")
        try:
            if self.session:
                self.session.close()
        except Exception as e:
            _log(f"[profile-{self.profile_id}] session.close failed: {e}")
        _log(f"[profile-{self.profile_id}] stopped")

    # ── Stop ──────────────────────────────────────────────────────────

    def stop(self) -> None:
        """Set the run-loop flag + push a sentinel into the input queue.

        The runner's while-loop checks `self._running` AND looks for
        `req is None` — either of these terminates the loop cleanly.
        """
        self._running = False
        try:
            self.in_queue.put_nowait(None)
        except queue.Full:
            pass


class SlideDispatcher:
    """Manages ProfileWorker thread(s) and request routing.

    Per the current single-profile contract (num_profiles is
    hard-clamped to 1 in __init__), only one ProfileWorker exists.
    Forward-expansion to multi-profile would route by hash(req["id"])
    % len(self.profiles). Today the contract stays single-profile to
    avoid Chrome profile_dir collisions in the host filesystem.

    `warmup_all` / `dispatch_generate` / `health_all` /
    `health_deep_all` / `shutdown_all` are the canonical operators
    invoked by the CLI shim (slide_worker.py). Their return shapes
    are part of the wire contract — do not change without a paired
    Go-side update.
    """

    def __init__(self, num_profiles: int, headful: bool = False, profile_id: int = 0) -> None:
        # Per the wave plan § "Single-profile model", num_profiles is
        # hard-clamped to 1; future expansion would update this.
        self.num_profiles = 1
        self.headful = headful
        self.profile_id = profile_id
        self.profiles: List[ProfileWorker] = []
        self._shutdown_called = False

    # ── Lifecycle ─────────────────────────────────────────────────────

    def warmup_all(self) -> dict:
        """Spawn one ProfileWorker thread + wait for warmup completion.

        On success: returns `{"status": "ready", "profiles": 1}`.
        On failure: returns `{"status": "error", "error": "..."}`
        with the canonical error string. The wait is bounded at 30s.
        """
        self.profiles = [ProfileWorker(self.profile_id, headful=self.headful)]
        for pw in self.profiles:
            pw.start()
        if not self.profiles[0]._warmed.wait(timeout=30):
            _log("slide_worker: profile-0 warmup timed out")
            return {"status": "error", "error": "profile-0 warmup timed out"}
        if self.profiles[0]._warmup_error:
            err = self.profiles[0]._warmup_error
            _log(f"slide_worker: profile-0 warmup failed immediately: {err}")
            return {"status": "error", "error": err}
        _log("slide_worker: profile ready")
        return {"status": "ready", "profiles": 1}

    def dispatch_generate(self, req: dict) -> dict:
        """Enqueue `req` to the (single) profile's in_queue + relay
        the response from out_queue to the caller.

        This is a synchronous BLOCKING call; the caller (CLI shim's
        main loop) waits for the runner to finish before forwarding
        the response on stdout. The blocking pattern matches the
        legacy inline-on-stdthread contract (preserved end-to-end).
        """
        pw = self.profiles[0]
        pw.in_queue.put(req)
        _log(f"slide_worker: [{req.get('id', '')}] dispatched to profile-{pw.profile_id}")
        return pw.out_queue.get()

    def health_all(self) -> dict:
        """Aggregate health status across all profiles.

        Returns `{"status": "ok" | "degraded", "profiles": {"0": "..."}}`.
        """
        statuses = {}
        all_ok = True
        for pw in self.profiles:
            if not pw._warmed.is_set():
                statuses[str(pw.profile_id)] = "error: not warmed"
                all_ok = False
                continue
            if not pw.is_alive():
                statuses[str(pw.profile_id)] = "error: thread died"
                all_ok = False
                continue
            if pw.session is None:
                statuses[str(pw.profile_id)] = "error: no session"
                all_ok = False
                continue
            h = pw.session.health()
            statuses[str(pw.profile_id)] = h["status"]
            if h["status"] != "ok":
                all_ok = False
        return {"status": "ok" if all_ok else "degraded", "profiles": statuses}

    def health_deep_all(self) -> dict:
        """Forward the first profile's health_deep() result.

        Pre-refactor semantics: aggregators above us tolerate a
        single-profile return shape on the wire.
        """
        if not self.profiles:
            return {
                "status": "error",
                "panel_ok": False,
                "textarea_ok": False,
                "image_mode_selectable": False,
                "profile_healthy": False,
                "failure_reason": "no_profiles_loaded",
            }
        return self.profiles[0].session.health_deep()

    def shutdown_all(self) -> None:
        """Idempotent shutdown: stop threads + join with bounded timeout.

        Idempotent: double-shutdown calls (e.g. SIGINT + main-loop
        finally) won't double-stop the profile threads.
        """
        if self._shutdown_called:
            return
        self._shutdown_called = True
        _log("slide_worker: shutting down all profiles...")
        for pw in self.profiles:
            pw.stop()
        for pw in self.profiles:
            pw.join(timeout=10)
            if pw.is_alive():
                _log(f"slide_worker: profile-{pw.profile_id} did not stop within 10s")
        _log("slide_worker: all profiles stopped")
